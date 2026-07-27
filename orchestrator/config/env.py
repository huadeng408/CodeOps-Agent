from __future__ import annotations

import os
from pathlib import Path


def load_dotenv(path: str | Path = ".env.local") -> None:
    env_path = Path(path)
    if not env_path.exists():
        return

    for raw_line in env_path.read_text(encoding="utf-8-sig").splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        key = key.strip()
        value = value.strip().strip('"').strip("'")
        if key and key not in os.environ:
            os.environ[key] = value


def read_env(name: str, default: str = "") -> str:
    value = os.environ.get(name, default)
    return value.strip() if isinstance(value, str) else default


def is_thinking_enabled() -> bool:
    """Return whether Extended Thinking / 深度思考模式 is enabled.

    Controlled by the THINKING_ENABLED environment variable (default: true).
    """
    return read_env("THINKING_ENABLED", "true").lower() == "true"


MODEL_FAST_DEFAULT: str = "gpt-4o-mini"

_otel_initialised = False
_otel_shutdown = lambda: None


def get_model_fast() -> str:
    """Return the fast/cheap model name for simple tasks.

    Controlled by the MODEL_FAST environment variable, which the Go harness
    sets from its config.ModelFast before launching the orchestrator subprocess.
    Defaults to "gpt-4o-mini".
    Returns empty string when explicitly set to empty (disabled).
    """
    return read_env("MODEL_FAST", MODEL_FAST_DEFAULT)


def configure_otel():
    """Initialise the OTel SDK with an OTLP HTTP exporter, returning a shutdown callable.

    Reads OTEL_EXPORTER_OTLP_ENDPOINT (default:
    ``http://localhost:6006/v1/traces`` -- the Arize Phoenix OTLP endpoint)
    and OTEL_SERVICE_NAME (default: ``"code-agent-orchestrator"``).

    If any part of initialisation fails (missing packages, unreachable
    endpoint, ...) the function returns a no-op shutdown and logs a warning.
    The orchestrator MUST NOT crash when telemetry is unavailable.

    Only the first call performs actual initialisation; subsequent calls
    return a no-op to avoid the OTel SDK's "Overriding of current
    TracerProvider is not allowed" warning.
    """
    global _otel_initialised, _otel_shutdown
    if _otel_initialised:
        return _otel_shutdown

    import logging

    _logger = logging.getLogger(__name__)

    endpoint = read_env("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:6006/v1/traces")
    service_name = read_env("OTEL_SERVICE_NAME", "code-agent-orchestrator")

    try:
        from opentelemetry import trace
        from opentelemetry.sdk.resources import Resource
        from opentelemetry.sdk.trace import TracerProvider
        from opentelemetry.sdk.trace.export import BatchSpanProcessor
        from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
    except ImportError as exc:
        _logger.warning("OTel SDK not available -- telemetry disabled: %s", exc)
        _otel_initialised = True
        _otel_shutdown = lambda: None
        return _otel_shutdown

    try:
        resource = Resource.create({"service.name": service_name})
        exporter = OTLPSpanExporter(endpoint=endpoint)
        processor = BatchSpanProcessor(exporter)
        provider = TracerProvider(resource=resource)
        provider.add_span_processor(processor)
        trace.set_tracer_provider(provider)
    except Exception as exc:
        _logger.warning("OTel initialisation failed (endpoint=%s): %s", endpoint, exc)
        _otel_initialised = True
        _otel_shutdown = lambda: None
        return _otel_shutdown

    def _shutdown() -> None:
        try:
            provider.shutdown()
        except Exception:
            pass

    _otel_initialised = True
    _otel_shutdown = _shutdown
    return _otel_shutdown
