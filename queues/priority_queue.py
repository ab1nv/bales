"""Redis-backed priority queue using sorted sets (ZSET)."""

import json
import logging
import time
from typing import Any

import redis.asyncio as aioredis

from api.schemas import InferenceRequest, Priority
from config import get_settings

logger = logging.getLogger(__name__)
settings = get_settings()


class RedisPriorityQueue:
    """One ZSET per model_type: key = "bales:queue:{model_type}"

    Score formula:
        score = priority_value * 1_000_000_000_000 + unix_time_microseconds

    Priority values: CRITICAL=0, HIGH=1, NORMAL=2, LOW=3
    Lower score = popped first by ZPOPMIN

    Example scores:
        CRITICAL at t=1700000000.000001 -> 0 * 1e12 + 1700000000000001 = 1700000000000001
        LOW      at t=1700000000.000001 -> 3 * 1e12 + 1700000000000001 = 4700000000000001
    So CRITICAL always < LOW score -> CRITICAL always popped first.

    Within same priority (same multiplier), earlier timestamp -> lower score -> popped first.
    This preserves FIFO within a priority band.
    """

    QUEUE_PREFIX = "bales:queue"

    def __init__(self, redis_url: str | None = None):
        url = redis_url or settings.redis_url
        self._redis = aioredis.from_url(url, decode_responses=True)

    def _key(self, model_type: str) -> str:
        return f"{self.QUEUE_PREFIX}:{model_type}"

    def _score(self, priority: Priority) -> float:
        """Compute ZSET score. Lower = higher priority."""
        ts_us = int(time.time() * 1_000_000)
        return float(priority.value * 1_000_000_000_000 + ts_us)

    async def push(self, request: InferenceRequest) -> None:
        """Enqueue one request. Serializes full InferenceRequest as JSON.

        If the same request_id is pushed twice, the second push overwrites
        the score (Redis ZADD default behavior) -- this is acceptable.
        """
        key = self._key(request.model_type)
        score = self._score(request.priority)
        member = request.model_dump_json()
        await self._redis.zadd(key, {member: score})
        logger.debug(
            f"[queue] pushed request_id={request.request_id} model_type={request.model_type} priority={request.priority.name} score={score}"
        )

    async def pop_batch(self, model_type: str, max_size: int) -> list[dict[str, Any]]:
        """Atomically pop up to max_size highest-priority items from model_type's queue.

        Returns list of deserialized request dicts (same structure as InferenceRequest).
        Returns empty list if queue is empty.

        Uses ZPOPMIN which is atomic -- no two consumers will receive the same item.
        """
        key = self._key(model_type)
        # ZPOPMIN returns list of (member, score) tuples
        results: list[tuple[str, float]] = await self._redis.zpopmin(key, max_size)
        deserialized = []
        for member, score in results:
            try:
                deserialized.append(json.loads(member))
            except json.JSONDecodeError:
                logger.error(
                    f"[queue] failed to deserialize queue member: {member[:100]}"
                )
        return deserialized

    async def length(self, model_type: str) -> int:
        """Return number of items waiting in queue for this model_type."""
        return await self._redis.zcard(self._key(model_type))

    async def all_lengths(self) -> dict[str, int]:
        """Return {model_type: queue_length} for all known queues.

        Scans Redis for matching keys -- O(N) on keyspace, only use for health checks.
        """
        pattern = f"{self.QUEUE_PREFIX}:*"
        cursor = 0
        result = {}
        while True:
            cursor, keys = await self._redis.scan(cursor, match=pattern, count=100)
            for key in keys:
                model_type = key.split(":")[-1]
                result[model_type] = await self._redis.zcard(key)
            if cursor == 0:
                break
        return result

    async def close(self) -> None:
        await self._redis.aclose()


# Module-level singleton
priority_queue = RedisPriorityQueue()
