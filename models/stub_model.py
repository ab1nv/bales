"""Stub model: a real working model that requires NO weights file.

Used for all testing and benchmarking. Implements BaseModel exactly.
A 3-layer MLP, input dim=128, output dim=10.
"""

from typing import Any

import torch
import torch.nn as nn

from models.base import BaseModel


class StubMLP(nn.Module):
    """Tiny MLP: 128 -> 256 -> 128 -> 10. Synthetic weights, no file needed."""

    def __init__(self):
        super().__init__()
        self.net = nn.Sequential(
            nn.Linear(128, 256),
            nn.ReLU(),
            nn.Linear(256, 128),
            nn.ReLU(),
            nn.Linear(128, 10),
        )

    def forward(self, x: torch.Tensor) -> torch.Tensor:
        return self.net(x)


class StubModel(BaseModel):
    """Wraps StubMLP. Preprocess converts a list of 128 floats into a tensor.
    Postprocess converts 10-class logits to argmax label + confidence.
    """

    def __init__(self):
        self._net = StubMLP()
        self._net.eval()

    def preprocess(self, payload: dict[str, Any]) -> torch.Tensor:
        """Expects payload = {"input": [float, float, ..., float]}  (length 128)

        Raises ValueError if wrong length.
        Returns: torch.Tensor of shape [128]
        """
        raw = payload.get("input")
        if raw is None:
            raise ValueError("payload must contain key 'input'")
        if len(raw) != 128:
            raise ValueError(f"Expected input length 128, got {len(raw)}")
        return torch.tensor(raw, dtype=torch.float32)

    def run_batch(self, batch: torch.Tensor) -> torch.Tensor:
        """Input:  [B, 128]
        Output: [B, 10]  (raw logits -- postprocess handles interpretation)
        """
        with torch.no_grad():
            return self._net(batch)

    def postprocess(self, output: torch.Tensor) -> dict[str, Any]:
        """Input: [10]  (single output row)
        Returns: {"label": int, "confidence": float, "logits": list[float]}
        """
        probs = torch.softmax(output, dim=0)
        label = int(probs.argmax().item())
        confidence = float(probs[label].item())
        return {
            "label": label,
            "confidence": round(confidence, 6),
            "logits": [round(x, 6) for x in output.tolist()],
        }

    @property
    def input_dim(self) -> tuple[int, ...]:
        return (128,)

    @property
    def model_type(self) -> str:
        return "classification"
