"""Full integration test: real FastAPI app, real Redis, real batcher.

Requires: Redis on localhost:6379
Run: uv run pytest tests/test_integration.py -v
"""

import pytest
import pytest_asyncio
from httpx import ASGITransport, AsyncClient

pytestmark = pytest.mark.asyncio


@pytest_asyncio.fixture
async def app():
    """Create and start the full app. Yields ASGI app."""
    from main import create_app

    application = create_app()
    async with application.router.lifespan_context(application):
        yield application


@pytest_asyncio.fixture
async def client(app):
    async with AsyncClient(
        transport=ASGITransport(app=app), base_url="http://test"
    ) as c:
        yield c


class TestIntegration:
    async def test_health_returns_registered_model(self, client):
        r = await client.get("/health")
        assert r.status_code == 200
        data = r.json()
        assert "stub_v1" in data["registered_models"]
        assert data["status"] == "ok"

    async def test_infer_returns_result(self, client):
        payload = {
            "model_id": "stub_v1",
            "model_type": "classification",
            "payload": {"input": [0.5] * 128},
        }
        r = await client.post("/infer", json=payload)
        assert r.status_code == 200
        data = r.json()
        assert "result" in data
        assert data["result"]["label"] in range(10)
        assert 0.0 < data["result"]["confidence"] <= 1.0
        assert data["batch_size"] >= 1

    async def test_infer_unknown_model_returns_404(self, client):
        payload = {
            "model_id": "does_not_exist",
            "model_type": "classification",
            "payload": {"input": [0.1] * 128},
        }
        r = await client.post("/infer", json=payload)
        assert r.status_code == 404

    async def test_infer_bad_payload_returns_500(self, client):
        """Sending wrong-length input causes preprocess to raise -> 500."""
        payload = {
            "model_id": "stub_v1",
            "model_type": "classification",
            "payload": {"input": [0.1] * 10},  # wrong length
        }
        r = await client.post("/infer", json=payload)
        assert r.status_code in (500, 504)

    async def test_concurrent_requests(self, client):
        """50 concurrent requests all succeed."""
        import asyncio

        payloads = [
            {
                "model_id": "stub_v1",
                "model_type": "classification",
                "payload": {"input": [float(i % 10) / 10] * 128},
            }
            for i in range(50)
        ]
        responses = await asyncio.gather(
            *[client.post("/infer", json=p) for p in payloads]
        )
        statuses = [r.status_code for r in responses]
        assert all(s == 200 for s in statuses), (
            f"Some failed: {[s for s in statuses if s != 200]}"
        )
