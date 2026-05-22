"""Single source of truth for all configuration."""

from functools import lru_cache

from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    """Application settings loaded from environment variables."""

    # Redis
    redis_url: str = "redis://localhost:6379/0"
    redis_queue_db: int = 0
    redis_dedup_db: int = 1

    # Batching
    max_batch_size: int = 32
    batch_window_ms: float = 5.0  # collection window in milliseconds
    batcher_timeout_s: float = 5.0  # how long a client waits before 504

    # Model
    default_model_id: str = "stub_v1"
    model_weights_dir: str = "./weights"
    thread_pool_size: int = 4  # executor threads for torch inference

    # Server
    host: str = "0.0.0.0"
    port: int = 8000
    workers: int = 1  # uvicorn workers (keep 1 for shared state)
    log_level: str = "info"

    # Metrics
    enable_prometheus: bool = True
    prometheus_port: int = 9090

    model_config = SettingsConfigDict(
        env_file=".env",
        env_file_encoding="utf-8",
    )


@lru_cache
def get_settings() -> Settings:
    """Return cached Settings instance."""
    return Settings()
