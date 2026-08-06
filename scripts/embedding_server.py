"""Serve a lightweight OpenAI-compatible embedding API for local development.

Two backends:
- fastembed (default) for models it supports (bge-small/large, jina, etc.)
- FlagEmbedding BGEM3FlagModel for BAAI/bge-m3 (1024-dim native, not
  supported by fastembed). Selected automatically when the model name
  contains "bge-m3"; the model is downloaded from the configured source
  (ModelScope via MODELSCOPE_CACHE, or HuggingFace via HF_ENDPOINT).
"""

import os
import time
import logging
import threading
from typing import List, Optional

from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, Field


class EmbeddingRequest(BaseModel):
    model: str | None = None
    input: List[str] | str = Field(default_factory=list)
    dimensions: int | None = None


class EmbeddingItem(BaseModel):
    object: str = "embedding"
    index: int
    embedding: List[float]


class EmbeddingUsage(BaseModel):
    prompt_tokens: int = 0
    total_tokens: int = 0


class EmbeddingResponse(BaseModel):
    object: str = "list"
    model: str
    data: List[EmbeddingItem]
    usage: EmbeddingUsage
    latency_ms: float


DEFAULT_MODEL = os.getenv("EMBEDDING_MODEL", "jinaai/jina-embeddings-v2-base-zh")
DEFAULT_REVISION = os.getenv("EMBEDDING_REVISION", "")
DEFAULT_THREADS = int(os.getenv("EMBEDDING_THREADS", "0"))
PRELOAD_MODEL = os.getenv("EMBEDDING_PRELOAD", "true").lower() not in {"0", "false", "no"}
SERIALIZE_REQUESTS = os.getenv("EMBEDDING_SERIALIZE_REQUESTS", "true").lower() not in {"0", "false", "no"}
WARMUP_TEXT = os.getenv("EMBEDDING_WARMUP_TEXT", "warmup")
OUTPUT_DIMENSIONS = int(os.getenv("EMBEDDING_OUTPUT_DIMENSIONS", "0"))

# Map logical model names to local model directories so callers can keep
# using the canonical name ("BAAI/bge-m3") while the service loads the
# already-downloaded local weights (never re-downloads from the hub).
LOCAL_MODEL_DIR = os.getenv("EMBEDDING_LOCAL_DIR", "")
MODEL_ALIASES = {"BAAI/bge-m3": LOCAL_MODEL_DIR} if LOCAL_MODEL_DIR else {}

app = FastAPI(title="PaiSmart Embedding", version="1.0.0")
_model_name = DEFAULT_MODEL
_model = None  # TextEmbedding | BGEM3FlagModel
_model_lock = threading.RLock()
_ready = False
_last_error: str | None = None

logging.basicConfig(level=os.getenv("EMBEDDING_LOG_LEVEL", "INFO").upper())
logger = logging.getLogger("paismart.embedding")


def _is_bge_m3(model_name: str) -> bool:
    return "bge-m3" in model_name.lower()


def _load_bge_m3(model_name: str):
    """Load BAAI/bge-m3 via sentence-transformers (native 1024-dim dense)."""
    from sentence_transformers import SentenceTransformer

    device = "cuda" if os.getenv("EMBEDDING_DEVICE", "cuda") == "cuda" else "cpu"
    if device == "cuda":
        try:
            import torch

            if not torch.cuda.is_available():
                device = "cpu"
        except Exception:
            device = "cpu"
    return SentenceTransformer(model_name, device=device)


def _load_fastembed(model_name: str):
    from fastembed import TextEmbedding

    init_kwargs = {}
    if DEFAULT_THREADS > 0:
        init_kwargs["threads"] = DEFAULT_THREADS
    return TextEmbedding(model_name=model_name, **init_kwargs)


def _resolve_model_name(model_name: str) -> str:
    """Resolve a logical model name to a local directory when aliased."""
    return MODEL_ALIASES.get(model_name, model_name)


def load_model(model_name: str):
    """Load the requested embedding model once and reuse it across requests."""
    global _model
    global _model_name
    global _ready
    global _last_error

    resolved = _resolve_model_name(model_name)
    with _model_lock:
        if _model is not None and _model_name == resolved:
            return _model

        started = time.perf_counter()
        if _is_bge_m3(resolved):
            _model = _load_bge_m3(resolved)
        else:
            _model = _load_fastembed(resolved)
        _model_name = resolved
        _ready = True
        _last_error = None
        logger.info("loaded embedding model=%s in %.2fs", resolved, time.perf_counter() - started)
        return _model


def _embed(model, texts: List[str]) -> List[List[float]]:
    """Run the model-specific embedding call."""
    if _is_bge_m3(_model_name):
        vecs = model.encode(texts, normalize_embeddings=False)
        return [list(v) for v in vecs]
    return list(model.embed(texts))


def run_embedding(model_name: str, texts: List[str]):
    """Run embedding generation and update service health when inference fails."""
    global _ready
    global _last_error

    try:
        if SERIALIZE_REQUESTS:
            with _model_lock:
                model = load_model(model_name)
                return _embed(model, texts)
        model = load_model(model_name)
        return _embed(model, texts)
    except Exception as exc:
        _ready = False
        _last_error = str(exc)
        logger.exception("embedding inference failed for model=%s", model_name)
        raise


def resize_vector(vector: List[float], target_dim: int | None) -> List[float]:
    """Resize vectors for local development so downstream ES dimensions stay stable."""
    if not target_dim or target_dim <= 0:
        return [float(v) for v in vector]

    values = [float(v) for v in vector]
    if len(values) == target_dim:
        return values
    if len(values) > target_dim:
        return values[:target_dim]
    if not values:
        return [0.0] * target_dim

    resized = list(values)
    idx = 0
    while len(resized) < target_dim:
        resized.append(values[idx % len(values)])
        idx += 1
    return resized


@app.on_event("startup")
def preload_model():
    """Optionally warm the default model to reduce first-request latency."""
    global _ready
    global _last_error

    if not PRELOAD_MODEL:
        return

    try:
        model = load_model(DEFAULT_MODEL)
        # Warm up the model once to avoid first-request timeout during host integration.
        _embed(model, [WARMUP_TEXT])
        _ready = True
        _last_error = None
        logger.info("embedding model warmup completed for model=%s", DEFAULT_MODEL)
    except Exception as exc:
        _ready = False
        _last_error = str(exc)
        logger.exception("embedding model preload failed for model=%s", DEFAULT_MODEL)


@app.get("/health")
def health():
    """Return the current readiness state of the embedding service.

    ``model`` reports the canonical logical name (EMBEDDING_MODEL) so callers
    comparing it against their configured model see a match even when the
    service loads weights from a local directory alias.
    """
    return {
        "status": "ok" if _ready else "degraded",
        "model": DEFAULT_MODEL,
        "model_revision": DEFAULT_REVISION,
        "dimensions": OUTPUT_DIMENSIONS or None,
        "ready": _ready,
        "last_error": _last_error,
    }


@app.post("/embeddings", response_model=EmbeddingResponse)
def embeddings(req: EmbeddingRequest):
    """Generate embeddings for one or more input texts."""
    model_name = req.model or DEFAULT_MODEL

    texts = req.input
    if isinstance(texts, str):
        texts = [texts]
    texts = [text for text in texts if text and text.strip()]
    if not texts:
        raise HTTPException(status_code=400, detail="input is required")

    started = time.perf_counter()
    try:
        vectors = run_embedding(model_name, texts)
    except Exception as exc:
        raise HTTPException(status_code=500, detail=f"embedding inference failed: {exc}") from exc
    latency_ms = (time.perf_counter() - started) * 1000

    target_dim = req.dimensions or OUTPUT_DIMENSIONS or None
    resized_vectors = [resize_vector(vector, target_dim) for vector in vectors]

    data = [
        EmbeddingItem(index=idx, embedding=vector)
        for idx, vector in enumerate(resized_vectors)
    ]
    token_estimate = sum(max(1, len(text) // 4) for text in texts)
    return EmbeddingResponse(
        model=model_name,
        data=data,
        usage=EmbeddingUsage(prompt_tokens=token_estimate, total_tokens=token_estimate),
        latency_ms=latency_ms,
    )


if __name__ == "__main__":
    import uvicorn

    uvicorn.run(app, host="127.0.0.1", port=int(os.getenv("EMBEDDING_PORT", "8009")))
