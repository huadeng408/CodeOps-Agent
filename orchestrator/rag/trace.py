from __future__ import annotations

import contextvars
import logging
import time
import uuid


_trace_id_var: contextvars.ContextVar[str] = contextvars.ContextVar("paismart_trace_id", default="")
logger = logging.getLogger("paismart.orchestrator")


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
