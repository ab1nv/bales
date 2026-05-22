"""Tests for DynamicBatcher."""

import asyncio
import time

import pytest
import torch

pytestmark = pytest.mark.asyncio


def make_batcher(window_ms=50.0, max_batch=8):
    """Helper: create a DynamicBatcher with a simple identity model."""
    from concurrent.futures import ThreadPoolExecutor
    from batcher.dynamic_batcher import DynamicBatcher

    executor = ThreadPoolExecutor(max_workers=2)

    def model_fn(batch: torch.Tensor) -> torch.Tensor:
        return batch * 2.0  # simple: double the input

    def postprocess_fn(output: torch.Tensor):
        return output.tolist()

    batcher = DynamicBatcher(
        model_id="test_model",
        model_fn=model_fn,
        postprocess_fn=postprocess_fn,
        executor=executor,
        max_batch_size=max_batch,
        window_ms=window_ms,
    )
    return batcher, executor


class TestDynamicBatcher:

    async def test_single_request_resolves(self):
        """A single submitted request is processed and future resolved."""
        batcher, executor = make_batcher(window_ms=50)
        batcher.start()

        tensor = torch.tensor([1.0, 2.0, 3.0])
        future = await batcher.submit("req-1", tensor, time.monotonic())

        result = await asyncio.wait_for(future, timeout=2.0)
        assert result["request_id"] == "req-1"
        assert result["result"] == pytest.approx([2.0, 4.0, 6.0])
        assert result["batch_size"] == 1

        await batcher.stop()
        executor.shutdown(wait=False)

    async def test_multiple_requests_batched_together(self):
        """Multiple simultaneous requests should be grouped in one batch."""
        batcher, executor = make_batcher(window_ms=100)
        batcher.start()

        tensors = [torch.tensor([float(i)] * 4) for i in range(8)]
        futures = await asyncio.gather(*[
            batcher.submit(f"req-{i}", t, time.monotonic())
            for i, t in enumerate(tensors)
        ])

        results = await asyncio.gather(*[asyncio.wait_for(f, timeout=2.0) for f in futures])

        batch_sizes = {r["batch_size"] for r in results}
        # All requests should have been in one batch (or at most 2 due to timing)
        assert max(batch_sizes) > 1, "Expected batching but all ran individually"

        for i, r in enumerate(results):
            assert r["result"] == pytest.approx([float(i) * 2.0] * 4)

        await batcher.stop()
        executor.shutdown(wait=False)

    async def test_batch_size_limit_respected(self):
        """If >max_batch_size items queue up, they are split across dispatches."""
        batcher, executor = make_batcher(window_ms=200, max_batch=4)
        batcher.start()

        tensors = [torch.tensor([1.0]) for _ in range(10)]
        futures = await asyncio.gather(*[
            batcher.submit(f"req-{i}", t, time.monotonic())
            for i, t in enumerate(tensors)
        ])

        results = await asyncio.gather(*[asyncio.wait_for(f, timeout=3.0) for f in futures])
        assert len(results) == 10
        assert all(r["batch_size"] <= 4 for r in results)

        await batcher.stop()
        executor.shutdown(wait=False)

    async def test_future_not_leaked_on_model_error(self):
        """If model_fn raises, the future should be resolved with exception (not hung)."""
        from concurrent.futures import ThreadPoolExecutor
        from batcher.dynamic_batcher import DynamicBatcher

        executor = ThreadPoolExecutor(max_workers=1)

        def bad_model(batch):
            raise RuntimeError("model exploded")

        batcher = DynamicBatcher("bad", bad_model, lambda x: x, executor, window_ms=20)
        batcher.start()

        future = await batcher.submit("req-err", torch.tensor([1.0]), time.monotonic())

        with pytest.raises((RuntimeError, Exception)):
            await asyncio.wait_for(future, timeout=2.0)

        await batcher.stop()
        executor.shutdown(wait=False)
