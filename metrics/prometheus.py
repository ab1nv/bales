"""Prometheus metrics for observability."""

from prometheus_client import (
    CONTENT_TYPE_LATEST,
    Counter,
    Gauge,
    Histogram,
    generate_latest,
)

# Total inference requests, labelled by model_id and status (success|error|timeout)
REQUEST_COUNT = Counter(
    "bales_requests_total",
    "Total inference requests",
    ["model_id", "status"],
)

# End-to-end latency: from POST /infer received to response sent (ms)
REQUEST_LATENCY = Histogram(
    "bales_request_latency_ms",
    "End-to-end request latency in milliseconds",
    ["model_id"],
    buckets=[0.5, 1, 2, 5, 10, 20, 50, 100, 250, 500],
)

# Batch size distribution -- tells you if batching is working
BATCH_SIZE = Histogram(
    "bales_batch_size",
    "Number of requests in each dispatched batch",
    ["model_id"],
    buckets=[1, 2, 4, 8, 16, 24, 32, 48, 64],
)

# Queue depth -- real-time backpressure indicator
QUEUE_DEPTH = Gauge(
    "bales_queue_depth",
    "Number of requests waiting in priority queue",
    ["model_type"],
)


def record_request(
    model_id: str, status: str, latency_ms: float, batch_size: int
) -> None:
    """Call this once per completed (or failed) request."""
    REQUEST_COUNT.labels(model_id=model_id, status=status).inc()
    REQUEST_LATENCY.labels(model_id=model_id).observe(latency_ms)
    if status == "success":
        BATCH_SIZE.labels(model_id=model_id).observe(batch_size)


def update_queue_depth(model_type: str, depth: int) -> None:
    QUEUE_DEPTH.labels(model_type=model_type).set(depth)


def render_metrics() -> bytes:
    """Return latest Prometheus metrics as bytes."""
    return generate_latest()


def metrics_content_type() -> str:
    """Return the correct Content-Type for Prometheus metrics."""
    return CONTENT_TYPE_LATEST
