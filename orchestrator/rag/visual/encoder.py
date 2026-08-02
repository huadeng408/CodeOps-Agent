"""Visual encoder abstraction (plan Task 5.1).

The encoder is intentionally model-agnostic: unit tests never download a
model. A concrete encoder is only constructed when a GPU is available and a
model revision is pinned; otherwise the status is explicitly "disabled" and
encode calls raise.
"""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class EncoderStatus:
    state: str  # "ready" | "disabled"
    reason: str = ""


def encoder_status(device: str | None, model: str | None) -> tuple[str, str]:
    """Return (state, reason) for the visual encoder.

    ready requires a device and a pinned model revision; anything else is
    disabled with an explicit reason so callers can degrade loudly.
    """
    if not device:
        return "disabled", "no GPU/accelerator device available"
    if not model:
        return "disabled", "no pinned visual model revision"
    return "ready", ""


class VisualEncoder:
    """Encodes page/crop images into visual embeddings.

    Construction does not load a model; encode_page requires an enabled
    encoder and raises RuntimeError otherwise (fail loud, never fake).
    """

    def __init__(self, device: str | None, model: str | None) -> None:
        self._status, self._reason = encoder_status(device, model)

    @property
    def status(self) -> str:
        return self._status

    @property
    def reason(self) -> str:
        return self._reason

    def encode_page(self, image_bytes: bytes) -> list[float]:
        if self._status != "ready":
            raise RuntimeError(f"visual encoder disabled: {self._reason}")
        raise NotImplementedError("concrete encoder (ColPali/ColQwen) must implement encode_page")

    def encode_crop(self, image_bytes: bytes, bbox_scaled: list[float]) -> list[float]:
        if self._status != "ready":
            raise RuntimeError(f"visual encoder disabled: {self._reason}")
        raise NotImplementedError("concrete encoder (ColPali/ColQwen) must implement encode_crop")
