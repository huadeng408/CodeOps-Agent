"""Visual encoder abstraction (plan Task 5.1).

The encoder is intentionally model-agnostic: unit tests never download a
model. A concrete encoder is only constructed when a GPU is available and a
model revision is pinned; otherwise the status is explicitly "disabled" and
encode calls raise.
"""

from __future__ import annotations

import io
import re
from dataclasses import dataclass
from typing import Any


_IMMUTABLE_REVISION = re.compile(r"^[0-9a-f]{40}$")


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


class CLIPVisualEncoder:
    """Pinned CLIP page encoder for the visual pilot baseline.

    The model is deliberately loaded lazily so configuration validation and
    malformed-image failures never trigger a download. The caller must provide
    a full commit hash: mutable branches and tags are not valid evaluation
    dependencies.
    """

    def __init__(self, model_id: str, revision: str, device: str = "cuda") -> None:
        if not model_id.strip():
            raise ValueError("visual model_id is required")
        if not _IMMUTABLE_REVISION.fullmatch(revision):
            raise ValueError("visual model revision must be an immutable 40-character commit")
        if device not in {"cpu", "cuda"}:
            raise ValueError(f"unsupported visual encoder device: {device}")
        self.model_id = model_id
        self.revision = revision
        self.device = device
        self._model: Any | None = None
        self._processor: Any | None = None

    @property
    def metadata(self) -> dict[str, Any]:
        return {
            "model": self.model_id,
            "model_revision": self.revision,
            "device": self.device,
            "embedding_dimensions": 512,
            "normalization": "l2",
            "encoder": "clip-image-feature",
        }

    def _load(self) -> None:
        if self._model is not None and self._processor is not None:
            return
        try:
            import torch
            from transformers import CLIPModel, CLIPProcessor
        except ImportError as exc:  # pragma: no cover - environment dependent
            raise RuntimeError("CLIP visual encoder requires torch and transformers") from exc
        if self.device == "cuda" and not torch.cuda.is_available():
            raise RuntimeError("CLIP visual encoder requested cuda but CUDA is unavailable")
        self._processor = CLIPProcessor.from_pretrained(self.model_id, revision=self.revision)
        self._model = CLIPModel.from_pretrained(self.model_id, revision=self.revision)
        self._model.eval().to(self.device)

    @staticmethod
    def _decode(image_bytes: bytes) -> Any:
        try:
            from PIL import Image

            image = Image.open(io.BytesIO(image_bytes))
            image.load()
            return image.convert("RGB")
        except Exception as exc:
            raise ValueError("visual encoder input must be a valid image payload") from exc

    def encode_page(self, image_bytes: bytes) -> list[float]:
        return self.encode_pages([image_bytes])[0]

    def encode_pages(self, image_payloads: list[bytes]) -> list[list[float]]:
        if not image_payloads:
            raise ValueError("visual image batch must not be empty")
        images = [self._decode(payload) for payload in image_payloads]
        self._load()
        import torch

        assert self._model is not None
        assert self._processor is not None
        inputs = self._processor(images=images, return_tensors="pt")
        inputs = {name: value.to(self.device) for name, value in inputs.items()}
        with torch.inference_mode():
            features = self._model.get_image_features(**inputs)
            features = torch.nn.functional.normalize(features, p=2, dim=-1)
        vectors = features.detach().cpu().tolist()
        if any(len(vector) != self.metadata["embedding_dimensions"] for vector in vectors):
            raise RuntimeError(f"CLIP returned an unexpected embedding dimension, expected {self.metadata['embedding_dimensions']}")
        return [[float(value) for value in vector] for vector in vectors]

    def encode_query(self, query: str) -> list[float]:
        return self.encode_queries([query])[0]

    def encode_queries(self, queries: list[str]) -> list[list[float]]:
        if not queries or any(not query.strip() for query in queries):
            raise ValueError("visual query must not be empty")
        self._load()
        import torch

        assert self._model is not None
        assert self._processor is not None
        inputs = self._processor(text=queries, return_tensors="pt", padding=True, truncation=True)
        inputs = {name: value.to(self.device) for name, value in inputs.items()}
        with torch.inference_mode():
            features = self._model.get_text_features(**inputs)
            features = torch.nn.functional.normalize(features, p=2, dim=-1)
        vectors = features.detach().cpu().tolist()
        if any(len(vector) != self.metadata["embedding_dimensions"] for vector in vectors):
            raise RuntimeError(f"CLIP returned an unexpected embedding dimension, expected {self.metadata['embedding_dimensions']}")
        return [[float(value) for value in vector] for vector in vectors]

    def encode_crop(self, image_bytes: bytes, bbox_scaled: list[float]) -> list[float]:
        if len(bbox_scaled) != 4:
            raise ValueError("visual crop bbox must contain four coordinates")
        image = self._decode(image_bytes)
        x1, y1, x2, y2 = (int(round(value)) for value in bbox_scaled)
        if x2 <= x1 or y2 <= y1:
            raise ValueError("visual crop bbox must have positive area")
        if x1 < 0 or y1 < 0 or x2 > image.width or y2 > image.height:
            raise ValueError("visual crop bbox is outside the image")
        buffer = io.BytesIO()
        image.crop((x1, y1, x2, y2)).save(buffer, format="PNG")
        return self.encode_page(buffer.getvalue())
