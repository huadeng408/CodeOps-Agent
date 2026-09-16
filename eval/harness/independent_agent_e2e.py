"""Fail-closed receipt runner for the provider-backed independent-agent E2E.

The production process scenario remains owned by
``tests/e2e/independent_agents_process_e2e_test.go``.  This module invokes that
scenario, binds its receipt to provider response metadata from the same run,
and reads exported spans back from Phoenix.  It never upgrades the deterministic
fixture lane, a provider-silent run, or locally captured spans to release
evidence.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import subprocess
import time
from collections.abc import Callable, Mapping, Sequence
from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path
from typing import Any
from urllib.parse import urlsplit

from eval.harness.artifacts import RunArtifacts
from eval.harness.phoenix import read_run_spans
from eval.harness.source_pin import source_pin
from eval.harness.trace_contract import CapturedSpan

BLOCKED_EXIT_CODE = 3
ERROR_EXIT_CODE = 2
EMPTY_DIRTY_HASH = hashlib.sha256(b"").hexdigest()

REQUIRED_CHECKS = frozenset(
    {
        "agent_card",
        "message_text_file_json",
        "task_lifecycle",
        "isolated_child_context",
        "skill_lazy_loaded",
        "harness_authorized_tool",
        "mcp_call",
        "sandbox_enforced",
        "artifact_pinned",
        "memory_reflection_written",
        "restart_recall",
        "ledger_hash_chain",
    }
)

REQUIRED_SPANS = frozenset(
    {
        "eval.run",
        "agent.main",
        "agent.subagent",
        "skill.load",
        "tool.mcp",
        "artifact.publish",
        "memory.reflect",
        "memory.recall",
    }
)

_PROVIDER_CREDENTIAL_GROUPS: dict[str, tuple[tuple[str, ...], ...]] = {
    "openai": (("OPENAI_API_KEY",),),
    "anthropic": (("ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"),),
}
_IDENTITY_KEYS = (
    "endpoint_host",
    "requested_model",
    "reported_model",
    "response_id",
    "system_fingerprint",
    "created",
    "identity_verified",
)
_SENSITIVE_ENV_MARKERS = ("API_KEY", "AUTH_TOKEN", "PASSWORD", "SECRET", "TOKEN")
_SENSITIVE_RECORD_MARKERS = (
    "api_key",
    "authorization",
    "auth_token",
    "password",
    "secret",
)

_BASE_ENV_KEYS = frozenset(
    {
        "PATH",
        "PATHEXT",
        "SYSTEMROOT",
        "WINDIR",
        "COMSPEC",
        "TEMP",
        "TMP",
        "USERPROFILE",
        "HOME",
        "LOCALAPPDATA",
        "APPDATA",
        "GOROOT",
        "GOPATH",
        "GOCACHE",
        "GOMODCACHE",
        "VIRTUAL_ENV",
        "PYTHONHOME",
        "PYTHONPATH",
        "SSL_CERT_FILE",
        "SSL_CERT_DIR",
        "REQUESTS_CA_BUNDLE",
        "HTTP_PROXY",
        "HTTPS_PROXY",
        "NO_PROXY",
        "LANG",
        "LC_ALL",
    }
)
_PROVIDER_ENV_KEYS = {
    "openai": frozenset(
        {
            "OPENAI_API_KEY",
            "OPENAI_BASE_URL",
            "OPENAI_MODEL",
            "OPENAI_TIMEOUT",
            "OPENAI_MAX_RETRIES",
            "OPENAI_MAX_TOKENS",
            "OPENAI_REASONING_EFFORT",
        }
    ),
    "anthropic": frozenset(
        {
            "ANTHROPIC_API_KEY",
            "ANTHROPIC_AUTH_TOKEN",
            "ANTHROPIC_BASE_URL",
            "ANTHROPIC_MODEL",
            "ANTHROPIC_TIMEOUT",
            "ANTHROPIC_MAX_RETRIES",
            "ANTHROPIC_MAX_TOKENS",
            "ANTHROPIC_VERSION",
        }
    ),
}
_SANDBOX_BACKEND_KEY = "CODE_AGENT_E2E_SANDBOX_BACKEND"
_SANDBOX_IMAGE_KEY = "CODE_AGENT_E2E_SANDBOX_IMAGE"
_SANDBOX_WSL_DISTRO_KEY = "CODE_AGENT_E2E_WSL_DISTRO"
_EVAL_JOIN_ID = re.compile(r"[A-Za-z0-9][A-Za-z0-9._:-]{0,95}")

ProcessExecutor = Callable[
    [Sequence[str], Path, Mapping[str, str], float], subprocess.CompletedProcess[str]
]
PhoenixReader = Callable[[str, str, str, str, Sequence[str]], list[CapturedSpan]]
SourcePinReader = Callable[[str | Path], dict[str, Any]]


@dataclass(frozen=True, slots=True)
class IndependentAgentE2EConfig:
    run_id: str
    artifact_root: Path
    repo_root: Path
    provider: str = ""
    phoenix_url: str = ""
    phoenix_project: str = "default"
    timeout_seconds: float = 600.0
    trace_readback_attempts: int = 6
    trace_readback_interval_seconds: float = 1.0

    def validate(self) -> None:
        if (
            not self.run_id
            or Path(self.run_id).name != self.run_id
            or self.run_id in {".", ".."}
            or _EVAL_JOIN_ID.fullmatch(self.run_id) is None
        ):
            raise ValueError("run_id must be one safe trace and directory identifier")
        if self.timeout_seconds <= 0:
            raise ValueError("timeout_seconds must be positive")
        if not 1 <= self.trace_readback_attempts <= 60:
            raise ValueError("trace_readback_attempts must be between 1 and 60")
        if self.trace_readback_interval_seconds < 0:
            raise ValueError("trace_readback_interval_seconds must not be negative")
        if self.provider and self.provider not in _PROVIDER_CREDENTIAL_GROUPS:
            raise ValueError("provider must be openai or anthropic")
        if not self.phoenix_project.strip():
            raise ValueError("phoenix_project must not be empty")
        if self.phoenix_url:
            parsed = urlsplit(self.phoenix_url)
            if (
                parsed.scheme not in {"http", "https"}
                or not parsed.hostname
                or parsed.username
                or parsed.password
                or parsed.query
                or parsed.fragment
            ):
                raise ValueError(
                    "phoenix_url must be an HTTP(S) origin without credentials, query, or fragment"
                )


def _sha256_file(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def _sha256_text(value: str) -> str:
    return hashlib.sha256(value.encode("utf-8")).hexdigest()


def _failure(failures: list[dict[str, str]], category: str, message: str) -> None:
    candidate = {"category": category, "message": message}
    if candidate not in failures:
        failures.append(candidate)


def _approved_credentials(
    provider: str, environ: Mapping[str, str]
) -> tuple[bool, tuple[str, ...]]:
    groups = _PROVIDER_CREDENTIAL_GROUPS.get(provider, ())
    missing: list[str] = []
    for alternatives in groups:
        if not any(str(environ.get(name, "")).strip() for name in alternatives):
            missing.append(" or ".join(alternatives))
    return bool(groups) and not missing, tuple(missing)


def _sandbox_preflight(environ: Mapping[str, str]) -> tuple[str, tuple[str, ...]]:
    backend = str(environ.get(_SANDBOX_BACKEND_KEY, "")).strip().lower()
    problems: list[str] = []
    if backend not in {"docker", "wsl2"}:
        problems.append(f"{_SANDBOX_BACKEND_KEY} must explicitly select docker or wsl2")
    if not str(environ.get(_SANDBOX_IMAGE_KEY, "")).strip():
        problems.append(f"{_SANDBOX_IMAGE_KEY} is required")
    if backend == "wsl2" and not str(environ.get(_SANDBOX_WSL_DISTRO_KEY, "")).strip():
        problems.append(f"{_SANDBOX_WSL_DISTRO_KEY} is required for wsl2")
    return backend, tuple(problems)


def _subprocess_environment(
    provider: str,
    environ: Mapping[str, str],
    *,
    run_id: str,
    process_receipt: Path,
    route_receipt: Path,
    phoenix_url: str,
) -> dict[str, str]:
    sandbox_backend = str(environ.get(_SANDBOX_BACKEND_KEY, "")).strip().lower()
    sandbox_keys = {_SANDBOX_BACKEND_KEY, _SANDBOX_IMAGE_KEY}
    if sandbox_backend == "wsl2":
        sandbox_keys.add(_SANDBOX_WSL_DISTRO_KEY)
    allowed = (
        _BASE_ENV_KEYS
        | _PROVIDER_ENV_KEYS.get(provider, frozenset())
        | frozenset(sandbox_keys)
    )
    result = {
        key: str(value)
        for key, value in environ.items()
        if key.upper() in allowed and str(value)
    }
    result.update(
        {
            "CODE_AGENT_RUN_INDEPENDENT_AGENTS_E2E": "1",
            "CODE_AGENT_E2E_PROVIDER": provider,
            "CODE_AGENT_EVAL_RUN_ID": run_id,
            "CODE_AGENT_INDEPENDENT_AGENTS_E2E_RUN_ID": run_id,
            "CODE_AGENT_INDEPENDENT_AGENTS_E2E_RECEIPT": str(process_receipt),
            "CODE_AGENT_INDEPENDENT_AGENTS_E2E_PROVIDER_ROUTE_RECEIPT": str(
                route_receipt
            ),
            "PHOENIX_URL": phoenix_url.rstrip("/"),
            "OTEL_EXPORTER_OTLP_ENDPOINT": phoenix_url.rstrip("/") + "/v1/traces",
            "OTEL_SERVICE_NAME": "code-agent-independent-e2e",
            "LLM_PROVIDER": provider,
        }
    )
    return result


def _execute_process(
    command: Sequence[str],
    cwd: Path,
    environ: Mapping[str, str],
    timeout_seconds: float,
) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        list(command),
        cwd=cwd,
        env=dict(environ),
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        timeout=timeout_seconds,
        check=False,
    )


def _load_object(path: Path, label: str) -> dict[str, Any]:
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise ValueError(
            f"{label} is unavailable or malformed: {type(exc).__name__}"
        ) from exc
    if not isinstance(payload, dict):
        raise TypeError(f"{label} must be a JSON object")
    if _contains_sensitive_key(payload):
        raise ValueError(f"{label} contains a credential-bearing field")
    return payload


def _safe_identity(value: object) -> dict[str, Any]:
    source = value if isinstance(value, dict) else {}
    return {key: source[key] for key in _IDENTITY_KEYS if key in source}


def _contains_sensitive_key(value: object) -> bool:
    if isinstance(value, dict):
        for key, child in value.items():
            normalized = str(key).strip().lower()
            if any(marker in normalized for marker in _SENSITIVE_RECORD_MARKERS):
                return True
            if _contains_sensitive_key(child):
                return True
    elif isinstance(value, list):
        return any(_contains_sensitive_key(item) for item in value)
    return False


def _load_provider_events(path: Path) -> list[dict[str, Any]]:
    events: list[dict[str, Any]] = []
    try:
        lines = path.read_text(encoding="utf-8").splitlines()
    except OSError as exc:
        raise ValueError(
            f"provider route evidence is unavailable: {type(exc).__name__}"
        ) from exc
    for index, line in enumerate(lines, start=1):
        if not line.strip():
            continue
        try:
            payload = json.loads(line)
        except json.JSONDecodeError as exc:
            raise ValueError(f"provider route line {index} is malformed") from exc
        if not isinstance(payload, dict):
            raise TypeError(f"provider route line {index} must be an object")
        if _contains_sensitive_key(payload):
            raise ValueError(
                f"provider route line {index} contains a credential-bearing field"
            )
        if payload.get("phase") != "model_after":
            continue
        turn = payload.get("turn")
        if payload.get("schema_version") != 1:
            raise ValueError(f"provider route line {index} has unsupported schema")
        if not isinstance(turn, int) or isinstance(turn, bool) or turn < 1:
            raise ValueError(f"provider route line {index} has invalid turn")
        events.append(
            {
                "schema_version": 1,
                "phase": "model_after",
                "eval_run_id": str(payload.get("eval_run_id", "")),
                "session_id": str(payload.get("session_id", "")),
                "turn": turn,
                "provider_backed": payload.get("provider_backed") is True,
                "provider": str(payload.get("provider", "")),
                "model": str(payload.get("model", "")),
                "generation": payload.get("generation"),
                "model_identity": _safe_identity(payload.get("model_identity")),
            }
        )
    return events


def _identity_status(events: Sequence[dict[str, Any]]) -> str:
    if not events:
        return "MODEL_IDENTITY_UNVERIFIED"
    for event in events:
        identity = event.get("model_identity", {})
        if not isinstance(identity, dict):
            return "MODEL_IDENTITY_UNVERIFIED"
        if identity.get("identity_verified") is not True:
            return "MODEL_IDENTITY_UNVERIFIED"
        if not all(
            str(identity.get(key, "")).strip()
            for key in ("reported_model", "response_id", "system_fingerprint")
        ):
            return "MODEL_IDENTITY_UNVERIFIED"
    return "MODEL_IDENTITY_VERIFIED"


def _trace_summary(
    spans: Sequence[CapturedSpan],
    *,
    parent_session_id: str,
    child_session_id: str,
) -> dict[str, Any]:
    trace_ids = sorted({span.trace_id for span in spans if span.trace_id})
    observed = sorted({span.name for span in spans})
    span_ids = {span.span_id for span in spans if span.span_id}
    invalid_parentage = sorted(
        span.name
        for span in spans
        if span.parent_span_id and span.parent_span_id not in span_ids
    )
    disallowed_statuses = {"ERROR", "CANCELLED", "TIMEOUT", "SKIPPED"}
    instance_ids = sorted(
        {
            str(span.attributes.get("eval.instance_id", ""))
            for span in spans
            if str(span.attributes.get("eval.instance_id", ""))
        }
    )
    main_bound = any(
        span.name == "agent.main"
        and str(span.attributes.get("eval.instance_id", "")) == parent_session_id
        for span in spans
    )
    child_bound = any(
        span.name == "agent.subagent"
        and str(span.attributes.get("eval.instance_id", "")) == child_session_id
        for span in spans
    )
    required_instances_bound = all(
        str(span.attributes.get("eval.instance_id", ""))
        in {parent_session_id, child_session_id}
        for span in spans
        if span.name in REQUIRED_SPANS and span.name != "eval.run"
    )
    run_root_bound = any(
        span.name == "eval.run"
        and not span.parent_span_id
        and not str(span.attributes.get("eval.instance_id", ""))
        for span in spans
    )
    return {
        "backend_readback": bool(spans),
        "single_trace": len(trace_ids) == 1,
        "trace_ids": trace_ids,
        "observed_spans": observed,
        "span_count": len(spans),
        "all_ended": all(span.ended for span in spans),
        "error_free": all(
            str(span.status).upper() not in disallowed_statuses for span in spans
        ),
        "parentage_complete": not invalid_parentage,
        "invalid_parentage": invalid_parentage,
        "instance_ids": instance_ids,
        "main_session_bound": main_bound,
        "subagent_session_bound": child_bound,
        "required_instances_bound": required_instances_bound,
        "run_root_bound": run_root_bound,
    }


def _trace_ready(
    spans: Sequence[CapturedSpan], parent_session_id: str, child_session_id: str
) -> bool:
    summary = _trace_summary(
        spans,
        parent_session_id=parent_session_id,
        child_session_id=child_session_id,
    )
    return (
        summary["backend_readback"]
        and summary["single_trace"]
        and summary["all_ended"]
        and summary["error_free"]
        and summary["parentage_complete"]
        and summary["main_session_bound"]
        and summary["subagent_session_bound"]
        and summary["required_instances_bound"]
        and summary["run_root_bound"]
        and REQUIRED_SPANS.issubset(set(summary["observed_spans"]))
    )


def _process_checks(process_receipt: Mapping[str, Any]) -> dict[str, bool]:
    raw = process_receipt.get("release_checks", process_receipt.get("checks", {}))
    source = raw if isinstance(raw, Mapping) else {}
    return {name: source.get(name) is True for name in sorted(REQUIRED_CHECKS)}


def _phoenix_origin(value: str) -> dict[str, Any]:
    if not value:
        return {}
    parsed = urlsplit(value)
    return {
        "scheme": parsed.scheme,
        "host": parsed.hostname or "",
        "port": parsed.port,
    }


def _finalize(
    artifacts: RunArtifacts,
    receipt: dict[str, Any],
) -> dict[str, Any]:
    receipt["checksum_verification"] = []
    artifacts.write("receipt.json", receipt)
    artifacts.write_checksums()
    problems = artifacts.verify_checksums()
    if problems:
        receipt["status"] = "BLOCKED"
        receipt["exit_code"] = BLOCKED_EXIT_CODE
        _failure(
            receipt["failures"],
            "artifact_checksum",
            "; ".join(problems),
        )
        artifacts.write("receipt.json", receipt)
        artifacts.write_checksums()
        receipt["checksum_verification"] = artifacts.verify_checksums()
    return receipt


def run_independent_agent_e2e(
    config: IndependentAgentE2EConfig,
    *,
    environ: Mapping[str, str] | None = None,
    process_executor: ProcessExecutor = _execute_process,
    phoenix_reader: PhoenixReader = read_run_spans,
    source_pin_reader: SourcePinReader = source_pin,
    sleep_fn: Callable[[float], None] = time.sleep,
) -> dict[str, Any]:
    """Run and classify one provider-backed independent-agent acceptance.

    Missing credentials, an unsupported fixture receipt, incomplete provider
    identity, or failed trace readback all produce a finalized ``BLOCKED``
    receipt.  Credential values and provider output are never serialized.
    """

    config.validate()
    environment = dict(os.environ if environ is None else environ)
    run_source_pin = source_pin_reader(config.repo_root)
    run_root = config.artifact_root.resolve() / config.run_id
    if run_root.exists() and any(run_root.iterdir()):
        raise FileExistsError(f"refusing to overwrite run directory: {run_root}")
    artifacts = RunArtifacts(config.run_id, config.artifact_root)
    process_receipt_path = artifacts.root / "process-receipt.json"
    route_receipt_path = artifacts.root / "provider-route.jsonl"
    started_at = datetime.now(UTC).isoformat().replace("+00:00", "Z")
    failures: list[dict[str, str]] = []

    if (
        run_source_pin.get("dirty_hash") != EMPTY_DIRTY_HASH
        or run_source_pin.get("untracked_files") != 0
    ):
        _failure(
            failures,
            "source_pin",
            "provider-backed release evidence requires a clean tracked source tree",
        )
    if not config.provider:
        _failure(
            failures,
            "provider_config",
            "CODE_AGENT_E2E_PROVIDER or --provider is required",
        )
    credentials_ok, missing_credentials = _approved_credentials(
        config.provider, environment
    )
    if not credentials_ok:
        detail = ", ".join(missing_credentials) or "approved provider credentials"
        _failure(
            failures,
            "provider_credentials",
            f"missing process-environment credential: {detail}",
        )
    sandbox_backend, sandbox_problems = _sandbox_preflight(environment)
    if sandbox_problems:
        _failure(
            failures,
            "sandbox_config",
            "; ".join(sandbox_problems),
        )
    if not config.phoenix_url:
        _failure(
            failures,
            "trace_backend",
            "PHOENIX_URL or --phoenix-url is required for backend readback",
        )

    artifacts.write_manifest(
        {
            "schema_version": 1,
            "run_id": config.run_id,
            "source_pin": run_source_pin,
            "provider": config.provider,
            "sandbox": {
                "backend": sandbox_backend,
                "image_configured": bool(
                    str(environment.get(_SANDBOX_IMAGE_KEY, "")).strip()
                ),
                "wsl_distro_configured": bool(
                    str(environment.get(_SANDBOX_WSL_DISTRO_KEY, "")).strip()
                ),
            },
            "phoenix": {
                "origin": _phoenix_origin(config.phoenix_url),
                "project": config.phoenix_project,
                "readback_started_at": started_at,
                "readback_attempts": config.trace_readback_attempts,
                "readback_interval_seconds": config.trace_readback_interval_seconds,
            },
            "required_checks": sorted(REQUIRED_CHECKS),
            "required_spans": sorted(REQUIRED_SPANS),
        }
    )
    artifacts.write_environment()

    process_invoked = False
    process_result: subprocess.CompletedProcess[str] | None = None
    if not failures:
        command = (
            "go",
            "test",
            "./tests/e2e",
            "-run",
            "^TestProductionIndependentAgentsAndMemoryProcess$",
            "-count=1",
        )
        child_environment = _subprocess_environment(
            config.provider,
            environment,
            run_id=config.run_id,
            process_receipt=process_receipt_path,
            route_receipt=route_receipt_path,
            phoenix_url=config.phoenix_url,
        )
        try:
            process_invoked = True
            process_result = process_executor(
                command,
                config.repo_root.resolve(),
                child_environment,
                config.timeout_seconds,
            )
        except subprocess.TimeoutExpired:
            _failure(failures, "process_timeout", "independent-agent E2E timed out")
        except OSError as exc:
            _failure(
                failures,
                "process_start",
                f"independent-agent E2E could not start: {type(exc).__name__}",
            )

    artifacts.write(
        "process-execution.json",
        {
            "invoked": process_invoked,
            "command": (
                "go test ./tests/e2e -run "
                "^TestProductionIndependentAgentsAndMemoryProcess$ -count=1"
            ),
            "exit_code": process_result.returncode if process_result else None,
            "stdout_sha256": (
                _sha256_text(process_result.stdout or "") if process_result else ""
            ),
            "stderr_sha256": (
                _sha256_text(process_result.stderr or "") if process_result else ""
            ),
        },
    )
    if process_result is not None and process_result.returncode != 0:
        _failure(
            failures,
            "process_exit",
            f"independent-agent E2E exited with code {process_result.returncode}",
        )

    process_receipt: dict[str, Any] = {}
    if process_receipt_path.is_file():
        try:
            process_receipt = _load_object(
                process_receipt_path, "independent-agent process receipt"
            )
        except (TypeError, ValueError) as exc:
            _failure(failures, "process_receipt", str(exc))
            process_receipt_path.unlink(missing_ok=True)
    elif process_invoked:
        _failure(
            failures,
            "process_receipt",
            "independent-agent process receipt was not produced",
        )

    provider_events: list[dict[str, Any]] = []
    if route_receipt_path.is_file():
        try:
            provider_events = _load_provider_events(route_receipt_path)
        except (TypeError, ValueError) as exc:
            _failure(failures, "provider_route", str(exc))
            route_receipt_path.unlink(missing_ok=True)
    elif process_invoked:
        _failure(
            failures,
            "provider_route",
            "same-run provider route evidence was not produced",
        )
    artifacts.write("provider-identities.json", {"calls": provider_events})

    if process_receipt:
        if process_receipt.get("run_id") != config.run_id:
            _failure(
                failures,
                "process_binding",
                "process receipt run_id does not match the requested run",
            )
        if process_receipt.get("git_sha") != run_source_pin.get("git_sha"):
            _failure(
                failures,
                "process_binding",
                "process receipt Git SHA does not match the source pin",
            )
        if process_receipt.get("exit_code") != 0:
            _failure(
                failures,
                "process_receipt",
                "process receipt does not record a successful exit",
            )
        if process_receipt.get("provider_backed") is not True:
            _failure(
                failures,
                "provider_backing",
                "fixture-backed independent-agent evidence cannot satisfy this lane",
            )
        if process_receipt.get("provider_kind") != config.provider:
            _failure(
                failures,
                "process_binding",
                "process receipt provider does not match the requested provider",
            )
        if process_receipt.get("sandbox_backend") != sandbox_backend:
            _failure(
                failures,
                "process_binding",
                "process receipt sandbox backend does not match the approved configuration",
            )
        if process_receipt.get("provider_call_count") != len(provider_events):
            _failure(
                failures,
                "process_binding",
                "process receipt provider call count does not match route evidence",
            )
        if process_receipt.get("source_dirty") is not False:
            _failure(
                failures,
                "process_binding",
                "process receipt does not prove a clean source tree",
            )
        if route_receipt_path.is_file():
            route_sha256 = _sha256_file(route_receipt_path)
            if process_receipt.get("provider_route_sha256") != route_sha256:
                _failure(
                    failures,
                    "process_binding",
                    "provider route evidence is not pinned by the process receipt",
                )

    checks = _process_checks(process_receipt)
    missing_checks = sorted(name for name, passed in checks.items() if not passed)
    if missing_checks:
        _failure(
            failures,
            "agent_checks",
            "missing independent-agent checks: " + ", ".join(missing_checks),
        )

    parent_session_id = str(process_receipt.get("parent_session_id", ""))
    child_session_id = str(process_receipt.get("child_session_id", ""))
    if (
        not parent_session_id
        or not child_session_id
        or parent_session_id == child_session_id
    ):
        _failure(
            failures,
            "session_isolation",
            "distinct parent and child Session IDs were not proven",
        )

    route_sessions: set[str] = set()
    route_correlations: set[tuple[str, int]] = set()
    route_binding_ok = bool(provider_events)
    for event in provider_events:
        identity = event.get("model_identity", {})
        requested_model = (
            str(identity.get("requested_model", ""))
            if isinstance(identity, Mapping)
            else ""
        )
        correlation = (str(event.get("session_id", "")), int(event.get("turn", 0)))
        if correlation in route_correlations:
            route_binding_ok = False
        route_correlations.add(correlation)
        route_sessions.add(correlation[0])
        if (
            event.get("eval_run_id") != config.run_id
            or event.get("provider_backed") is not True
            or event.get("provider") != config.provider
            or correlation[0] not in {parent_session_id, child_session_id}
            or not str(event.get("model", "")).strip()
            or requested_model != event.get("model")
        ):
            route_binding_ok = False
    if route_sessions != {parent_session_id, child_session_id}:
        route_binding_ok = False
    if not route_binding_ok:
        _failure(
            failures,
            "provider_route_binding",
            "provider events must bind uniquely to this run, provider, model, and both Sessions",
        )

    identity_status = _identity_status(provider_events)
    if identity_status != "MODEL_IDENTITY_VERIFIED":
        _failure(
            failures,
            "model_identity",
            "every provider call must report model, response ID, and immutable fingerprint",
        )

    spans: list[CapturedSpan] = []
    trace_readback_error = ""
    trace_readback_attempts_used = 0
    if process_invoked and config.phoenix_url:
        for attempt in range(config.trace_readback_attempts):
            trace_readback_attempts_used = attempt + 1
            try:
                readback = phoenix_reader(
                    config.phoenix_url,
                    config.phoenix_project,
                    started_at,
                    config.run_id,
                    (parent_session_id, child_session_id),
                )
                spans = [
                    span
                    for span in readback
                    if str(span.attributes.get("eval.run_id", "")) == config.run_id
                ]
                trace_readback_error = ""
                if _trace_ready(spans, parent_session_id, child_session_id):
                    break
            except Exception as exc:  # noqa: BLE001 - external readback fails closed
                spans = []
                trace_readback_error = type(exc).__name__
            if attempt + 1 < config.trace_readback_attempts:
                sleep_fn(config.trace_readback_interval_seconds)
        if trace_readback_error:
            _failure(
                failures,
                "trace_readback",
                f"Phoenix readback failed after bounded retries: {trace_readback_error}",
            )
    trace = _trace_summary(
        spans,
        parent_session_id=parent_session_id,
        child_session_id=child_session_id,
    )
    artifacts.write(
        "trace-readback.json",
        {
            "phoenix_origin": _phoenix_origin(config.phoenix_url),
            "phoenix_project": config.phoenix_project,
            "readback_started_at": started_at,
            "readback_attempts_used": trace_readback_attempts_used,
            "spans": [span.to_dict() for span in spans],
        },
    )
    missing_spans = sorted(REQUIRED_SPANS - set(trace["observed_spans"]))
    if not trace["backend_readback"]:
        _failure(
            failures,
            "trace_readback",
            "Phoenix returned no spans for the E2E run",
        )
    if not trace["single_trace"]:
        _failure(
            failures,
            "trace_topology",
            "independent-agent evidence must be contained in one trace",
        )
    if not trace["all_ended"]:
        _failure(
            failures,
            "trace_completion",
            "independent-agent evidence contains unfinished spans",
        )
    if not trace["error_free"]:
        _failure(
            failures,
            "trace_status",
            "independent-agent success evidence contains error spans",
        )
    if not trace["parentage_complete"]:
        _failure(
            failures,
            "trace_topology",
            "independent-agent evidence contains spans with missing parents",
        )
    if (
        not trace["main_session_bound"]
        or not trace["subagent_session_bound"]
        or not trace["required_instances_bound"]
    ):
        _failure(
            failures,
            "trace_session_binding",
            "agent.main and agent.subagent are not bound to the parent and child Sessions",
        )
    if not trace["run_root_bound"]:
        _failure(
            failures,
            "trace_topology",
            "eval.run is not an unbound root span",
        )
    if set(trace["instance_ids"]) != {parent_session_id, child_session_id}:
        _failure(
            failures,
            "trace_session_binding",
            "trace instances do not match exactly the parent and child Sessions",
        )
    if missing_spans:
        _failure(
            failures,
            "trace_spans",
            "missing required spans: " + ", ".join(missing_spans),
        )

    status = "VERIFIED" if not failures else "BLOCKED"
    raw_evidence = {
        "storage_scope": "LOCAL_IGNORED",
        "artifact_root": (
            f"{config.artifact_root.as_posix().rstrip('/')}/{config.run_id}"
        ),
        "run_manifest_sha256": _sha256_file(artifacts.root / "run-manifest.json"),
        "process_execution_sha256": _sha256_file(
            artifacts.root / "process-execution.json"
        ),
        "provider_identities_sha256": _sha256_file(
            artifacts.root / "provider-identities.json"
        ),
        "trace_readback_sha256": _sha256_file(artifacts.root / "trace-readback.json"),
    }
    if process_receipt_path.is_file():
        raw_evidence["process_receipt_sha256"] = _sha256_file(process_receipt_path)
    if route_receipt_path.is_file():
        raw_evidence["provider_route_sha256"] = _sha256_file(route_receipt_path)

    reported_models = sorted(
        {
            str(event["model_identity"].get("reported_model", ""))
            for event in provider_events
            if isinstance(event.get("model_identity"), dict)
            and str(event["model_identity"].get("reported_model", ""))
        }
    )
    receipt = {
        "schema_version": 1,
        "status": status,
        "exit_code": 0 if status == "VERIFIED" else BLOCKED_EXIT_CODE,
        "run_id": config.run_id,
        "trace_id": trace["trace_ids"][0] if trace["single_trace"] else "",
        "source_pin": run_source_pin,
        "provider": {
            "backed": process_receipt.get("provider_backed") is True,
            "kind": config.provider,
            "model_revision_status": identity_status,
            "reported_models": reported_models,
            "call_count": len(provider_events),
        },
        "sandbox": {
            "backend": sandbox_backend,
            "image_configured": bool(
                str(environment.get(_SANDBOX_IMAGE_KEY, "")).strip()
            ),
            "wsl_distro_configured": bool(
                str(environment.get(_SANDBOX_WSL_DISTRO_KEY, "")).strip()
            ),
        },
        "isolation": {
            "parent_session_id": parent_session_id,
            "child_session_id": child_session_id,
        },
        "checks": checks,
        "trace": trace,
        "process": {
            "invoked": process_invoked,
            "exit_code": process_result.returncode if process_result else None,
        },
        "failures": failures,
        "raw_evidence": raw_evidence,
        "artifacts": {
            "process_receipt": (
                "process-receipt.json" if process_receipt_path.is_file() else ""
            ),
            "provider_identities": "provider-identities.json",
            "trace_readback": "trace-readback.json",
            "checksum_file": "checksums.sha256",
        },
    }
    return _finalize(artifacts, receipt)


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--run-id", required=True)
    parser.add_argument(
        "--artifact-root", type=Path, default=Path("eval_results/agent-e2e")
    )
    parser.add_argument("--repo-root", type=Path, default=Path.cwd())
    parser.add_argument(
        "--provider", default=os.environ.get("CODE_AGENT_E2E_PROVIDER", "")
    )
    parser.add_argument("--phoenix-url", default=os.environ.get("PHOENIX_URL", ""))
    parser.add_argument(
        "--phoenix-project",
        default=os.environ.get("PHOENIX_PROJECT", "default"),
    )
    parser.add_argument("--timeout", type=float, default=600.0)
    parser.add_argument("--trace-readback-attempts", type=int, default=6)
    parser.add_argument("--trace-readback-interval", type=float, default=1.0)
    return parser


def main(argv: list[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    try:
        receipt = run_independent_agent_e2e(
            IndependentAgentE2EConfig(
                run_id=args.run_id,
                artifact_root=args.artifact_root,
                repo_root=args.repo_root,
                provider=str(args.provider).strip().lower(),
                phoenix_url=str(args.phoenix_url).strip(),
                phoenix_project=str(args.phoenix_project).strip(),
                timeout_seconds=args.timeout,
                trace_readback_attempts=args.trace_readback_attempts,
                trace_readback_interval_seconds=args.trace_readback_interval,
            )
        )
    except (FileExistsError, OSError, ValueError) as exc:
        print(
            json.dumps(
                {"status": "ERROR", "error_type": type(exc).__name__},
                sort_keys=True,
            )
        )
        return ERROR_EXIT_CODE
    print(
        json.dumps(
            {
                "status": receipt["status"],
                "run_id": receipt["run_id"],
                "failure_categories": [
                    item["category"] for item in receipt["failures"]
                ],
            },
            sort_keys=True,
        )
    )
    return int(receipt["exit_code"])


if __name__ == "__main__":
    raise SystemExit(main())
