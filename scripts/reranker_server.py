"""Serve a lightweight reranker API for local retrieval experiments."""

import logging
import os
import threading
import time
from typing import List

from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, Field

from fastembed.rerank.cross_encoder import TextCrossEncoder


class RerankRequest(BaseModel):
    model: str | None = None
    query: str
    documents: List[str] = Field(default_factory=list)
    top_n: int = 5


class RerankItem(BaseModel):
    index: int
    relevance_score: float


class RerankResponse(BaseModel):
    model: str
    latency_ms: float
    results: List[RerankItem]


DEFAULT_MODEL = os.getenv("RERANKER_MODEL", "BAAI/bge-reranker-base")
DEFAULT_THREADS = int(os.getenv("RERANKER_THREADS", "0"))
PRELOAD_MODEL = os.getenv("RERANKER_PRELOAD", "true").lower() not in {"0", "false", "no"}
SERIALIZE_REQUESTS = os.getenv("RERANKER_SERIALIZE_REQUESTS", "false").lower() not in {"0", "false", "no"}
WARMUP_QUERY = os.getenv("RERANKER_WARMUP_QUERY", "warmup")
WARMUP_DOCS = [item for item in os.getenv("RERANKER_WARMUP_DOCS", "warmup document||fallback document").split("||") if item.strip()]

app = FastAPI(title="PaiSmart Reranker", version="1.0.0")
_model_name = DEFAULT_MODEL
_model: TextCrossEncoder | None = None
_model_lock = threading.RLock()
_ready = False
_last_error: str | None = None

logging.basicConfig(level=os.getenv("RERANKER_LOG_LEVEL", "INFO").upper())
logger = logging.getLogger("paismart.reranker")


def load_model(model_name: str) -> TextCrossEncoder:
    """Load the reranker model once and reuse it across requests."""
    global _model
    global _model_name
    global _ready
    global _last_error

    with _model_lock:
        if _model is not None and _model_name == model_name:
            return _model

        init_kwargs = {}
        if DEFAULT_THREADS > 0:
            init_kwargs["threads"] = DEFAULT_THREADS

        started = time.perf_counter()
        _model = TextCrossEncoder(model_name=model_name, **init_kwargs)
        _model_name = model_name
        _ready = True
        _last_error = None
        logger.info("loaded reranker model=%s in %.2fs", model_name, time.perf_counter() - started)
        return _model


def run_rerank(model_name: str, query: str, documents: List[str]) -> list[float]:
    """Run reranking and mark the service unhealthy if inference fails."""
    global _ready
    global _last_error

    try:
        if SERIALIZE_REQUESTS:
            with _model_lock:
                model = load_model(model_name)
                return list(model.rerank(query, documents))
        model = load_model(model_name)
        return list(model.rerank(query, documents))
    except Exception as exc:
        _ready = False
        _last_error = str(exc)
        logger.exception("rerank inference failed for model=%s", model_name)
        raise


@app.on_event("startup")
def preload_model():
    """Warm the default model so the first user request is fast and stable."""
    global _ready
    global _last_error

    if not PRELOAD_MODEL:
        return

    try:
        model = load_model(DEFAULT_MODEL)
        list(model.rerank(WARMUP_QUERY, WARMUP_DOCS))
        _ready = True
        _last_error = None
        logger.info("reranker model warmup completed for model=%s", DEFAULT_MODEL)
    except Exception as exc:
        _ready = False
        _last_error = str(exc)
        logger.exception("reranker model preload failed for model=%s", DEFAULT_MODEL)


@app.get("/health")
def health():
    """Return the current reranker service health."""
    return {
        "status": "ok" if _ready else "degraded",
        "model": _model_name or DEFAULT_MODEL,
        "ready": _ready,
        "last_error": _last_error,
    }


@app.post("/rerank", response_model=RerankResponse)
def rerank(req: RerankRequest):
    """Score and sort candidate documents for the supplied query."""
    if not req.query.strip():
        raise HTTPException(status_code=400, detail="query is required")
    if not req.documents:
        return RerankResponse(model=req.model or DEFAULT_MODEL, latency_ms=0.0, results=[])

    model_name = req.model or DEFAULT_MODEL
    started = time.perf_counter()
    scores = run_rerank(model_name, req.query, req.documents)
    latency_ms = (time.perf_counter() - started) * 1000

    ranked = sorted(
        (
            RerankItem(index=idx, relevance_score=float(score))
            for idx, score in enumerate(scores)
        ),
        key=lambda item: item.relevance_score,
        reverse=True,
    )

    top_n = req.top_n if req.top_n > 0 else len(ranked)
    return RerankResponse(model=model_name, latency_ms=latency_ms, results=ranked[:top_n])


if __name__ == "__main__":
    import uvicorn

    uvicorn.run(app, host="127.0.0.1", port=int(os.getenv("RERANKER_PORT", "8008")))
