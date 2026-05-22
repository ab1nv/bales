"""Locust load test for the full HTTP -> Redis -> Batcher pipeline.

Run: uv run locust -f benchmarks/locustfile.py --headless -u 500 -r 100 --run-time 60s --host http://localhost:8000

Targets:
    - Throughput: >8,000 req/s
    - P99 latency: <12ms at batch_size=32

Interpretation:
    - If throughput is low: increase -u (concurrent users)
    - If P99 is high: reduce BATCH_WINDOW_MS or increase THREAD_POOL_SIZE
    - If errors appear: check /health for queue backlog
"""

import uuid

from locust import HttpUser, between, task


class InferenceUser(HttpUser):
    # Minimal wait time between tasks = maximum throughput
    wait_time = between(0.0001, 0.001)

    @task(9)  # 90% normal inference
    def infer_normal(self):
        self.client.post("/infer", json={
            "request_id": str(uuid.uuid4()),
            "model_id": "stub_v1",
            "model_type": "classification",
            "priority": 2,  # NORMAL
            "payload": {"input": [0.1] * 128},
        }, name="/infer (NORMAL)")

    @task(1)  # 10% high priority
    def infer_high(self):
        self.client.post("/infer", json={
            "request_id": str(uuid.uuid4()),
            "model_id": "stub_v1",
            "model_type": "classification",
            "priority": 1,  # HIGH
            "payload": {"input": [0.9] * 128},
        }, name="/infer (HIGH)
