"""FastAPI route definitions. Three endpoints: POST /infer, GET /health,
POST /models/{model_id}/reload. This is where HTTP meets the async pipeline.
"""

import asyncio
import logging
import time
from pathlib import Path

from fastapi import APIRouter, HTTPException
from fastapi.responses import PlainTextResponse

from api.schemas import (
    ErrorResponse,
    HealthResponse,
    HotSwapRequest,
    HotSwapResponse,
    InferenceRequest,
    InferenceResponse,
)
from config import get_settings
from metrics.prometheus import record_request, render_metrics, metrics_content_type
from models.registry import model_registry
from models.stub_model import StubModel
from queues.consumer import pending_futures, start_consumer
from queues.priority_queue import priority_queue

logger = logging.getLogger(__name__)
settings = get_settings()
router = APIRouter()


@router.post(
    "/infer",
    response_model=InferenceResponse,
    responses={
        504: {"model": ErrorResponse, "description": "Inference timeout"},
        422: {"model": ErrorResponse, "description": "Validation error"},
        404: {"model": ErrorResponse, "description": "Model not found"},
    },
)
async def infer(req: InferenceRequest) -> InferenceResponse:
    """Accept one inference request. Flow:
        1. Validate model_id is registered
        2. Create a Future and store in pending_futures[request_id]
        3. Push request to Redis priority queue
        4. Ensure a consumer is running for this model_type
        5. Await the future with timeout
        6. Return InferenceResponse

    The future is resolved by consumer.py -> batcher -> on_done callback.
    This endpoint never calls the model directly.

    RACE CONDITION NOTE: Steps 2 and 3 must happen in this exact order.
    If we push to Redis first and the consumer pops it before we create the
    future, the callback in consumer.py will find no future and discard the
    result. Always create future BEFORE pushing to queue.
    """
    wall_start = time.monotonic()

    # Validate model exists before queuing
    if req.model_id not in model_registry:
        raise HTTPException(status_code=404, detail=f"Model '{req.model_id}' not registered")

    # Step 1: Create future BEFORE pushing to queue (see race condition note above)
    loop = asyncio.get_event_loop()
    future: asyncio.Future = loop.create_future()
    pending_futures[req.request_id] = future

    # Step 2: Push to Redis queue
    await priority_queue.push(req)

    # Step 3: Ensure consumer is running for this model_type
    start_consumer(req.model_type)

    # Step 4: Await result
    try:
        result_dict = await asyncio.wait_for(future, timeout=settings.batcher_timeout_s)
    except asyncio.TimeoutError:
        # Clean up -- future may already be gone if consumer resolved it between
        # TimeoutError raise and this line, so pop with default
        pending_futures.pop(req.request_id, None)
        wall_ms = (time.monotonic() - wall_start) * 1000
        record_request(req.model_id, "timeout", wall_ms, 0)
        raise HTTPException(
            status_code=504,
            detail=f"Inference timeout after {settings.batcher_timeout_s}s for request_id={req.request_id}"
        )
    except Exception as e:
        pending_futures.pop(req.request_id, None)
        wall_ms = (time.monotonic() - wall_start) * 1000
        record_request(req.model_id, "error", wall_ms, 0)
        raise HTTPException(status_code=500, detail=str(e))

    wall_ms = (time.monotonic() - wall_start) * 1000
    record_request(req.model_id, "success", wall_ms, result_dict["batch_size"])

    return InferenceResponse(
        request_id=result_dict["request_id"],
        model_id=req.model_id,
        result=result_dict["result"],
        latency_ms=result_dict["latency_ms"],
        batch_size=result_dict["batch_size"],
        queued_ms=result_dict["queued_ms"],
    )


@router.post(
    "/models/{model_id}/reload",
    response_model=HotSwapResponse,
)
async def reload_model(model_id: str, body: HotSwapRequest) -> HotSwapResponse:
    """Hot-swap a registered model's weights while serving live traffic.

    Flow:
        1. Validate model_id is currently registered
        2. Load new model from weights_path (in thread pool -- may be slow)
        3. Call model_registry.hot_swap() -> atomic reference swap
        4. Return success

    In-flight requests during this call are NOT dropped:
        - Requests already in batcher: use old model object (still in memory until GC)
        - New requests after swap: use new model object
        - The swap lock in registry ensures no request sees a half-constructed model
    """
    if model_id not in model_registry:
        raise HTTPException(status_code=404, detail=f"Model '{model_id}' not registered")

    # Security: validate weights_path is a real file path, not a command
    weights_path = Path(body.weights_path)
    if not weights_path.exists():
        raise HTTPException(status_code=400, detail=f"Weights file not found: {body.weights_path}")

    try:
        # Load new model in thread pool (may involve disk I/O)
        loop = asyncio.get_event_loop()
        new_model = await loop.run_in_executor(
            None,
            lambda: _load_model_from_path(model_id, str(weights_path))
        )
        await model_registry.hot_swap(model_id, new_model)

    except KeyError as e:
        raise HTTPException(status_code=404, detail=str(e))
    except FileNotFoundError:
        raise HTTPException(status_code=400, detail=f"Weights file not found: {body.weights_path}")
    except Exception as e:
        logger.error(f"[routes] hot-swap failed for {model_id}: {e}")
        raise HTTPException(status_code=500, detail=f"Hot-swap failed: {str(e)}")

    return HotSwapResponse(
        model_id=model_id,
        status="swapped",
        message=f"Model '{model_id}' reloaded from {body.weights_path}",
    )


def _load_model_from_path(model_id: str, weights_path: str):
    """Synchronous model loader -- runs in executor.

    For StubModel, weights_path is ignored.
    In production, this would: torch.load(weights_path), assign to model.
    """
    model = StubModel()
    return model


@router.get("/health", response_model=HealthResponse)
async def health() -> HealthResponse:
    """Returns:
        - registered model IDs
        - queue depths per model_type
        - number of in-flight (pending) requests
    """
    queue_lengths = await priority_queue.all_lengths()
    return HealthResponse(
        status="ok",
        registered_models=model_registry.list_models(),
        queue_lengths=queue_lengths,
        pending_requests=len(pending_futures),
    )


@router.get("/metrics")
async def metrics() -> PlainTextResponse:
    """Prometheus metrics scrape endpoint."""
    return PlainTextResponse(
        content=render_metrics().decode("utf-8"),
        media_type=metrics_content_type(),
    )
