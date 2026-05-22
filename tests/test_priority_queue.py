"""Tests for RedisPriorityQueue.

All tests use pytest-asyncio. Mark file:
How to run: uv run pytest tests/test_priority_queue.py -v
Requires: Redis running on localhost:6379
"""

import asyncio

import pytest
import pytest_asyncio

pytestmark = pytest.mark.asyncio


class TestRedisPriorityQueue:
    """Tests for RedisPriorityQueue.
    All tests use a separate Redis DB (db=15) and flush it before each test.
    """

    @pytest_asyncio.fixture(autouse=True)
    async def setup(self):
        import redis.asyncio as aioredis
        self.r = aioredis.from_url("redis://localhost:6379/15", decode_responses=True)
        await self.r.flushdb()
        yield
        await self.r.flushdb()
        await self.r.aclose()

    async def test_push_and_pop_single(self):
        """Pushing one item and popping it returns that item."""
        from api.schemas import InferenceRequest, Priority
        from queues.priority_queue import RedisPriorityQueue
        pq = RedisPriorityQueue("redis://localhost:6379/15")

        req = InferenceRequest(
            model_id="stub_v1",
            model_type="classification",
            priority=Priority.NORMAL,
            payload={"input": [0.1] * 128},
        )
        await pq.push(req)
        items = await pq.pop_batch("classification", max_size=10)

        assert len(items) == 1
        assert items[0]["request_id"] == req.request_id

    async def test_priority_ordering(self):
        """CRITICAL requests are popped before LOW requests regardless of insertion order."""
        from api.schemas import InferenceRequest, Priority
        from queues.priority_queue import RedisPriorityQueue
        pq = RedisPriorityQueue("redis://localhost:6379/15")

        low_req = InferenceRequest(model_id="m", model_type="t", priority=Priority.LOW, payload={"input": [0.1]*128})
        crit_req = InferenceRequest(model_id="m", model_type="t", priority=Priority.CRITICAL, payload={"input": [0.1]*128})

        # Push LOW first, then CRITICAL
        await pq.push(low_req)
        await asyncio.sleep(0.001)  # ensure different timestamps
        await pq.push(crit_req)

        items = await pq.pop_batch("t", max_size=2)
        assert len(items) == 2
        # First popped should be CRITICAL (lower score)
        assert items[0]["request_id"] == crit_req.request_id
        assert items[1]["request_id"] == low_req.request_id

    async def test_fifo_within_priority(self):
        """Within the same priority, earlier arrivals are popped first."""
        from api.schemas import InferenceRequest, Priority
        from queues.priority_queue import RedisPriorityQueue
        pq = RedisPriorityQueue("redis://localhost:6379/15")

        reqs = []
        for i in range(5):
            r = InferenceRequest(model_id="m", model_type="t", priority=Priority.NORMAL, payload={"input": [float(i)]*128})
            reqs.append(r)
            await pq.push(r)
            await asyncio.sleep(0.001)

        items = await pq.pop_batch("t", max_size=5)
        popped_ids = [i["request_id"] for i in items]
        expected_ids = [r.request_id for r in reqs]
        assert popped_ids == expected_ids

    async def test_pop_empty_returns_empty_list(self):
        from queues.priority_queue import RedisPriorityQueue
        pq = RedisPriorityQueue("redis://localhost:6379/15")
        items = await pq.pop_batch("nonexistent_type", max_size=10)
        assert items == []

    async def test_pop_respects_max_size(self):
        from api.schemas import InferenceRequest
        from queues.priority_queue import RedisPriorityQueue
        pq = RedisPriorityQueue("redis://localhost:6379/15")

        for _ in range(20):
            r = InferenceRequest(model_id="m", model_type="t", payload={"input": [0.1]*128})
            await pq.push(r)

        items = await pq.pop_batch("t", max_size=5)
        assert len(items) == 5
        remaining = await pq.length("t")
        assert remaining == 15
