# BALES - High-Throughput ML Inference Gateway

<p align="center">
  <!-- Logo placeholder - add your logo here -->
  <img src="docs/assets/logo.png" alt="BALES Logo" width="180" />
</p>

<p align="center">
  <strong>Zero-downtime inference with dynamic batching and priority scheduling.</strong>
</p>

<p align="center">
  <a href="https://www.python.org/"><img src="https://img.shields.io/badge/python-3.14-blue?logo=python" alt="Python 3.14" /></a>
  <a href="https://fastapi.tiangolo.com/"><img src="https://img.shields.io/pypi/v/fastapi?label=FastAPI&logo=fastapi" alt="FastAPI" /></a>
  <a href="https://redis.io/"><img src="https://img.shields.io/badge/Redis-7.4-DC382D?logo=redis" alt="Redis" /></a>
  <a href="https://pytorch.org/"><img src="https://img.shields.io/pypi/v/torch?label=PyTorch&logo=pytorch" alt="PyTorch" /></a>
  <a href="https://prometheus.io/"><img src="https://img.shields.io/pypi/v/prometheus-client?label=Prometheus&logo=prometheus" alt="Prometheus" /></a>
  <a href="https://www.docker.com/"><img src="https://img.shields.io/badge/Docker-latest-2496ED?logo=docker" alt="Docker" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-yellow.svg" alt="License: MIT" /></a>
</p>

<p align="center">
  <a href="#-quick-start">Quick Start</a> &bull;
  <a href="#-architecture">Architecture</a> &bull;
  <a href="#-configuration">Configuration</a> &bull;
  <a href="#-benchmarking">Benchmarking</a> &bull;
  <a href="#-security">Security</a> &bull;
  <a href="#-api-reference">API</a>
</p>

---

## Overview

BALES is a production-ready inference gateway designed for high-throughput CPU-based ML serving. It combines **Redis-backed priority queues**, **dynamic request batching**, and **atomic model hot-swapping** to deliver:

- **>8,000 req/s** throughput on CPU
- **P99 latency <12ms** at `batch_size=32`
- **Zero-downtime** model reloads without dropping in-flight requests

Built with **FastAPI**, **PyTorch**, and **asyncio**, BALES is engineered for safety-first concurrency: inference never blocks the event loop, and every request future is guaranteed to resolve or time out cleanly.

## Table of Contents

- [Overview](#overview)
- [Quick Start](#-quick-start)
  - [Local Development](#local-development)
  - [Docker](#docker)
- [Architecture](#-architecture)
  - [Data Flow](#data-flow)
  - [Key Invariants](#key-invariants)
- [Configuration](#-configuration)
- [Benchmarking](#-benchmarking)
  - [Isolated Batcher](#isolated-batcher)
  - [Full-Stack Load Test](#full-stack-load-test)
- [Security](#-security)
- [API Reference](#-api-reference)
  - [`POST /infer`](#post-infer)
  - [`GET /health`](#get-health)
  - [`POST /models/{model_id}/reload`](#post-modelsmodel_idreload)
  - [`GET /metrics`](#get-metrics)
- [Development](#-development)

---

## Quick Start

### Local Development

**Prerequisites:** Python 3.14+, Redis 7+, [uv](https://docs.astral.sh/uv/)

```bash
# 1. Clone the repository
git clone https://github.com/yourusername/bales.git
cd bales

# 2. Install dependencies (first time)
uv sync --extra dev

# 3. Start Redis (if not already running)
redis-server --save "" --appendonly no

# 4. Run the server
uv run python main.py
```

The gateway will be available at `http://localhost:8000`.

### Docker

```bash
# Build and start everything (Redis + Bales)
docker compose up --build

# Optional: include Prometheus for metrics scraping
docker compose --profile monitoring up --build
```

---

## Architecture

### Data Flow

```
[Client] --POST /infer--> [FastAPI Routes]
                                |
                                v
                        [Redis Priority Queue]
                                |
                                v
                        [Consumer Loop]
                                |
                                v
                        [Dynamic Batcher]
                                |
                                v
                        [PyTorch run_in_executor]
                                |
                                v
                        [Response Future resolved]
                                |
                                v
                        [Client receives JSON]
```

### Key Invariants

1. **PyTorch inference NEVER runs on the event loop thread** -- always dispatched via `run_in_executor`.
2. **A request NEVER touches a half-loaded model during hot-swap** -- atomic reference replacement under an async lock.
3. **A request NEVER gets dropped during hot-swap** -- in-flight requests hold a local reference to the old model until GC cleans up.
4. **`request_id`** is the single source of truth linking API -> queue -> batcher -> response.
5. **`pending_futures`** is the ONLY place futures are stored.

---

## Configuration

All configuration is read from environment variables (with sensible defaults). Create a `.env` file from the example:

```bash
cp .env.example .env
```

| Variable | Default | Description |
|----------|---------|-------------|
| `REDIS_URL` | `redis://localhost:6379/0` | Redis connection string |
| `MAX_BATCH_SIZE` | `32` | Maximum requests per batch |
| `BATCH_WINDOW_MS` | `5.0` | Collection window in milliseconds |
| `BATCHER_TIMEOUT_S` | `5.0` | Client timeout before 504 |
| `DEFAULT_MODEL_ID` | `stub_v1` | Default registered model |
| `THREAD_POOL_SIZE` | `4` | Executor threads for torch inference |
| `HOST` | `0.0.0.0` | Server bind host |
| `PORT` | `8000` | Server port |
| `LOG_LEVEL` | `info` | Logging level |
| `ENABLE_PROMETHEUS` | `true` | Enable metrics export |

> **Note:** `workers` must remain `1` for in-process shared state (`pending_futures`). Scale horizontally with Docker replicas instead.

---

## Benchmarking

### Isolated Batcher

Test pure batching throughput (no HTTP or Redis overhead):

```bash
uv run python benchmarks/profile_batcher.py
```

Targets:
- Throughput: **>8,000 req/s**
- P99 latency: **<12ms** at `batch_size=32`

### Full-Stack Load Test

Use Locust to benchmark the complete HTTP -> Redis -> Batcher pipeline:

```bash
uv run locust -f benchmarks/locustfile.py \
  --headless -u 500 -r 100 \
  --run-time 60s --host http://localhost:8000
```

Tuning tips:
- If throughput is low -> increase concurrent users (`-u`).
- If P99 is high -> reduce `BATCH_WINDOW_MS` or increase `THREAD_POOL_SIZE`.
- If errors appear -> check `/health` for queue backlog.

---

## Security

BALES follows security best practices:

- **Input validation:** All requests are validated via Pydantic v2 before entering the pipeline.
- **No shell execution:** `weights_path` in hot-swap is validated and never passed to shell commands.
- **Resource limits:** Docker Compose enforces CPU (`4.0`) and memory (`2G`) caps.
- **No Redis persistence:** Queue data is ephemeral by design (`--save "" --appendonly no`) to avoid I/O overhead and accidental data retention.
- **Single worker:** Prevents shared-state corruption; horizontal scaling is done via container replicas behind a load balancer.
- **Healthchecks:** Docker `HEALTHCHECK` polls `/health` every 10s to detect degraded state.

---

## API Reference

### `POST /infer`

Submit an inference request.

**Request body:**

```json
{
  "model_id": "stub_v1",
  "model_type": "classification",
  "priority": 2,
  "payload": {
    "input": [0.1, 0.2, ...]
  }
}
```

**Response:**

```json
{
  "request_id": "uuid",
  "model_id": "stub_v1",
  "result": { "label": 3, "confidence": 0.95 },
  "latency_ms": 4.123,
  "batch_size": 16,
  "queued_ms": 1.234
}
```

### `GET /health`

Returns system health, registered models, queue depths, and pending request count.

### `POST /models/{model_id}/reload`

Hot-swap a model's weights without dropping traffic.

**Request body:**

```json
{
  "weights_path": "./weights/new_model.pt"
}
```

### `GET /metrics`

Prometheus scrape endpoint exposing `bales_requests_total`, `bales_request_latency_ms`, `bales_batch_size`, and `bales_queue_depth`.

---

## Development

```bash
# Run the test suite (requires Redis on localhost:6379)
uv run pytest tests/ -v

# Run a specific test file
uv run pytest tests/test_integration.py -v

# Profile the batcher
uv run python benchmarks/profile_batcher.py
```
