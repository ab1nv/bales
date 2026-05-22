"""App factory, lifespan context manager (startup/shutdown), and wiring
all singletons together. This is the only place where global state is initialized.
"""

import asyncio
import logging
import logging.config
from concurrent.futures import ThreadPoolExecutor
from contextlib import asynccontextmanager

import uvicorn
from fastapi import FastAPI

import batcher.dynamic_batcher as batcher_module
from api.routes import router
from batcher.dynamic_batcher import BatcherRegistry
from config import get_settings
from models.registry import model_registry
from models.stub_model import StubModel
from queues.consumer import stop_all_consumers
from queues.priority_queue import priority_queue

settings = get_settings()

logging.basicConfig(
    level=settings.log_level.upper(),
    format="%(asctime)s %(levelname)s %(name)s -- %(message)s",
)
logger = logging.getLogger(__name__)


@asynccontextmanager
async def lifespan(app: FastAPI):
    """Startup:
        1. Create thread pool executor (for PyTorch inference)
        2. Create BatcherRegistry with that executor
        3. Register default models
        4. (Consumer loops are started lazily on first request -- see routes.py)

    Shutdown:
        1. Stop all consumer loops (stop popping from Redis)
        2. Shutdown all batchers (drain pending batches)
        3. Shutdown executor (wait for in-flight inferences)
        4. Close Redis connection
    """
    logger.info("[main] starting up Bales inference gateway")

    # Step 1: Thread pool for PyTorch
    executor = ThreadPoolExecutor(
        max_workers=settings.thread_pool_size,
        thread_name_prefix="bales-torch",
    )

    # Step 2: Wire batcher_registry singleton
    batcher_module.batcher_registry = BatcherRegistry(executor=executor)

    # Step 3: Register default stub model
    stub = StubModel()
    await model_registry.register(settings.default_model_id, stub)
    logger.info(f"[main] registered default model '{settings.default_model_id}'")

    logger.info("[main] startup complete -- ready to serve")

    yield  # <- server is running here

    # Shutdown sequence
    logger.info("[main] shutting down")
    await stop_all_consumers()
    await batcher_module.batcher_registry.shutdown_all()
    executor.shutdown(wait=True, cancel_futures=False)
    await priority_queue.close()
    logger.info("[main] shutdown complete")


def create_app() -> FastAPI:
    app = FastAPI(
        title="Bales Inference Gateway",
        description="High-throughput ML inference with dynamic batching and priority scheduling",
        version="1.0.0",
        lifespan=lifespan,
    )
    app.include_router(router)
    return app


app = create_app()


if __name__ == "__main__":
    uvicorn.run(
        "main:app",
        host=settings.host,
        port=settings.port,
        workers=1,  # MUST be 1 -- see config.py note
        loop="uvloop",
        http="httptools",
        log_level=settings.log_level,
        access_log=False,  # disable for benchmark (reduces I/O overhead)
    )
