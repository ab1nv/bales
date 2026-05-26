"""Pydantic models for all request/response shapes."""

import uuid
from enum import IntEnum
from typing import Any

from pydantic import BaseModel, Field, field_validator


class Priority(IntEnum):
    """
    Lower integer = higher urgency.
    Used to compute Redis ZSET score: score = priority * 1e12 + timestamp_us.
    """

    CRITICAL = 0
    HIGH = 1
    NORMAL = 2
    LOW = 3


class InferenceRequest(BaseModel):
    """Incoming inference request."""

    request_id: str = Field(default_factory=lambda: str(uuid.uuid4()))
    model_id: str = Field(..., description="Which registered model to run")
    model_type: str = Field(
        ..., description="Logical group (e.g. 'classification', 'embedding')"
    )
    priority: Priority = Priority.NORMAL
    payload: dict[str, Any] = Field(..., description="Raw input dict; model-specific")

    @field_validator("model_id")
    @classmethod
    def model_id_not_empty(cls, v: str) -> str:
        if not v.strip():
            raise ValueError("model_id cannot be empty")
        return v


class InferenceResponse(BaseModel):
    """Successful inference response."""

    request_id: str
    model_id: str
    result: Any
    latency_ms: float = Field(
        ..., description="Wall time from model call start to return"
    )
    batch_size: int = Field(..., description="How many requests were in the same batch")
    queued_ms: float = Field(
        ..., description="Time spent waiting in queue before batching"
    )


class HotSwapRequest(BaseModel):
    """Request to hot-swap a model's weights."""

    weights_path: str = Field(
        ..., description="Absolute or relative path to new .pt weights file"
    )


class HotSwapResponse(BaseModel):
    """Result of a hot-swap operation."""

    model_id: str
    status: str  # "swapped" | "failed"
    message: str


class HealthResponse(BaseModel):
    """Health check response."""

    status: str  # "ok" | "degraded"
    registered_models: list[str]
    queue_lengths: dict[str, int]
    pending_requests: int


class ErrorResponse(BaseModel):
    """Standard error response."""

    detail: str
    request_id: str | None = None
