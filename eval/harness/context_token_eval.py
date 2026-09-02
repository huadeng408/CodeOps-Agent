"""Provider-reported A/B evaluation for layered repository context."""

from __future__ import annotations

import argparse
import asyncio
import hashlib
import json
import uuid
from dataclasses import dataclass, replace
from pathlib import Path
from typing import Any
from urllib.parse import urlparse

from eval.harness.artifacts import RunArtifacts
from eval.harness.redaction import redact_credential_text
from eval.harness.source_pin import source_pin
from orchestrator.config import load_dotenv
from orchestrator.context import LayeredContext, SQLiteContextStore
from orchestrator.llm import ChatMessage, ChatRequest, ChatResponse, LLMClient
from orchestrator.llm.providers import build_default_client

_SYSTEM_PROMPT = (
    "Answer the repository question using only the supplied context. "
    "Return exactly one JSON object with no Markdown or explanation. "
    "For class-name fields, use the unqualified class name unless the task "
    "explicitly requests a qualified name."
)
_SENSITIVE_NAMES = {
    ".env",
    ".env.local",
    ".env.production",
    "credentials.json",
    "id_ed25519",
    "id_rsa",
}
_LOOPBACK_HOSTS = {"127.0.0.1", "::1", "localhost"}


@dataclass(frozen=True, slots=True)
class ContextTokenTask:
    task_id: str
    question: str
    corpus_paths: tuple[str, ...]
    p3_paths: tuple[str, ...]
    expected: dict[str, Any]
    sha256: str


@dataclass(frozen=True, slots=True)
class ContextTokenEvalConfig:
    run_id: str
    artifact_root: Path
    project_root: Path
    task_path: Path
    model: str
    max_output_tokens: int = 128
    max_input_tokens_per_arm: int = 100_000
    timeout_s: float = 120.0
    minimum_reduction: float = 0.60

    def validate(self) -> None:
        if not self.run_id or Path(self.run_id).name != self.run_id:
            raise ValueError("run_id must be one safe directory name")
        if not self.model.strip():
            raise ValueError("model must not be empty")
        if self.max_output_tokens <= 0 or self.max_input_tokens_per_arm <= 0:
            raise ValueError("token budgets must be positive")
        if self.timeout_s <= 0:
            raise ValueError("timeout must be positive")
        if not 0.0 < self.minimum_reduction < 1.0:
            raise ValueError("minimum_reduction must be between zero and one")


@dataclass(frozen=True, slots=True)
class _ArmResult:
    call_completed: bool
    provider_input_tokens: int
    provider_output_tokens: int
    cached_input_tokens: int
    outcome_passed: bool
    response_sha256: str
    reported_model: str
    response_id_sha256: str
    system_fingerprint: str

    def receipt_value(self) -> dict[str, Any]:
        return {
            "call_completed": self.call_completed,
            "provider_input_tokens": self.provider_input_tokens,
            "provider_output_tokens": self.provider_output_tokens,
            "cached_input_tokens": self.cached_input_tokens,
            "outcome_passed": self.outcome_passed,
            "response_sha256": self.response_sha256,
            "reported_model": self.reported_model,
            "response_id_sha256": self.response_id_sha256,
            "system_fingerprint": self.system_fingerprint,
        }


def _status_exit_code(status: str) -> int:
    return 0 if status in {"VERIFIED", "SMOKE_PASS"} else 2


def _sha256_bytes(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


def _sha256_text(value: str) -> str:
    return _sha256_bytes(value.encode("utf-8"))


def _safe_relative_path(value: str) -> Path:
    path = Path(value)
    if (
        not value.strip()
        or path.is_absolute()
        or "\x00" in value
        or any(part in {"", ".", ".."} or ":" in part for part in path.parts)
    ):
        raise ValueError(f"unsafe corpus path: {value!r}")
    if path.name.lower() in _SENSITIVE_NAMES or path.name.lower().startswith(".env."):
        raise ValueError(f"sensitive corpus path is not allowed: {value!r}")
    return path


def load_context_token_task(path: str | Path) -> ContextTokenTask:
    task_path = Path(path)
    raw = task_path.read_bytes()
    payload = json.loads(raw.decode("utf-8"))
    if int(payload.get("schema_version", 0)) != 1:
        raise ValueError("context token task schema_version must be 1")
    task_id = str(payload.get("task_id", "")).strip()
    question = str(payload.get("question", "")).strip()
    corpus_paths = tuple(str(item) for item in payload.get("corpus_paths", ()))
    p3_paths = tuple(str(item) for item in payload.get("p3_paths", ()))
    expected = payload.get("expected")
    if not task_id or Path(task_id).name != task_id:
        raise ValueError("task_id must be one safe name")
    if not question:
        raise ValueError("task question must not be empty")
    if not corpus_paths or len(corpus_paths) != len(set(corpus_paths)):
        raise ValueError("corpus_paths must be non-empty and unique")
    for item in (*corpus_paths, *p3_paths):
        _safe_relative_path(item)
    if not p3_paths or not set(p3_paths).issubset(corpus_paths):
        raise ValueError("p3_paths must be a non-empty subset of corpus_paths")
    if not isinstance(expected, dict) or not expected:
        raise ValueError("expected must be a non-empty JSON object")
    return ContextTokenTask(
        task_id=task_id,
        question=question,
        corpus_paths=corpus_paths,
        p3_paths=p3_paths,
        expected=expected,
        sha256=_sha256_bytes(raw),
    )


def _materialize_corpus(
    task: ContextTokenTask,
    project_root: Path,
    workspace: Path,
) -> tuple[str, int]:
    root = project_root.resolve()
    workspace.mkdir(parents=True, exist_ok=False)
    manifest: list[dict[str, Any]] = []
    redacted_files = 0
    for relative in task.corpus_paths:
        relative_path = _safe_relative_path(relative)
        source = (root / relative_path).resolve()
        if not source.is_relative_to(root) or not source.is_file():
            raise ValueError(f"corpus file is unavailable: {relative}")
        if any(
            (root / Path(*relative_path.parts[:index])).is_symlink()
            for index in range(1, len(relative_path.parts) + 1)
        ):
            raise ValueError(f"corpus path must not traverse a symlink: {relative}")
        try:
            original = source.read_text(encoding="utf-8")
        except UnicodeDecodeError as exc:
            raise ValueError(f"corpus file must be UTF-8 text: {relative}") from exc
        safe_text = redact_credential_text(original)
        redacted_files += int(safe_text != original)
        destination = workspace / relative_path
        destination.parent.mkdir(parents=True, exist_ok=True)
        destination.write_text(safe_text, encoding="utf-8", newline="")
        content = destination.read_bytes()
        manifest.append(
            {
                "path": relative_path.as_posix(),
                "bytes": len(content),
                "sha256": _sha256_bytes(content),
            }
        )
    canonical = json.dumps(manifest, sort_keys=True, separators=(",", ":")).encode(
        "utf-8"
    )
    return _sha256_bytes(canonical), redacted_files


def _baseline_context(task: ContextTokenTask, workspace: Path) -> str:
    sections = ["FULL REPOSITORY CONTEXT"]
    for relative in task.corpus_paths:
        text = (workspace / _safe_relative_path(relative)).read_text(encoding="utf-8")
        sections.append(f"FILE {Path(relative).as_posix()}\n{text}\nEND FILE")
    return "\n\n".join(sections)


def _layered_context(task: ContextTokenTask, workspace: Path, database: Path) -> str:
    store = SQLiteContextStore(database)
    try:
        loader = LayeredContext(
            store,
            workspace,
            max_files=len(task.corpus_paths),
            max_raw_bytes=max(
                (workspace / _safe_relative_path(path)).stat().st_size
                for path in task.p3_paths
            )
            + 1,
        )
        snapshot = loader.load(task.task_id, raw_paths=task.p3_paths)
    finally:
        store.close()
    sections = [snapshot.p0, snapshot.p1]
    if snapshot.events_text:
        sections.append("SESSION EVENTS\n" + snapshot.events_text)
    for relative in task.p3_paths:
        key = Path(relative).as_posix()
        sections.append(f"P3 raw content: {key}\n{snapshot.p3[key]}")
    return "\n\n".join(sections)


def _parse_outcome(response: str) -> dict[str, Any] | None:
    try:
        value = json.loads(response)
    except (TypeError, json.JSONDecodeError):
        return None
    return value if isinstance(value, dict) else None


def _outcome_matches(
    actual: dict[str, Any] | None,
    expected: dict[str, Any],
) -> bool:
    if actual is None or actual.keys() != expected.keys():
        return False
    for key, expected_value in expected.items():
        allowed = (
            expected_value if isinstance(expected_value, list) else [expected_value]
        )
        if not allowed or actual[key] not in allowed:
            return False
    return True


async def _run_arm(
    client: LLMClient,
    config: ContextTokenEvalConfig,
    task: ContextTokenTask,
    context: str,
) -> tuple[_ArmResult, dict[str, Any]]:
    response: ChatResponse = await asyncio.wait_for(
        client.chat(
            ChatRequest(
                model=config.model,
                messages=[
                    ChatMessage(role="system", content=_SYSTEM_PROMPT),
                    ChatMessage(
                        role="user",
                        content=f"{task.question}\n\nRepository context:\n{context}",
                    ),
                ],
                temperature=0.0,
                thinking_enabled=False,
            )
        ),
        timeout=config.timeout_s,
    )
    identity = response.model_identity or {}
    response_id = str(identity.get("response_id", ""))
    parsed = _parse_outcome(response.text)
    result = _ArmResult(
        call_completed=True,
        provider_input_tokens=int(response.usage.input_tokens),
        provider_output_tokens=int(response.usage.output_tokens),
        cached_input_tokens=int(response.usage.cached_input_tokens),
        outcome_passed=_outcome_matches(parsed, task.expected),
        response_sha256=_sha256_text(response.text),
        reported_model=str(identity.get("reported_model", "")),
        response_id_sha256=_sha256_text(response_id) if response_id else "",
        system_fingerprint=str(identity.get("system_fingerprint", "")),
    )
    raw = {
        "response": redact_credential_text(response.text),
        "parsed_as_json_object": parsed is not None,
        "outcome_passed": result.outcome_passed,
        "usage": {
            "input_tokens": result.provider_input_tokens,
            "output_tokens": result.provider_output_tokens,
            "cached_input_tokens": result.cached_input_tokens,
        },
        "model_identity": identity,
    }
    return result, raw


def _evidence_scope(client: LLMClient) -> tuple[str, str | None, dict[str, str]]:
    endpoint = urlparse(str(getattr(client, "base_url", "")))
    host = (endpoint.hostname or "").lower()
    metadata = {"scheme": endpoint.scheme.lower(), "host": host}
    if host in _LOOPBACK_HOSTS:
        return "LOCAL_PROVIDER_INTEGRATION", None, metadata
    if endpoint.scheme.lower() != "https" or not host:
        return (
            "UNTRUSTED_PROVIDER_TRANSPORT",
            "remote provider endpoint must use HTTPS",
            metadata,
        )
    return "REMOTE_PROVIDER_REPORTED_USAGE", None, metadata


async def run_context_token_eval(
    config: ContextTokenEvalConfig,
    client: LLMClient,
) -> dict[str, Any]:
    config.validate()
    task = load_context_token_task(config.task_path)
    artifact_base = config.artifact_root.resolve()
    run_root = artifact_base / config.run_id
    if run_root.exists() and any(run_root.iterdir()):
        raise FileExistsError(f"refusing to overwrite run directory: {run_root}")
    artifacts = RunArtifacts(config.run_id, artifact_base)
    workspace = artifacts.root / "workspace"
    corpus_sha256, redacted_files = _materialize_corpus(
        task, config.project_root, workspace
    )
    baseline_context = _baseline_context(task, workspace)
    layered_context = _layered_context(
        task, workspace, artifacts.root / "context.sqlite"
    )
    evaluation_client = replace(
        client, model=config.model, max_tokens=config.max_output_tokens
    )
    scope, transport_failure, endpoint = _evidence_scope(evaluation_client)
    run_source_pin = source_pin(Path(__file__).resolve().parents[2])
    trace_id = uuid.uuid4().hex
    artifacts.write_manifest(
        {
            "schema_version": 1,
            "run_id": config.run_id,
            "trace_id": trace_id,
            "source_pin": run_source_pin,
            "data_pin": {
                "task_sha256": task.sha256,
                "corpus_sha256": corpus_sha256,
            },
            "requested_model": config.model,
            "provider_endpoint": endpoint,
            "budget": {
                "max_output_tokens_per_arm": config.max_output_tokens,
                "max_input_tokens_per_arm": config.max_input_tokens_per_arm,
                "timeout_seconds_per_arm": config.timeout_s,
                "temperature": 0.0,
                "calls_per_arm": 1,
            },
        }
    )
    artifacts.write_environment()
    failures: list[dict[str, str]] = []

    async def attempt_arm(name: str, context: str) -> tuple[_ArmResult, dict[str, Any]]:
        try:
            return await _run_arm(evaluation_client, config, task, context)
        except Exception as exc:  # noqa: BLE001 - provider failures become auditable receipt rows.
            failures.append(
                {
                    "category": "provider_call",
                    "message": f"{name} provider call failed: {type(exc).__name__}",
                }
            )
            return (
                _ArmResult(
                    call_completed=False,
                    provider_input_tokens=0,
                    provider_output_tokens=0,
                    cached_input_tokens=0,
                    outcome_passed=False,
                    response_sha256="",
                    reported_model="",
                    response_id_sha256="",
                    system_fingerprint="",
                ),
                {
                    "call_completed": False,
                    "error_type": type(exc).__name__,
                    "error": redact_credential_text(str(exc)),
                },
            )

    baseline, baseline_raw = await attempt_arm("baseline", baseline_context)
    layered, layered_raw = await attempt_arm("layered", layered_context)
    artifacts.write(
        "arm-results.json",
        {"baseline": baseline_raw, "layered": layered_raw},
    )

    if transport_failure:
        failures.append(
            {"category": "provider_transport", "message": transport_failure}
        )
    for name, result in (("baseline", baseline), ("layered", layered)):
        if not result.call_completed:
            continue
        if result.provider_input_tokens <= 0:
            failures.append(
                {
                    "category": "provider_usage",
                    "message": f"{name} input_tokens missing",
                }
            )
        if result.provider_input_tokens > config.max_input_tokens_per_arm:
            failures.append(
                {"category": "budget", "message": f"{name} input token budget exceeded"}
            )
        if result.provider_output_tokens > config.max_output_tokens:
            failures.append(
                {
                    "category": "budget",
                    "message": f"{name} output token budget exceeded",
                }
            )
        if not result.outcome_passed:
            failures.append(
                {"category": "outcome", "message": f"{name} task outcome failed"}
            )

    both_calls_completed = baseline.call_completed and layered.call_completed
    reported_models = {baseline.reported_model, layered.reported_model}
    if both_calls_completed and (
        "" in reported_models or reported_models != {config.model}
    ):
        failures.append(
            {
                "category": "model_identity",
                "message": "provider-reported model does not match both locked arms",
            }
        )
    fingerprints = {baseline.system_fingerprint, layered.system_fingerprint}
    model_revision_status = (
        "VERIFIED"
        if both_calls_completed and "" not in fingerprints and len(fingerprints) == 1
        else "MODEL_IDENTITY_UNVERIFIED"
    )
    baseline_tokens = baseline.provider_input_tokens
    reduction = (
        (baseline_tokens - layered.provider_input_tokens) / baseline_tokens
        if baseline_tokens > 0
        else 0.0
    )
    if both_calls_completed and reduction < config.minimum_reduction:
        failures.append(
            {
                "category": "token_reduction",
                "message": "provider-reported input token reduction is below threshold",
            }
        )
    outcome_regressed = baseline.outcome_passed and not layered.outcome_passed
    if outcome_regressed and not any(
        item["category"] == "outcome" for item in failures
    ):
        failures.append(
            {"category": "outcome", "message": "layered task outcome regressed"}
        )

    passed = not failures
    status = (
        "VERIFIED"
        if passed and scope == "REMOTE_PROVIDER_REPORTED_USAGE"
        else "SMOKE_PASS"
        if passed
        else "BLOCKED"
    )
    receipt = {
        "schema_version": 1,
        "status": status,
        "exit_code": _status_exit_code(status),
        "evidence_scope": scope,
        "limitations": [
            "This lane verifies provider-reported context A/B usage, not the full product E2E.",
            "P3 paths are task-pinned; this lane does not measure context-selection accuracy.",
        ],
        "run_id": config.run_id,
        "trace_id": trace_id,
        "source_pin": run_source_pin,
        "data_pin": {
            "task_id": task.task_id,
            "task_sha256": task.sha256,
            "corpus_sha256": corpus_sha256,
            "corpus_file_count": len(task.corpus_paths),
            "redacted_file_count": redacted_files,
        },
        "prompt_pins": {
            "system_sha256": _sha256_text(_SYSTEM_PROMPT),
            "task_sha256": _sha256_text(task.question),
            "baseline_context_sha256": _sha256_text(baseline_context),
            "layered_context_sha256": _sha256_text(layered_context),
        },
        "provider": {
            "requested_model": config.model,
            "endpoint": endpoint,
            "model_revision_status": model_revision_status,
        },
        "budget": {
            "max_output_tokens_per_arm": config.max_output_tokens,
            "max_input_tokens_per_arm": config.max_input_tokens_per_arm,
            "timeout_seconds_per_arm": config.timeout_s,
            "temperature": 0.0,
            "calls_per_arm": 1,
        },
        "arms": {
            "baseline": baseline.receipt_value(),
            "layered": layered.receipt_value(),
        },
        "comparison": {
            "baseline_input_token_denominator": baseline_tokens,
            "layered_input_token_denominator": layered.provider_input_tokens,
            "saved_input_tokens": baseline_tokens - layered.provider_input_tokens,
            "input_token_reduction": reduction,
            "minimum_required_reduction": config.minimum_reduction,
            "outcome_regressed": outcome_regressed,
        },
        "failures": failures,
        "artifacts": {
            "arm_results": "arm-results.json",
            "checksum_file": "checksums.sha256",
            "context_database": "context.sqlite",
        },
    }
    artifacts.write("receipt.json", receipt)
    artifacts.write_checksums()
    checksum_problems = artifacts.verify_checksums()
    if checksum_problems:
        receipt["status"] = "BLOCKED"
        receipt["exit_code"] = _status_exit_code(receipt["status"])
        receipt["failures"].append(
            {"category": "artifact_checksum", "message": "; ".join(checksum_problems)}
        )
        artifacts.write("receipt.json", receipt)
        artifacts.write_checksums()
        if artifacts.verify_checksums():
            raise RuntimeError("context token artifacts failed checksum verification")
    return receipt


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--task", required=True, type=Path)
    parser.add_argument("--project-root", default=Path.cwd(), type=Path)
    parser.add_argument("--artifact-root", default=Path("eval_results"), type=Path)
    parser.add_argument("--run-id", required=True)
    parser.add_argument("--model", required=True)
    parser.add_argument("--max-output-tokens", default=128, type=int)
    parser.add_argument("--max-input-tokens-per-arm", default=100_000, type=int)
    parser.add_argument("--timeout", default=120.0, type=float)
    parser.add_argument("--minimum-reduction", default=0.60, type=float)
    return parser


def main(argv: list[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    load_dotenv()
    client = build_default_client()
    if not isinstance(client, LLMClient):
        raise TypeError("a provider client implementing LLMClient is required")
    config = ContextTokenEvalConfig(
        run_id=args.run_id,
        artifact_root=args.artifact_root,
        project_root=args.project_root,
        task_path=args.task,
        model=args.model,
        max_output_tokens=args.max_output_tokens,
        max_input_tokens_per_arm=args.max_input_tokens_per_arm,
        timeout_s=args.timeout,
        minimum_reduction=args.minimum_reduction,
    )
    receipt = asyncio.run(run_context_token_eval(config, client))
    print(
        json.dumps(
            {
                "status": receipt["status"],
                "run_id": receipt["run_id"],
                "input_token_reduction": receipt["comparison"]["input_token_reduction"],
            },
            ensure_ascii=False,
        )
    )
    return int(receipt["exit_code"])


if __name__ == "__main__":
    raise SystemExit(main())
