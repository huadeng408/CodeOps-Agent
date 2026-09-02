"""Fixed-corpus evaluation for model-selected Harness Skills."""

from __future__ import annotations

import argparse
import asyncio
import hashlib
import json
import re
import uuid
from dataclasses import dataclass, replace
from pathlib import Path
from typing import Any
from urllib.parse import urlparse

from eval.harness.artifacts import RunArtifacts
from eval.harness.redaction import redact_credential_text
from eval.harness.skill_selection_checkpoint import SkillSelectionCheckpoint
from eval.harness.source_pin import source_pin
from orchestrator.config import load_dotenv
from orchestrator.llm import ChatMessage, ChatRequest, ChatResponse
from orchestrator.llm.providers import OpenAIClient, build_default_client
from orchestrator.runtime.tools import ToolRegistry

_SYSTEM_PROMPT = (
    "Select exactly one registered Skill for the user request. "
    "Call the Skill tool once with its exact name. Do not answer the request."
)
_LOOPBACK_HOSTS = {"127.0.0.1", "::1", "localhost"}
_CHECKPOINT_FILENAME = "checkpoint.sqlite3"
_SELECTIONS_FILENAME = "selections.jsonl"


@dataclass(frozen=True, slots=True)
class SkillSelectionCase:
    case_id: str
    prompt: str
    expected_skill: str


@dataclass(frozen=True, slots=True)
class SkillSelectionDataset:
    dataset_id: str
    license: str
    provenance: str
    sha256: str
    cases: tuple[SkillSelectionCase, ...]


@dataclass(frozen=True, slots=True)
class SkillSelectionEvalConfig:
    run_id: str
    artifact_root: Path
    manifest_path: Path
    dataset_path: Path
    model: str
    max_output_tokens: int = 64
    timeout_s: float = 120.0
    max_concurrency: int = 10
    minimum_accuracy: float = 0.948
    required_case_count: int = 1_000
    resume: bool = False

    def validate(self) -> None:
        if not self.run_id or Path(self.run_id).name != self.run_id:
            raise ValueError("run_id must be one safe directory name")
        if not self.model.strip():
            raise ValueError("model must not be empty")
        if self.max_output_tokens <= 0 or self.timeout_s <= 0:
            raise ValueError("token and timeout budgets must be positive")
        if not 1 <= self.max_concurrency <= 10:
            raise ValueError("max_concurrency must be between 1 and 10")
        if not 0.0 < self.minimum_accuracy <= 1.0:
            raise ValueError("minimum_accuracy must be in (0, 1]")
        if self.required_case_count <= 0:
            raise ValueError("required_case_count must be positive")


@dataclass(frozen=True, slots=True)
class _Catalog:
    names: frozenset[str]
    sha256: str
    tool_schema: dict[str, Any]


@dataclass(frozen=True, slots=True)
class _SelectionResult:
    case_id: str
    call_completed: bool
    selected_skill: str
    correct: bool
    provider_input_tokens: int
    provider_output_tokens: int
    reported_model: str
    system_fingerprint: str
    response_sha256: str
    response_id_sha256: str
    issue: str = ""
    failure_category: str = ""
    failure_message: str = ""

    def artifact_value(self) -> dict[str, Any]:
        return {
            "case_id": self.case_id,
            "call_completed": self.call_completed,
            "selected_skill": self.selected_skill,
            "correct": self.correct,
            "provider_input_tokens": self.provider_input_tokens,
            "provider_output_tokens": self.provider_output_tokens,
            "reported_model": self.reported_model,
            "system_fingerprint": self.system_fingerprint,
            "response_sha256": self.response_sha256,
            "response_id_sha256": self.response_id_sha256,
            "issue": self.issue,
            "failure_category": self.failure_category,
            "failure_message": self.failure_message,
        }


def _sha256_bytes(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


def _sha256_text(value: str) -> str:
    return _sha256_bytes(value.encode("utf-8"))


def _status_exit_code(status: str) -> int:
    return 0 if status in {"VERIFIED", "SMOKE_PASS"} else 2


def _provider_failure_message(exc: Exception) -> str:
    redacted = redact_credential_text(str(exc))
    status_match = re.search(r"\bHTTP\s+(\d{3})\b", redacted)
    status = f" HTTP {status_match.group(1)}" if status_match else ""
    return (
        f"{type(exc).__name__}: provider{status} call failed; "
        f"detail_sha256={_sha256_text(redacted)}"
    )


def _verify_checksum_sidecar(path: Path, raw: bytes) -> None:
    sidecar = path.with_suffix(path.suffix + ".sha256")
    try:
        line = sidecar.read_text(encoding="ascii").strip()
    except OSError as exc:
        raise ValueError("dataset checksum sidecar is required") from exc
    digest, separator, filename = line.partition("  ")
    if (
        not separator
        or re.fullmatch(r"[0-9a-f]{64}", digest) is None
        or filename != path.name
        or digest != _sha256_bytes(raw)
    ):
        raise ValueError("dataset checksum sidecar does not match the dataset")


def _load_catalog(path: str | Path) -> _Catalog:
    manifest_path = Path(path).resolve()
    if manifest_path.name != "skills.json" or manifest_path.parent.name != ".agent":
        raise ValueError("manifest_path must point to .agent/skills.json")
    raw = manifest_path.read_bytes()
    payload = json.loads(raw.decode("utf-8"))
    raw_skills = payload.get("skills", []) if isinstance(payload, dict) else []
    names: set[str] = set()
    for item in raw_skills:
        if not isinstance(item, dict):
            raise TypeError("skill manifest entries must be objects")
        name = str(item.get("name", "")).strip()
        description = str(item.get("description", "")).strip()
        if not name or not description or "prompt" in item:
            raise ValueError("skill manifest requires metadata only")
        if name in names:
            raise ValueError(f"duplicate skill name: {name}")
        names.add(name)
    if not names:
        raise ValueError("skill manifest must not be empty")
    registry = ToolRegistry(str(manifest_path.parent.parent), allowed_tools={"Skill"})
    skill_tool = registry.get("Skill")
    if skill_tool is None:
        raise ValueError("runtime Skill tool is unavailable")
    return _Catalog(
        names=frozenset(names),
        sha256=_sha256_bytes(raw),
        tool_schema=skill_tool.to_openai_schema(),
    )


def load_skill_selection_dataset(
    dataset_path: str | Path,
    manifest_path: str | Path,
) -> SkillSelectionDataset:
    catalog = _load_catalog(manifest_path)
    path = Path(dataset_path)
    raw = path.read_bytes()
    _verify_checksum_sidecar(path, raw)
    payload = json.loads(raw.decode("utf-8"))
    if not isinstance(payload, dict) or int(payload.get("schema_version", 0)) != 1:
        raise ValueError("skill selection dataset schema_version must be 1")
    dataset_id = str(payload.get("dataset_id", "")).strip()
    license_name = str(payload.get("license", "")).strip()
    provenance = str(payload.get("provenance", "")).strip()
    expected_count = int(payload.get("expected_case_count", 0))
    templates = payload.get("prompt_templates")
    raw_skills = payload.get("skills")
    if not dataset_id or Path(dataset_id).name != dataset_id:
        raise ValueError("dataset_id must be one safe name")
    if not license_name or not provenance:
        raise ValueError("dataset license and provenance are required")
    if not isinstance(templates, list) or not templates:
        raise ValueError("prompt_templates must be a non-empty list")
    if not isinstance(raw_skills, list) or not raw_skills:
        raise ValueError("dataset skills must be a non-empty list")

    cases: list[SkillSelectionCase] = []
    dataset_names: set[str] = set()
    prompts: set[str] = set()
    for skill_index, raw_skill in enumerate(raw_skills):
        if not isinstance(raw_skill, dict):
            raise TypeError("dataset skill entries must be objects")
        name = str(raw_skill.get("name", "")).strip()
        requests = raw_skill.get("requests")
        if not name or name in dataset_names:
            raise ValueError("dataset skill names must be non-empty and unique")
        if not isinstance(requests, list) or not requests:
            raise ValueError(f"dataset skill {name} requires requests")
        dataset_names.add(name)
        for request_index, request_value in enumerate(requests):
            request = str(request_value).strip()
            if not request:
                raise ValueError(f"dataset skill {name} has an empty request")
            for template_index, template_value in enumerate(templates):
                template = str(template_value)
                if template.count("{request}") != 1:
                    raise ValueError("each prompt template must contain {request} once")
                try:
                    prompt = template.format(request=request).strip()
                except (KeyError, ValueError) as exc:
                    raise ValueError("prompt template is invalid") from exc
                if not prompt or prompt in prompts:
                    raise ValueError("expanded prompts must be non-empty and unique")
                prompts.add(prompt)
                cases.append(
                    SkillSelectionCase(
                        case_id=(
                            f"{dataset_id}-{skill_index:02d}-{request_index:02d}-"
                            f"{template_index:02d}"
                        ),
                        prompt=prompt,
                        expected_skill=name,
                    )
                )
    if dataset_names != set(catalog.names):
        raise ValueError("dataset catalog coverage must exactly match the manifest")
    if len(cases) != expected_count:
        raise ValueError(
            f"expanded case count {len(cases)} does not match expected {expected_count}"
        )
    return SkillSelectionDataset(
        dataset_id=dataset_id,
        license=license_name,
        provenance=provenance,
        sha256=_sha256_bytes(raw),
        cases=tuple(cases),
    )


def _endpoint_pin(client: OpenAIClient) -> dict[str, str]:
    parsed = urlparse(client.base_url)
    scheme = parsed.scheme.lower()
    host = (parsed.hostname or "").lower()
    default_port = 443 if scheme == "https" else 80 if scheme == "http" else None
    endpoint = {
        "scheme": scheme,
        "host": host,
        "port": str(parsed.port or default_port or ""),
        "path": parsed.path.rstrip("/") or "/",
    }
    return endpoint


def _evidence_scope(client: OpenAIClient) -> tuple[str, str, dict[str, str]]:
    endpoint = _endpoint_pin(client)
    scheme = endpoint["scheme"]
    host = endpoint["host"]
    if scheme in {"http", "https"} and host in _LOOPBACK_HOSTS:
        return "LOCAL_PROVIDER_INTEGRATION", "", endpoint
    if scheme == "https" and host:
        return "REMOTE_PROVIDER_TOOL_SELECTION", "", endpoint
    return (
        "UNTRUSTED_PROVIDER_TRANSPORT",
        "formal Skill selection evidence requires remote HTTPS or loopback smoke",
        endpoint,
    )


def _prompt_pins(catalog: _Catalog) -> dict[str, str]:
    return {
        "system_sha256": _sha256_text(_SYSTEM_PROMPT),
        "tool_schema_sha256": _sha256_bytes(
            json.dumps(
                catalog.tool_schema,
                sort_keys=True,
                separators=(",", ":"),
            ).encode("utf-8")
        ),
    }


def _budget_pin(
    config: SkillSelectionEvalConfig,
    client: OpenAIClient,
    case_count: int,
) -> dict[str, int | float]:
    return {
        "max_output_tokens_per_case": config.max_output_tokens,
        "timeout_seconds_per_case": config.timeout_s,
        "max_concurrency": config.max_concurrency,
        "calls": case_count,
        "max_provider_attempts_per_case": client.max_retries + 1,
        "temperature": 0.0,
        "minimum_required_accuracy": config.minimum_accuracy,
    }


def _checkpoint_contract(
    config: SkillSelectionEvalConfig,
    dataset: SkillSelectionDataset,
    catalog: _Catalog,
    source: dict[str, Any],
    endpoint: dict[str, str],
    prompt_pins: dict[str, str],
    budget: dict[str, int | float],
) -> dict[str, Any]:
    return {
        "schema_version": 1,
        "run_id": config.run_id,
        "source_pin": source,
        "dataset_pin": {
            "dataset_id": dataset.dataset_id,
            "sha256": dataset.sha256,
            "case_count": len(dataset.cases),
        },
        "catalog_pin": {
            "sha256": catalog.sha256,
            "skill_count": len(catalog.names),
        },
        "model": config.model,
        "endpoint": endpoint,
        "prompt_pins": prompt_pins,
        "budget": budget,
    }


def _selection_result(payload: dict[str, Any]) -> _SelectionResult:
    try:
        return _SelectionResult(
            case_id=str(payload["case_id"]),
            call_completed=bool(payload["call_completed"]),
            selected_skill=str(payload["selected_skill"]),
            correct=bool(payload["correct"]),
            provider_input_tokens=int(payload["provider_input_tokens"]),
            provider_output_tokens=int(payload["provider_output_tokens"]),
            reported_model=str(payload["reported_model"]),
            system_fingerprint=str(payload["system_fingerprint"]),
            response_sha256=str(payload["response_sha256"]),
            response_id_sha256=str(payload["response_id_sha256"]),
            issue=str(payload.get("issue", "")),
            failure_category=str(payload.get("failure_category", "")),
            failure_message=str(payload.get("failure_message", "")),
        )
    except (KeyError, TypeError, ValueError) as exc:
        raise ValueError("checkpoint selection result is invalid") from exc


def _resume_trace_id(
    artifacts: RunArtifacts, contract: dict[str, Any], digest: str
) -> str:
    manifest_path = artifacts.root / "run-manifest.json"
    try:
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise ValueError("resume requires a valid run manifest") from exc
    if (
        not isinstance(manifest, dict)
        or manifest.get("run_id") != contract["run_id"]
        or manifest.get("checkpoint_contract_sha256") != digest
        or manifest.get("checkpoint_contract") != contract
    ):
        raise ValueError("run manifest does not match the checkpoint contract")
    trace_id = str(manifest.get("trace_id", ""))
    if re.fullmatch(r"[0-9a-f]{32}", trace_id) is None:
        raise ValueError("run manifest trace_id is invalid")
    return trace_id


def _selected_skill(response: ChatResponse, catalog: _Catalog) -> tuple[str, str]:
    if len(response.tool_calls) != 1:
        return "", "expected exactly one tool call"
    call = response.tool_calls[0]
    if call.name != "Skill":
        return "", "model called a tool other than Skill"
    selected = str(call.arguments.get("name", "")).strip()
    if not selected:
        return "", "Skill tool call omitted name"
    if selected not in catalog.names:
        return selected, "model selected an unknown Skill"
    return selected, ""


async def _run_case(
    client: OpenAIClient,
    config: SkillSelectionEvalConfig,
    catalog: _Catalog,
    case: SkillSelectionCase,
    semaphore: asyncio.Semaphore,
) -> _SelectionResult:
    async with semaphore:
        try:
            response = await asyncio.wait_for(
                client.chat(
                    ChatRequest(
                        model=config.model,
                        messages=[
                            ChatMessage(role="system", content=_SYSTEM_PROMPT),
                            ChatMessage(role="user", content=case.prompt),
                        ],
                        tools=[catalog.tool_schema],
                        temperature=0.0,
                        thinking_enabled=False,
                    )
                ),
                timeout=config.timeout_s,
            )
        except Exception as exc:  # noqa: BLE001 - provider failures become evidence.
            return _SelectionResult(
                case_id=case.case_id,
                call_completed=False,
                selected_skill="",
                correct=False,
                provider_input_tokens=0,
                provider_output_tokens=0,
                reported_model="",
                system_fingerprint="",
                response_sha256="",
                response_id_sha256="",
                failure_category="provider_call",
                failure_message=_provider_failure_message(exc),
            )

    identity = response.model_identity or {}
    selected, issue = _selected_skill(response, catalog)
    input_tokens = int(response.usage.input_tokens)
    output_tokens = int(response.usage.output_tokens)
    reported_model = str(identity.get("reported_model", ""))
    failure_category = ""
    failure_message = ""
    if input_tokens <= 0:
        failure_category = "provider_usage"
        failure_message = "provider did not report positive input token usage"
    elif output_tokens < 0 or output_tokens > config.max_output_tokens:
        failure_category = "provider_budget"
        failure_message = (
            "provider-reported output token usage exceeds the locked budget"
        )
    elif reported_model != config.model:
        failure_category = "model_identity"
        failure_message = "provider-reported model does not match the locked model"
    return _SelectionResult(
        case_id=case.case_id,
        call_completed=True,
        selected_skill=selected,
        correct=not issue and selected == case.expected_skill,
        provider_input_tokens=input_tokens,
        provider_output_tokens=output_tokens,
        reported_model=reported_model,
        system_fingerprint=str(identity.get("system_fingerprint", "")),
        response_sha256=_sha256_text(response.text),
        response_id_sha256=_sha256_text(str(identity.get("response_id", ""))),
        issue=issue,
        failure_category=failure_category,
        failure_message=failure_message,
    )


async def run_skill_selection_eval(
    config: SkillSelectionEvalConfig,
    client: OpenAIClient,
) -> dict[str, Any]:
    config.validate()
    dataset = load_skill_selection_dataset(config.dataset_path, config.manifest_path)
    if len(dataset.cases) != config.required_case_count:
        raise ValueError(
            f"official denominator requires {config.required_case_count} cases, "
            f"got {len(dataset.cases)}"
        )
    catalog = _load_catalog(config.manifest_path)
    artifacts = RunArtifacts(config.run_id, config.artifact_root)
    if not config.resume and any(artifacts.root.iterdir()):
        raise ValueError("run artifact directory must be empty")
    evaluation_client = replace(
        client,
        model=config.model,
        max_tokens=config.max_output_tokens,
    )
    scope, transport_failure, endpoint = _evidence_scope(evaluation_client)
    run_source_pin = source_pin(Path(__file__).resolve().parents[2])
    prompt_pins = _prompt_pins(catalog)
    budget = _budget_pin(config, evaluation_client, len(dataset.cases))
    contract = _checkpoint_contract(
        config,
        dataset,
        catalog,
        run_source_pin,
        endpoint,
        prompt_pins,
        budget,
    )
    checkpoint_path = (artifacts.root / _CHECKPOINT_FILENAME).resolve()
    if not checkpoint_path.is_relative_to(artifacts.root):
        raise ValueError("checkpoint path must stay within the run artifact directory")
    if config.resume and not checkpoint_path.is_file():
        raise ValueError("resume requires an existing checkpoint")

    checkpoint = SkillSelectionCheckpoint(checkpoint_path)
    try:
        if config.resume:
            contract_sha256 = checkpoint.validate_contract(contract)
            trace_id = _resume_trace_id(artifacts, contract, contract_sha256)
        else:
            contract_sha256 = checkpoint.initialize(contract)
            trace_id = uuid.uuid4().hex
            artifacts.write_manifest(
                {
                    "schema_version": 1,
                    "run_id": config.run_id,
                    "trace_id": trace_id,
                    "source_pin": run_source_pin,
                    "dataset_id": dataset.dataset_id,
                    "dataset_sha256": dataset.sha256,
                    "catalog_sha256": catalog.sha256,
                    "requested_model": config.model,
                    "provider_endpoint": endpoint,
                    "budget": budget,
                    "checkpoint_contract_sha256": contract_sha256,
                    "checkpoint_contract": contract,
                }
            )
            artifacts.write_environment()

        completed_ids = checkpoint.completed_case_ids()
        dataset_ids = frozenset(case.case_id for case in dataset.cases)
        unknown_ids = completed_ids - dataset_ids
        if unknown_ids:
            raise ValueError(
                "checkpoint contains case IDs outside the locked dataset: "
                + ", ".join(sorted(unknown_ids))
            )
        pending_cases = [
            case for case in dataset.cases if case.case_id not in completed_ids
        ]
        semaphore = asyncio.Semaphore(config.max_concurrency)

        async def run_and_checkpoint(case: SkillSelectionCase) -> None:
            result = await _run_case(
                evaluation_client,
                config,
                catalog,
                case,
                semaphore,
            )
            checkpoint.save_result(case.case_id, result.artifact_value())

        await asyncio.gather(*(run_and_checkpoint(case) for case in pending_cases))
        results = [
            _selection_result(payload)
            for payload in checkpoint.load_ordered_results(
                case.case_id for case in dataset.cases
            )
        ]
        checkpoint.finalize()
    finally:
        checkpoint.close()

    artifacts.write_jsonl(
        _SELECTIONS_FILENAME,
        (result.artifact_value() for result in results),
    )

    correct = sum(result.correct for result in results)
    denominator = len(dataset.cases)
    incorrect_ids = [result.case_id for result in results if not result.correct]
    issue_ids = [result.case_id for result in results if result.issue]
    operational = [result for result in results if result.failure_category]
    accuracy = correct / denominator
    fingerprints = {result.system_fingerprint for result in results}
    model_revision_status = (
        "VERIFIED"
        if not operational and "" not in fingerprints and len(fingerprints) == 1
        else "MODEL_IDENTITY_UNVERIFIED"
    )
    failures: list[dict[str, str]] = []
    if transport_failure:
        failures.append({"category": "transport", "message": transport_failure})
    if operational:
        categories = sorted({result.failure_category for result in operational})
        failures.append(
            {
                "category": "provider_integrity",
                "message": (
                    f"{len(operational)} case(s) failed provider integrity: "
                    + ", ".join(categories)
                ),
            }
        )
    if accuracy < config.minimum_accuracy:
        failures.append(
            {
                "category": "accuracy",
                "message": "Skill selection accuracy is below the locked threshold",
            }
        )
    if (
        scope == "REMOTE_PROVIDER_TOOL_SELECTION"
        and model_revision_status != "VERIFIED"
    ):
        failures.append(
            {
                "category": "model_revision",
                "message": (
                    "remote verification requires one non-empty provider "
                    "system fingerprint across all cases"
                ),
            }
        )
    if scope == "REMOTE_PROVIDER_TOOL_SELECTION" and (
        denominator != 1_000
        or len(catalog.names) < 40
        or config.minimum_accuracy < 0.948
    ):
        failures.append(
            {
                "category": "formal_contract",
                "message": (
                    "remote verification requires at least 40 Skills, exactly "
                    "1,000 cases, and a minimum accuracy threshold of 0.948"
                ),
            }
        )
    passed = not failures
    status = (
        "VERIFIED"
        if passed and scope == "REMOTE_PROVIDER_TOOL_SELECTION"
        else "SMOKE_PASS"
        if passed
        else "BLOCKED"
    )
    receipt: dict[str, Any] = {
        "schema_version": 1,
        "status": status,
        "exit_code": _status_exit_code(status),
        "evidence_scope": scope,
        "limitations": [
            "Repository-authored cases measure selection against this catalog, not external task validity.",
            "A blank provider fingerprint leaves the immutable model revision unverified.",
        ],
        "run_id": config.run_id,
        "trace_id": trace_id,
        "source_pin": run_source_pin,
        "dataset_pin": {
            "dataset_id": dataset.dataset_id,
            "sha256": dataset.sha256,
            "license": dataset.license,
            "provenance": dataset.provenance,
            "case_count": denominator,
        },
        "catalog_pin": {
            "sha256": catalog.sha256,
            "skill_count": len(catalog.names),
        },
        "prompt_pins": prompt_pins,
        "provider": {
            "requested_model": config.model,
            "endpoint": endpoint,
            "model_revision_status": model_revision_status,
            "system_fingerprints": sorted(fingerprints),
        },
        "budget": budget,
        "checkpoint": {
            "contract_sha256": contract_sha256,
            "resumed": config.resume,
            "reused_case_count": len(completed_ids),
            "provider_call_case_count": len(pending_cases),
            "path": _CHECKPOINT_FILENAME,
        },
        "scoring": {
            "correct": correct,
            "incorrect": denominator - correct,
            "denominator": denominator,
            "accuracy": accuracy,
            "minimum_required_accuracy": config.minimum_accuracy,
        },
        "incorrect_case_ids": incorrect_ids,
        "selection_issue_case_ids": issue_ids,
        "operational_failure_case_ids": [result.case_id for result in operational],
        "failures": failures,
        "artifacts": {
            "selection_results": _SELECTIONS_FILENAME,
            "checkpoint": _CHECKPOINT_FILENAME,
            "run_manifest": "run-manifest.json",
            "checksum_file": "checksums.sha256",
        },
    }
    artifacts.write("receipt.json", receipt)
    artifacts.write_checksums()
    checksum_problems = artifacts.verify_checksums()
    if checksum_problems:
        receipt["status"] = "BLOCKED"
        receipt["exit_code"] = 2
        receipt["failures"].append(
            {"category": "artifact_checksum", "message": "; ".join(checksum_problems)}
        )
        artifacts.write("receipt.json", receipt)
        artifacts.write_checksums()
        if artifacts.verify_checksums():
            raise RuntimeError("Skill selection artifacts failed checksum verification")
    return receipt


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--manifest", required=True, type=Path)
    parser.add_argument("--dataset", required=True, type=Path)
    parser.add_argument("--artifact-root", default=Path("eval_results"), type=Path)
    parser.add_argument("--run-id", required=True)
    parser.add_argument("--model", required=True)
    parser.add_argument("--max-output-tokens", default=64, type=int)
    parser.add_argument("--timeout", default=120.0, type=float)
    parser.add_argument("--max-concurrency", default=10, type=int)
    parser.add_argument("--minimum-accuracy", default=0.948, type=float)
    parser.add_argument("--required-case-count", default=1_000, type=int)
    parser.add_argument(
        "--resume",
        action="store_true",
        help="resume only from a checkpoint whose full evaluation contract matches",
    )
    return parser


def main(argv: list[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    load_dotenv()
    client = build_default_client()
    if not isinstance(client, OpenAIClient):
        raise TypeError("an OpenAI-compatible provider client is required")
    config = SkillSelectionEvalConfig(
        run_id=args.run_id,
        artifact_root=args.artifact_root,
        manifest_path=args.manifest,
        dataset_path=args.dataset,
        model=args.model,
        max_output_tokens=args.max_output_tokens,
        timeout_s=args.timeout,
        max_concurrency=args.max_concurrency,
        minimum_accuracy=args.minimum_accuracy,
        required_case_count=args.required_case_count,
        resume=args.resume,
    )
    receipt = asyncio.run(run_skill_selection_eval(config, client))
    print(
        json.dumps(
            {
                "status": receipt["status"],
                "run_id": receipt["run_id"],
                "correct": receipt["scoring"]["correct"],
                "denominator": receipt["scoring"]["denominator"],
                "accuracy": receipt["scoring"]["accuracy"],
            },
            ensure_ascii=False,
        )
    )
    return int(receipt["exit_code"])


if __name__ == "__main__":
    raise SystemExit(main())
