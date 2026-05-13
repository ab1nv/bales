"""Core performance engine: dynamic request batching."""

import asyncio
import logging
import time
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass
from typing import Any, Callable

import torch

from config import get_settings

logger = logging.getLogger(__name__)
settings = get_settings()


@dataclass
class _QueueItem:
    """Internal: one request waiting to be batched."""

    request_id: str
    enqueue_time: float         # time.monotonic() when submitted to batcher
    input_tensor: torch.Tensor  # shape: [*input_dims], no batch dim
    future: asyncio.Future      # resolved with InferenceResponse dict when done


@dataclass
class BatchResult:
    """What the batcher resolves each future with."""

    request_id: str
    result: Any                 # postprocessed output
    latency_ms: float
    batch_size: int
    queued_ms: float            # time from submit() to dispatch


class DynamicBatcher:
    """Lifecycle:
        1. Create instance with model_fn, postprocess_fn, model_id
        2. Call start() -> spawns background asyncio.Task (_run loop)
        3. Callers call await submit(request_id, tensor) -> get Future
        4. _run loop collects items for window_ms or until max_batch_size
        5. Dispatches batch in ThreadPoolExecutor (never blocks event loop)
        6. Resolves all futures in the batch with their individual results
        7. Call stop() on shutdown -> cancels background task

    INVARIANT: model_fn is ONLY ever called inside run_in_executor.
    INVARIANT: futures are ALWAYS resolved (or set_exception) -- never leaked.
    INVARIANT: input tensors are stacked with torch.stack, not torch.cat.
               (stack adds batch dim; cat would concatenate on existing dim)
    """

    def __init__(
        self,
        model_id: str,
        model_fn: Callable[[torch.Tensor], torch.Tensor],
        postprocess_fn: Callable[[torch.Tensor], Any],
        executor: ThreadPoolExecutor,
        max_batch_size: int | None = None,
        window_ms: float | None = None,
    ):
        self.model_id = model_id
        self._model_fn = model_fn
        self._postprocess_fn = postprocess_fn
        self._executor = executor
        self._max_batch_size = max_batch_size or settings.max_batch_size
        self._window_s = (window_ms or settings.batch_window_ms) / 1000.0
        self._queue: asyncio.Queue[_QueueItem] = asyncio.Queue()
        self._task: asyncio.Task | None = None
        self._running = False

    def start(self) -> None:
        """Spawn the background collection loop.

        Must be called from within a running event loop (inside async context or lifespan).
        """
        if self._running:
            logger.warning(f"[batcher:{self.model_id}] already running, ignoring start()")
            return
        self._running = True
        self._task = asyncio.create_task(self._run(), name=f"batcher-{self.model_id}")
        logger.info(f"[batcher:{self.model_id}] started (window={self._window_s*1000:.1f}ms max_batch={self._max_batch_size})")

    async def stop(self) -> None:
        """Graceful shutdown:
        1. Stop accepting new items
        2. Drain remaining queue (dispatch partial batch)
        3. Cancel background task
        """
        self._running = False
        if self._task:
            # Give the loop one window to drain
            await asyncio.sleep(self._window_s * 2)
            self._task.cancel()
            try:
                await self._task
            except asyncio.CancelledError:
                pass
        logger.info(f"[batcher:{self.model_id}] stopped")

    async def submit(self, request_id: str, input_tensor: torch.Tensor, enqueue_time: float) -> asyncio.Future:
        """Submit one request to be batched.

        Returns a Future that will be resolved with a BatchResult dict.
        Caller should await the future with a timeout.

        enqueue_time: time.monotonic() when the request entered the system
                      (used to compute queued_ms accurately)
        """
        loop = asyncio.get_event_loop()
        future = loop.create_future()
        item = _QueueItem(
            request_id=request_id,
            enqueue_time=enqueue_time,
            input_tensor=input_tensor,
            future=future,
        )
        await self._queue.put(item)
        return future

    async def _run(self) -> None:
        """Main collection loop. Runs forever until stop() is called.

        Collection strategy:
            - Wait up to window_s for first item (asyncio.wait_for on Queue.get)
            - Once first item arrives, collect more until window expires OR batch full
            - Dispatch whatever was collected (even batch of 1)
            - Immediately start next window

        This ensures:
            - We never hold items longer than window_s regardless of load
            - Under high load, batches fill to max_batch_size quickly
            - Under low load, single requests don't wait longer than window_s
        """
        while self._running:
            batch: list[_QueueItem] = []

            # Step 1: Block until at least one item arrives
            try:
                first = await asyncio.wait_for(self._queue.get(), timeout=0.1)
                batch.append(first)
            except asyncio.TimeoutError:
                continue  # nothing arrived in 100ms, loop again

            # Step 2: Collect more until window expires or batch full
            deadline = time.monotonic() + self._window_s
            while len(batch) < self._max_batch_size:
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    break
                try:
                    item = await asyncio.wait_for(
                        self._queue.get(),
                        timeout=remaining
                    )
                    batch.append(item)
                except asyncio.TimeoutError:
                    break

            # Step 3: Dispatch
            if batch:
                # Fire-and-forget dispatch -- don't await, start next window immediately
                asyncio.create_task(self._dispatch(batch))

    async def _dispatch(self, batch: list[_QueueItem]) -> None:
        """Stack tensors -> run model in executor -> postprocess -> resolve futures.

        Any exception resolves ALL futures in the batch with that exception.
        """
        dispatch_time = time.monotonic()

        try:
            # Stack: list of [*input_dims] tensors -> [B, *input_dims]
            stacked = torch.stack([item.input_tensor for item in batch], dim=0)

            # Run model in thread pool (NEVER on event loop)
            loop = asyncio.get_event_loop()
            t_inference_start = time.monotonic()
            output_batch: torch.Tensor = await loop.run_in_executor(
                self._executor,
                self._model_fn,
                stacked
            )
            latency_ms = (time.monotonic() - t_inference_start) * 1000.0

            # Postprocess and resolve each future individually
            for i, item in enumerate(batch):
                if item.future.done():
                    continue  # already cancelled or timed out by caller
                try:
                    single_output = output_batch[i]
                    result = self._postprocess_fn(single_output)
                    queued_ms = (dispatch_time - item.enqueue_time) * 1000.0
                    item.future.set_result({
                        "request_id": item.request_id,
                        "result": result,
                        "latency_ms": round(latency_ms, 3),
                        "batch_size": len(batch),
                        "queued_ms": round(queued_ms, 3),
                    })
                except Exception as e:
                    logger.error(f"[batcher:{self.model_id}] postprocess error for {item.request_id}: {e}")
                    item.future.set_exception(e)

        except Exception as e:
            logger.error(f"[batcher:{self.model_id}] batch dispatch failed: {e}")
            for item in batch:
                if not item.future.done():
                    item.future.set_exception(e)


class BatcherRegistry:
    """Manages one DynamicBatcher per model_id.
    Created lazily when a model is first used.
    """

    def __init__(self, executor: ThreadPoolExecutor):
        self._batchers: dict[str, DynamicBatcher] = {}
        self._executor = executor

    def get_or_create(
        self,
        model_id: str,
        model_fn: Callable,
        postprocess_fn: Callable,
    ) -> DynamicBatcher:
        if model_id not in self._batchers:
            batcher = DynamicBatcher(
                model_id=model_id,
                model_fn=model_fn,
                postprocess_fn=postprocess_fn,
                executor=self._executor,
            )
            batcher.start()
            self._batchers[model_id] = batcher
            logger.info(f"[batcher_registry] created batcher for model_id='{model_id}'")
        return self._batchers[model_id]

    async def shutdown_all(self) -> None:
        for model_id, batcher in self._batchers.items():
            logger.info(f"[batcher_registry] stopping batcher for '{model_id}'")
            await batcher.stop()
        self._batchers.clear()


# Module-level singleton -- initialized in main.py with executor
batcher_registry: BatcherRegistry | None = None
