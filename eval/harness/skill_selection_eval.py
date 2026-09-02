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


def _evidence_scope(client: OpenAIClient) -> tuple[str, str, dict[str, str]]:
    parsed = urlparse(client.base_url)
    scheme = parsed.scheme.lower()
    host = (parsed.hostname or "").lower()
    endpoint = {"scheme": scheme, "host": host}
    if scheme in {"http", "https"} and host in _LOOPBACK_HOSTS:
        return "LOCAL_PROVIDER_INTEGRATION", "", endpoint
    if scheme == "https" and host:
        return "REMOTE_PROVIDER_TOOL_SELECTION", "", endpoint
    return (
        "UNTRUSTED_PROVIDER_TRANSPORT",
        "formal Skill selection evidence requires remote HTTPS or loopback smoke",
        endpoint,
    )


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
                failure_message=redact_credential_text(str(exc))[:400],
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
    if any(artifacts.root.iterdir()):
        raise ValueError("run artifact directory must be empty")
    evaluation_client = replace(
        client,
        model=config.model,
        max_tokens=config.max_output_tokens,
    )
    scope, transport_failure, endpoint = _evidence_scope(evaluation_client)
    trace_id = uuid.uuid4().hex
    run_source_pin = source_pin(Path(__file__).resolve().parents[2])
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
            "budget": {
                "max_output_tokens_per_case": config.max_output_tokens,
                "timeout_seconds_per_case": config.timeout_s,
                "max_concurrency": config.max_concurrency,
                "calls": len(dataset.cases),
                "max_provider_attempts_per_case": evaluation_client.max_retries + 1,
                "temperature": 0.0,
            },
        }
    )
    artifacts.write_environment()

    semaphore = asyncio.Semaphore(config.max_concurrency)
    results = await asyncio.gather(
        *(
            _run_case(evaluation_client, config, catalog, case, semaphore)
            for case in dataset.cases
        )
    )
    for result in results:
        artifacts.append_line("selections.jsonl", result.artifact_value())

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
        "prompt_pins": {
            "system_sha256": _sha256_text(_SYSTEM_PROMPT),
            "tool_schema_sha256": _sha256_bytes(
                json.dumps(
                    catalog.tool_schema,
                    sort_keys=True,
                    separators=(",", ":"),
                ).encode("utf-8")
            ),
        },
        "provider": {
            "requested_model": config.model,
            "endpoint": endpoint,
            "model_revision_status": model_revision_status,
            "system_fingerprints": sorted(fingerprints),
        },
        "budget": {
            "max_output_tokens_per_case": config.max_output_tokens,
            "timeout_seconds_per_case": config.timeout_s,
            "max_concurrency": config.max_concurrency,
            "calls": denominator,
            "max_provider_attempts_per_case": evaluation_client.max_retries + 1,
            "temperature": 0.0,
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
            "selection_results": "selections.jsonl",
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
