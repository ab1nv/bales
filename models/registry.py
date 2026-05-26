"""Model registry with atomic hot-swap and zero dropped requests."""

import asyncio
import logging
from typing import TYPE_CHECKING

from models.base import BaseModel

if TYPE_CHECKING:
    pass  # avoid circular imports

logger = logging.getLogger(__name__)


class ModelRegistry:
    """Thread-safety model:
        - _models dict reads: GIL-protected, no lock needed
        - _models dict writes (hot-swap): guarded by _swap_lock
        - In-flight inference during swap: safe because we swap the dict VALUE
          (the reference), not the model object itself. Ongoing inference
          holds a local reference to the old model object -- it completes safely.

    Hot-swap sequence (CRITICAL -- do not change order):
        1. Load new model into memory (outside lock -- may take seconds)
        2. Acquire _swap_lock (blocks new swap requests, not inference)
        3. Replace dict entry atomically
        4. Release lock
        5. Delete old model reference (GC cleans up)

    This means there is a brief window where old and new model both exist in
    memory. This is intentional and necessary for zero-downtime.
    """

    def __init__(self):
        self._models: dict[str, BaseModel] = {}
        self._swap_lock = asyncio.Lock()

    def get(self, model_id: str) -> BaseModel:
        """Synchronous lookup. Returns model or raises KeyError.

        GIL protects dict read -- no async needed.
        """
        model = self._models.get(model_id)
        if model is None:
            raise KeyError(f"Model '{model_id}' is not registered")
        return model

    async def register(self, model_id: str, model: BaseModel) -> None:
        """Register a new model. Idempotent -- re-registering overwrites.

        Use hot_swap for production live replacement instead.
        """
        if not isinstance(model, BaseModel):
            raise TypeError(f"model must be a BaseModel subclass, got {type(model)}")
        async with self._swap_lock:
            self._models[model_id] = model
        logger.info(
            f"[registry] registered model_id='{model_id}' type={type(model).__name__}"
        )

    async def hot_swap(self, model_id: str, new_model: BaseModel) -> None:
        """Replace a live model atomically. The new model must already be
        loaded and in .eval() mode before calling this.

        Callers (routes.py) are responsible for loading the model object --
        this method only does the atomic swap.
        """
        if model_id not in self._models:
            raise KeyError(
                f"Cannot hot-swap '{model_id}': not currently registered. Use register() first."
            )
        async with self._swap_lock:
            old = self._models[model_id]
            self._models[model_id] = new_model
        logger.info(f"[registry] hot-swapped model_id='{model_id}'")
        # old reference goes out of scope here; GC handles memory
        del old

    def list_models(self) -> list[str]:
        """Return list of currently registered model IDs."""
        return list(self._models.keys())

    def __contains__(self, model_id: str) -> bool:
        return model_id in self._models


# Module-level singleton -- imported by routes.py, consumer.py, main.py
model_registry = ModelRegistry()
