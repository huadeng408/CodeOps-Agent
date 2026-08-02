from __future__ import annotations

import contextvars
import logging
import time
import uuid


_trace_id_var: contextvars.ContextVar[str] = contextvars.ContextVar("paismart_trace_id", default="")
logger = logging.getLogger("paismart.orchestrator")

# RAG span attribute names — MUST match the Go side
# (internal/telemetry/genai/schema_test.go). Phoenix join tests rely on
# these exact keys; renaming one side breaks cross-language trace queries.
RAG_ATTRIBUTE_NAMES = [
    "rag.corpus_generation",
    "rag.index_alias",
    "rag.index_physical",
    "rag.mapping_version",
    "rag.query_hash",
    "rag.top_n",
    "rag.retrieval_mode",
    "rag.reranker_applied",
    "rag.visual_path",
    "rag.document_hash",
    "rag.document_length",
]


class TraceSpan:
    """Builder for one RAG/agent span with the standard attribute set.

    Privacy rule (design spec §6.2): only hash/length/summary go into span
    attributes — never the raw query or document content.
    """

    def __init__(self, name: str, operation: str, provider: str) -> None:
        self._name = name
        self._attributes: dict[str, object] = {
            "gen_ai.operation.name": operation,
            "gen_ai.provider.name": provider,
        }

    def set_query_hash(self, query_hash: str) -> "TraceSpan":
        self._attributes["rag.query_hash"] = query_hash
        return self

    def set_top_n(self, top_n: int) -> "TraceSpan":
        self._attributes["rag.top_n"] = top_n
        return self

    def set_corpus(self, generation: str) -> "TraceSpan":
        self._attributes["rag.corpus_generation"] = generation
        return self

    def set_index_alias(self, alias: str) -> "TraceSpan":
        self._attributes["rag.index_alias"] = alias
        return self

    def set_index_physical(self, index: str) -> "TraceSpan":
        self._attributes["rag.index_physical"] = index
        return self

    def set_mapping_version(self, version: str) -> "TraceSpan":
        self._attributes["rag.mapping_version"] = version
        return self

    def set_retrieval_mode(self, mode: str) -> "TraceSpan":
        self._attributes["rag.retrieval_mode"] = mode
        return self

    def set_reranker_applied(self, applied: bool) -> "TraceSpan":
        self._attributes["rag.reranker_applied"] = applied
        return self

    def set_visual_path(self, path: str) -> "TraceSpan":
        self._attributes["rag.visual_path"] = path
        return self

    def set_document_hash(self, document_hash: str) -> "TraceSpan":
        self._attributes["rag.document_hash"] = document_hash
        return self

    def set_document_length(self, length: int) -> "TraceSpan":
        self._attributes["rag.document_length"] = length
        return self

    def attributes(self) -> dict[str, object]:
        return dict(self._attributes)


def configure_logging() -> None:
    if logger.handlers:
        return
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s %(message)s")


def ensure_trace_id(candidate: str = "") -> str:
    current = _trace_id_var.get().strip()
    if current:
        return current
    resolved = candidate.strip() or uuid.uuid4().hex[:16]
    _trace_id_var.set(resolved)
    return resolved


def set_trace_id(trace_id: str):
    resolved = trace_id.strip() or uuid.uuid4().hex[:16]
    return _trace_id_var.set(resolved)


def reset_trace_id(token: contextvars.Token[str]) -> None:
    _trace_id_var.reset(token)


def current_trace_id() -> str:
    return _trace_id_var.get().strip()


def log_request(message: str, **kwargs) -> None:
    payload = {"trace_id": current_trace_id()}
    payload.update(kwargs)
    logger.info("%s | %s", message, payload)


def elapsed_ms(start: float) -> int:
    return int((time.perf_counter() - start) * 1000)
