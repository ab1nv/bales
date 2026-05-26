"""Isolated batcher benchmark -- no HTTP overhead, no Redis.

Run: uv run python benchmarks/profile_batcher.py

Tests pure batching throughput. Use this to tune window_ms and max_batch_size
before load testing the full stack.
"""

import asyncio
import time

import torch
from concurrent.futures import ThreadPoolExecutor

from batcher.dynamic_batcher import DynamicBatcher


async def main():
    N = 10000  # total requests
    BATCH_SIZE = 32
    WINDOW_MS = 5.0

    executor = ThreadPoolExecutor(max_workers=4)

    call_count = 0

    def model_fn(batch: torch.Tensor) -> torch.Tensor:
        nonlocal call_count
        call_count += 1
        return batch * 1.0

    def postprocess_fn(t):
        return t.tolist()

    batcher = DynamicBatcher(
        "bench",
        model_fn,
        postprocess_fn,
        executor,
        max_batch_size=BATCH_SIZE,
        window_ms=WINDOW_MS,
    )
    batcher.start()

    tensors = [torch.rand(128) for _ in range(N)]
    start = time.monotonic()
    enqueue_time = start

    futures = await asyncio.gather(
        *[batcher.submit(f"req-{i}", tensors[i], enqueue_time) for i in range(N)]
    )
    results = await asyncio.gather(
        *[asyncio.wait_for(f, timeout=30.0) for f in futures]
    )

    elapsed = time.monotonic() - start
    throughput = N / elapsed
    latencies = [r["latency_ms"] for r in results]
    latencies.sort()
    p50 = latencies[int(N * 0.50)]
    p99 = latencies[int(N * 0.99)]
    avg_batch = sum(r["batch_size"] for r in results) / N

    print("\n=== Batcher Benchmark ===")
    print(f"Requests:          {N:,}")
    print(f"Elapsed:           {elapsed:.2f}s")
    print(f"Throughput:        {throughput:,.0f} req/s")
    print(f"P50 latency:       {p50:.2f}ms")
    print(f"P99 latency:       {p99:.2f}ms")
    print(f"Avg batch size:    {avg_batch:.1f}")
    print(
        f"Model calls:       {call_count:,}  (vs {N} individual = {N / call_count:.1f}x reduction)"
    )
    print("Target:            >8,000 req/s, P99 <12ms")
    print(f"Pass:              {'PASS' if throughput > 8000 and p99 < 12 else 'FAIL'}")

    await batcher.stop()
    executor.shutdown(wait=False)


if __name__ == "__main__":
    asyncio.run(main())
