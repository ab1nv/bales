"""Background task that polls Redis queue and feeds requests into DynamicBatcher."""

import asyncio
import logging
import time

from api.schemas import InferenceRequest
from batcher.dynamic_batcher import batcher_registry
from config import get_settings
from models.registry import model_registry
from queue.priority_queue import priority_queue

logger = logging.getLogger(__name__)
settings = get_settings()

# Global dict: request_id -> asyncio.Future
# Populated by routes.py when request arrives
# Resolved here when batcher finishes
# This is the SINGLE shared state linking HTTP layer to batcher
pending_futures: dict[str, asyncio.Future] = {}


async def consume_loop(model_type: str, poll_interval_s: float = 0.001) -> None:
    """Continuously poll Redis queue for one model_type.

    For each item popped:
        1. Look up the future in pending_futures (created by routes.py)
        2. Look up the model in model_registry
        3. Preprocess payload -> tensor
        4. Submit to batcher
        5. When batcher resolves the batcher future, resolve the pending future

    poll_interval_s = 1ms: tight loop for low latency.
    If queue is empty, sleep poll_interval_s to avoid busy-spin on Redis.

    CRITICAL: This function must never resolve pending_futures directly.
    Only the batcher resolves them. This function only SUBMITS to batcher.
    The result propagation is:
        batcher future resolved -> on_done callback -> pending future resolved
    """
    logger.info(f"[consumer] starting loop for model_type='{model_type}'")

    while True:
        try:
            items = await priority_queue.pop_batch(model_type, settings.max_batch_size)

            if not items:
                await asyncio.sleep(poll_interval_s)
                continue

            for item_dict in items:
                try:
                    req = InferenceRequest(**item_dict)
                    await _handle_item(req)
                except Exception as e:
                    logger.error(f"[consumer] failed to handle item: {e} -- item={str(item_dict)[:100]}")
                    # Resolve the pending future with error if it exists
                    fut = pending_futures.pop(item_dict.get("request_id", ""), None)
                    if fut and not fut.done():
                        fut.set_exception(e)

        except asyncio.CancelledError:
            logger.info(f"[consumer] loop for '{model_type}' cancelled")
            break
        except Exception as e:
            logger.error(f"[consumer] unexpected error in loop: {e}")
            await asyncio.sleep(0.1)  # brief backoff on unexpected errors


async def _handle_item(req: InferenceRequest) -> None:
    """Process one dequeued item:
    1. Retrieve model (raises KeyError if not registered -- should not happen in prod)
    2. Preprocess payload -> tensor (raises if payload malformed)
    3. Get or create batcher for this model_id
    4. Submit to batcher -> get batcher_future
    5. Attach callback: when batcher_future resolves, resolve pending_future
    """
    enqueue_time = time.monotonic()

    model = model_registry.get(req.model_id)
    input_tensor = model.preprocess(req.payload)

    batcher = batcher_registry.get_or_create(
        model_id=req.model_id,
        model_fn=model.run_batch,
        postprocess_fn=model.postprocess,
    )

    batcher_future = await batcher.submit(req.request_id, input_tensor, enqueue_time)

    def on_batcher_done(bf: asyncio.Future) -> None:
        """Callback fires when batcher resolves or errors.

        Propagates result/exception to the HTTP-layer future.
        """
        pending_fut = pending_futures.pop(req.request_id, None)
        if pending_fut is None:
            # Caller already timed out and cleaned up. Discard result silently.
            return
        if pending_fut.done():
            # Already cancelled by timeout. Discard.
            return
        if bf.exception():
            pending_fut.set_exception(bf.exception())
        else:
            pending_fut.set_result(bf.result())

    batcher_future.add_done_callback(on_batcher_done)


# Track running consumer tasks so main.py can cancel them
_consumer_tasks: dict[str, asyncio.Task] = {}


def start_consumer(model_type: str) -> None:
    """Start a consumer loop for a model_type if not already running."""
    if model_type in _consumer_tasks and not _consumer_tasks[model_type].done():
        return
    task = asyncio.create_task(consume_loop(model_type), name=f"consumer-{model_type}")
    _consumer_tasks[model_type] = task
    logger.info(f"[consumer] started task for model_type='{model_type}'")


async def stop_all_consumers() -> None:
    for model_type, task in _consumer_tasks.items():
        task.cancel()
        try:
            await task
        except asyncio.CancelledError:
            pass
    _consumer_tasks.clear()
