"""Abstract model interface that every registered model must implement."""

from abc import ABC, abstractmethod
from typing import Any

import torch


class BaseModel(ABC):
    """Contract: every model registered with ModelRegistry must subclass this.

    Input contract:
        run_batch receives a STACKED tensor of shape [B, *input_dims]
        where B = batch size.

    Output contract:
        run_batch returns a tensor of shape [B, *output_dims].
        The i-th output corresponds to the i-th input.

    Preprocessing/postprocessing of raw payload dict -> tensor is the
    responsibility of the model subclass, NOT the batcher or registry.
    """

    @abstractmethod
    def preprocess(self, payload: dict[str, Any]) -> torch.Tensor:
        """Convert a raw request payload dict into a 1D or ND tensor.

        Must be deterministic and side-effect free.
        Output shape: [*input_dims] (no batch dimension -- batcher adds that).
        """
        ...

    @abstractmethod
    def run_batch(self, batch: torch.Tensor) -> torch.Tensor:
        """Synchronous forward pass. Called inside run_in_executor.

        Input shape:  [B, *input_dims]
        Output shape: [B, *output_dims]
        Must call torch.no_grad() internally or caller guarantees it.
        """
        ...

    @abstractmethod
    def postprocess(self, output: torch.Tensor) -> Any:
        """Convert a single output tensor (one row from batch output) into
        a JSON-serializable Python object.

        Input shape: [*output_dims] (single item, no batch dim).
        """
        ...

    @property
    @abstractmethod
    def input_dim(self) -> tuple[int, ...]:
        """Shape of a single input tensor (no batch dim)."""
        ...

    @property
    @abstractmethod
    def model_type(self) -> str:
        """Logical group this model belongs to (matches InferenceRequest.model_type)."""
        ...
