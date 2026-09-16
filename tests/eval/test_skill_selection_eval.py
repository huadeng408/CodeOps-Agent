from __future__ import annotations

import asyncio
import copy
import hashlib
import json
import os
import sqlite3
import subprocess
import sys
import threading
import time
from collections import Counter
from contextlib import closing
from dataclasses import replace
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

import pytest

from eval.harness.artifacts import RunArtifacts
import eval.harness.skill_selection_eval as skill_selection_module
from eval.harness.skill_selection_eval import (
    SkillSelectionEvalConfig,
    _load_catalog,
    _run_case,
    _selection_system_prompt,
    load_skill_selection_dataset,
    run_skill_selection_eval,
)
from orchestrator.llm import ChatResponse, ToolCall, Usage
from orchestrator.llm.providers.anthropic import AnthropicClient
from orchestrator.llm.providers.openai import OpenAIClient


def _pin_dataset(path: Path) -> None:
    digest = hashlib.sha256(path.read_bytes()).hexdigest()
    path.with_suffix(path.suffix + ".sha256").write_text(
        f"{digest}  {path.name}\n",
        encoding="ascii",
    )


def _write_inputs(
    root: Path,
    *,
    requests_per_skill: int = 2,
    templates: list[str] | None = None,
) -> tuple[Path, Path]:
    agent_dir = root / ".agent"
    agent_dir.mkdir(parents=True)
    manifest_path = agent_dir / "skills.json"
    manifest_path.write_text(
        json.dumps(
            {
                "skills": [
                    {
                        "name": "debug",
                        "description": "diagnose a reproducible failure",
                        "tools": ["Read", "Bash"],
                    },
                    {
                        "name": "inspect",
                        "description": "inspect repository structure and relevant files",
                        "tools": ["Read", "Glob"],
                    },
                ]
            }
        ),
        encoding="utf-8",
    )
    prompts = templates or ["{request}"]
    skill_requests = {
        "debug": [
            f"Find why reproducible command {index} crashes."
            for index in range(requests_per_skill)
        ],
        "inspect": [
            f"Locate relevant files for unfamiliar request {index}."
            for index in range(requests_per_skill)
        ],
    }
    dataset_path = root / "skill-selection.json"
    dataset_path.write_text(
        json.dumps(
            {
                "schema_version": 1,
                "dataset_id": "skill-selection-test-v1",
                "license": "CC0-1.0",
                "provenance": "repository-authored test fixture",
                "expected_case_count": 2 * requests_per_skill * len(prompts),
                "prompt_templates": prompts,
                "skills": [
                    {"name": name, "requests": requests}
                    for name, requests in skill_requests.items()
                ],
            }
        ),
        encoding="utf-8",
    )
    _pin_dataset(dataset_path)
    return manifest_path, dataset_path


def _write_formal_inputs(root: Path) -> tuple[Path, Path]:
    agent_dir = root / ".agent"
    agent_dir.mkdir(parents=True)
    manifest_path = agent_dir / "skills.json"
    skills = [
        {
            "name": f"skill-{index:02d}",
            "description": f"route requests for capability {index:02d}",
            "tools": ["Read"],
            "invocation": {"modelInvocable": True, "userInvocable": True},
        }
        for index in range(40)
    ]
    manifest_path.write_text(json.dumps({"skills": skills}), encoding="utf-8")
    dataset_path = root / "skill-selection-formal.json"
    dataset_path.write_text(
        json.dumps(
            {
                "schema_version": 1,
                "dataset_id": "skill-selection-formal-test-v1",
                "license": "CC0-1.0",
                "provenance": "repository-authored formal contract fixture",
                "expected_case_count": 1_000,
                "prompt_templates": ["{request}"],
                "skills": [
                    {
                        "name": skill["name"],
                        "requests": [
                            f"Use capability {index:02d} for case {case:02d}."
                            for case in range(25)
                        ],
                    }
                    for index, skill in enumerate(skills)
                ],
            }
        ),
        encoding="utf-8",
    )
    _pin_dataset(dataset_path)
    return manifest_path, dataset_path


def _write_execution_matrix(
    path: Path,
    manifest_path: Path,
    source: dict[str, object],
) -> dict[str, object]:
    manifest_raw = manifest_path.read_bytes()
    manifest = json.loads(manifest_raw.decode("utf-8"))
    entries: list[dict[str, object]] = []
    for metadata in sorted(manifest["skills"], key=lambda item: item["name"]):
        invocation = metadata.get("invocation", {})
        entry: dict[str, object] = {"name": metadata["name"]}
        if metadata.get("tools"):
            entry["tools"] = metadata["tools"]
        entry["model_invocable"] = invocation.get("modelInvocable", True)
        entry["user_invocable"] = invocation.get("userInvocable", True)
        entry["body_sha256"] = hashlib.sha256(
            f"body:{metadata['name']}".encode("utf-8")
        ).hexdigest()
        entries.append(entry)
    matrix: dict[str, object] = {
        "schema_version": 1,
        "status": "VERIFIED",
        "run_id": "skills-matrix-test",
        "source_pin": source,
        "manifest_sha256": hashlib.sha256(manifest_raw).hexdigest(),
        "catalog_sha256": skill_selection_module._matrix_catalog_sha256(entries),
        "execution_matrix": {
            "denominator": len(entries),
            "passed": len(entries),
            "failures": [],
            "production_loader": True,
            "metadata_only_discovery": True,
            "lazy_body_loads": len(entries),
        },
        "skills": entries,
    }
    path.write_text(json.dumps(matrix, indent=2), encoding="utf-8")
    return matrix


class _ProviderState:
    def __init__(self, *, always_skill: str = "", delay_s: float = 0.0) -> None:
        self.always_skill = always_skill
        self.delay_s = delay_s
        self.lock = threading.Lock()
        self.active = 0
        self.max_active = 0
        self.requests: list[dict[str, object]] = []

    def select(self, prompt: str) -> str:
        if self.always_skill:
            return self.always_skill
        return "debug" if "crashes" in prompt else "inspect"


class _StaticRemoteClient(OpenAIClient):
    async def chat(self, request) -> ChatResponse:
        prompt = request.messages[-1].content
        selected = "debug" if "crashes" in prompt else "inspect"
        return ChatResponse(
            tool_calls=[ToolCall(name="Skill", arguments={"name": selected})],
            usage=Usage(input_tokens=100, output_tokens=5),
            model_identity={
                "reported_model": request.model,
                "response_id": "remote-response",
                "system_fingerprint": f"fp-{selected}",
            },
        )


class _NeverCalledRemoteClient(OpenAIClient):
    calls = 0

    async def chat(self, request) -> ChatResponse:
        type(self).calls += 1
        raise AssertionError("provider must not be called before matrix validation")


class _StaticAnthropicClient(AnthropicClient):
    async def chat(self, request) -> ChatResponse:
        prompt = request.messages[-1].content
        selected = "debug" if "crashes" in prompt else "inspect"
        return ChatResponse(
            tool_calls=[ToolCall(name="Skill", arguments={"name": selected})],
            usage=Usage(input_tokens=100, output_tokens=5),
            model_identity={
                "reported_model": request.model,
                "response_id": "anthropic-response",
                "system_fingerprint": "anthropic-fp",
            },
        )


def _provider_server(
    *,
    always_skill: str = "",
    delay_s: float = 0.0,
    output_tokens: int = 5,
    echo_prompt_error: bool = False,
) -> tuple[ThreadingHTTPServer, _ProviderState]:
    state = _ProviderState(always_skill=always_skill, delay_s=delay_s)

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self) -> None:
            length = int(self.headers.get("Content-Length", "0"))
            payload = json.loads(self.rfile.read(length).decode("utf-8"))
            with state.lock:
                state.active += 1
                state.max_active = max(state.max_active, state.active)
                state.requests.append(payload)
            try:
                if state.delay_s:
                    time.sleep(state.delay_s)
                prompt = payload["messages"][-1]["content"]
                if echo_prompt_error:
                    encoded = json.dumps({"error": prompt}).encode("utf-8")
                    self.send_response(400)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(encoded)))
                    self.end_headers()
                    self.wfile.write(encoded)
                    return
                selected = state.select(prompt)
                body = {
                    "id": f"response-{len(state.requests)}",
                    "model": payload["model"],
                    "choices": [
                        {
                            "message": {
                                "content": "",
                                "tool_calls": [
                                    {
                                        "id": "call-skill",
                                        "type": "function",
                                        "function": {
                                            "name": "Skill",
                                            "arguments": json.dumps({"name": selected}),
                                        },
                                    }
                                ],
                            }
                        }
                    ],
                    "usage": {
                        "prompt_tokens": 100,
                        "completion_tokens": output_tokens,
                    },
                }
                encoded = json.dumps(body).encode("utf-8")
                try:
                    self.send_response(200)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(encoded)))
                    self.end_headers()
                    self.wfile.write(encoded)
                except (BrokenPipeError, ConnectionResetError):
                    pass
            finally:
                with state.lock:
                    state.active -= 1

        def log_message(self, format: str, *args) -> None:
            return

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    return server, state


def _config(
    root: Path,
    manifest_path: Path,
    dataset_path: Path,
    *,
    run_id: str,
    required_case_count: int,
    minimum_accuracy: float = 1.0,
    max_concurrency: int = 2,
    resume: bool = False,
) -> SkillSelectionEvalConfig:
    return SkillSelectionEvalConfig(
        run_id=run_id,
        artifact_root=root / "eval_results",
        manifest_path=manifest_path,
        dataset_path=dataset_path,
        model="locked-model",
        max_output_tokens=32,
        timeout_s=10.0,
        max_concurrency=max_concurrency,
        minimum_accuracy=minimum_accuracy,
        required_case_count=required_case_count,
        resume=resume,
    )


def test_dataset_expands_locked_denominator_and_requires_catalog_coverage(
    tmp_path: Path,
) -> None:
    manifest_path, dataset_path = _write_inputs(
        tmp_path,
        requests_per_skill=2,
        templates=["Please {request}", "Repository task: {request}"],
    )

    dataset = load_skill_selection_dataset(dataset_path, manifest_path)

    assert dataset.dataset_id == "skill-selection-test-v1"
    assert dataset.license == "CC0-1.0"
    assert len(dataset.cases) == 8
    assert len({case.case_id for case in dataset.cases}) == 8
    assert {case.expected_skill for case in dataset.cases} == {"debug", "inspect"}
    assert all("expected_skill" not in case.prompt for case in dataset.cases)


def test_skill_tool_schema_enumerates_registered_names(tmp_path: Path) -> None:
    manifest_path, _ = _write_inputs(tmp_path)

    catalog = _load_catalog(manifest_path)

    assert catalog.tool_schema["function"]["parameters"]["properties"]["name"]["enum"] == [
        "debug",
        "inspect",
    ]


def test_skill_selection_prompt_exposes_catalog_as_structured_metadata(
    tmp_path: Path,
) -> None:
    manifest_path, _ = _write_inputs(tmp_path)

    prompt = _selection_system_prompt(_load_catalog(manifest_path))

    assert "- debug: diagnose a reproducible failure" in prompt
    assert "- inspect: inspect repository structure and relevant files" in prompt
    assert "expected_skill" not in prompt


def test_execution_matrix_accepts_source_bound_40_skill_catalog(tmp_path: Path) -> None:
    manifest_path, _ = _write_formal_inputs(tmp_path)
    repository_root = Path(__file__).resolve().parents[2]
    expected_source = skill_selection_module.source_pin(repository_root)
    matrix_path = tmp_path / "skills-matrix.json"
    _write_execution_matrix(matrix_path, manifest_path, expected_source)

    summary, payload = skill_selection_module._load_execution_matrix(
        matrix_path,
        _load_catalog(manifest_path),
        expected_source,
    )

    assert summary["denominator"] == 40
    assert summary["passed"] == 40
    assert summary["lazy_body_loads"] == 40
    assert payload["status"] == "VERIFIED"


def test_go_execution_matrix_is_accepted_by_python_verifier(tmp_path: Path) -> None:
    repository_root = Path(__file__).resolve().parents[2]
    manifest_path = tmp_path / ".agent" / "skills.json"
    matrix_path = tmp_path / "skills-matrix.json"

    result = subprocess.run(
        [
            "go",
            "run",
            "./cmd/skills-manifest",
            "--output",
            str(manifest_path),
            "--matrix-output",
            str(matrix_path),
            "--repo-root",
            str(repository_root),
            "--run-id",
            "skills-matrix-cross-language",
        ],
        cwd=repository_root,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        check=False,
    )
    assert result.returncode == 0, result.stderr
    expected_source = skill_selection_module.source_pin(repository_root)

    summary, _ = skill_selection_module._load_execution_matrix(
        matrix_path,
        _load_catalog(manifest_path),
        expected_source,
    )

    assert summary["denominator"] >= 40
    assert summary["catalog_sha256"]


def test_execution_matrix_rejects_tampered_pins_and_entries(tmp_path: Path) -> None:
    manifest_path, _ = _write_formal_inputs(tmp_path)
    repository_root = Path(__file__).resolve().parents[2]
    expected_source = skill_selection_module.source_pin(repository_root)
    matrix_path = tmp_path / "skills-matrix.json"
    original = _write_execution_matrix(matrix_path, manifest_path, expected_source)
    catalog = _load_catalog(manifest_path)

    for mutation in ("source", "manifest", "name", "body"):
        payload = copy.deepcopy(original)
        if mutation == "source":
            payload["source_pin"]["dirty_hash"] = "0" * 64
        elif mutation == "manifest":
            payload["manifest_sha256"] = "0" * 64
        elif mutation == "name":
            payload["skills"][0]["name"] = "different-skill"
        else:
            payload["skills"][0]["body_sha256"] = "0" * 64
        matrix_path.write_text(json.dumps(payload), encoding="utf-8")

        with pytest.raises(ValueError):
            skill_selection_module._load_execution_matrix(
                matrix_path,
                catalog,
                expected_source,
            )


@pytest.mark.asyncio
async def test_formal_remote_eval_requires_matrix_before_provider_call(
    tmp_path: Path,
) -> None:
    manifest_path, dataset_path = _write_formal_inputs(tmp_path)
    client = _NeverCalledRemoteClient(
        api_key="test-key",
        base_url="https://provider.example/v1",
        model="locked-model",
        max_retries=0,
    )
    _NeverCalledRemoteClient.calls = 0

    with pytest.raises(ValueError, match="production execution matrix"):
        await run_skill_selection_eval(
            _config(
                tmp_path,
                manifest_path,
                dataset_path,
                run_id="skill-selection-formal-without-matrix",
                required_case_count=1_000,
                minimum_accuracy=0.948,
            ),
            client,
        )

    assert _NeverCalledRemoteClient.calls == 0


@pytest.mark.asyncio
async def test_deepseek_selection_enables_thinking_without_forced_tool_choice(
    tmp_path: Path,
) -> None:
    manifest_path, dataset_path = _write_inputs(tmp_path)
    dataset = load_skill_selection_dataset(dataset_path, manifest_path)
    server, state = _provider_server()
    client = OpenAIClient(
        api_key="test-key",
        base_url=f"http://127.0.0.1:{server.server_port}/v1",
        model="deepseek-v4-pro",
        timeout=10.0,
        max_retries=0,
    )
    try:
        result = await _run_case(
            client,
            replace(
                _config(
                    tmp_path,
                    manifest_path,
                    dataset_path,
                    run_id="skill-selection-deepseek-wire",
                    required_case_count=4,
                ),
                model="deepseek-v4-pro",
            ),
            _load_catalog(manifest_path),
            dataset.cases[0],
            asyncio.Semaphore(1),
        )
    finally:
        server.shutdown()
        server.server_close()

    assert result.call_completed is True
    request = state.requests[0]
    assert request["thinking"] == {"type": "enabled"}
    assert "tool_choice" not in request
    assert "parallel_tool_calls" not in request


def test_cli_accepts_anthropic_compatible_client(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    manifest_path, dataset_path = _write_inputs(tmp_path)
    client = _StaticAnthropicClient(
        api_key="test-key",
        base_url="http://127.0.0.1/v1",
        model="locked-model",
        max_retries=0,
    )
    monkeypatch.setattr(
        skill_selection_module, "build_default_client", lambda: client
    )

    exit_code = skill_selection_module.main(
        [
            "--manifest",
            str(manifest_path),
            "--dataset",
            str(dataset_path),
            "--artifact-root",
            str(tmp_path / "eval_results"),
            "--run-id",
            "skill-selection-anthropic-cli",
            "--model",
            "locked-model",
            "--required-case-count",
            "4",
        ]
    )

    assert exit_code == 0
    receipt = json.loads(
        (
            tmp_path
            / "eval_results"
            / "skill-selection-anthropic-cli"
            / "receipt.json"
        ).read_text(encoding="utf-8")
    )
    assert receipt["status"] == "SMOKE_PASS"

    payload = json.loads(dataset_path.read_text(encoding="utf-8"))
    payload["skills"] = payload["skills"][:1]
    payload["expected_case_count"] = 4
    dataset_path.write_text(json.dumps(payload), encoding="utf-8")
    _pin_dataset(dataset_path)
    with pytest.raises(ValueError, match="catalog coverage"):
        load_skill_selection_dataset(dataset_path, manifest_path)


def test_dataset_rejects_checksum_sidecar_mismatch(tmp_path: Path) -> None:
    manifest_path, dataset_path = _write_inputs(tmp_path)
    dataset_path.with_suffix(".json.sha256").write_text(
        f"{'0' * 64}  {dataset_path.name}\n",
        encoding="ascii",
    )

    with pytest.raises(ValueError, match="checksum sidecar"):
        load_skill_selection_dataset(dataset_path, manifest_path)


@pytest.mark.asyncio
async def test_provider_tool_selection_writes_gold_free_smoke_receipt(
    tmp_path: Path,
) -> None:
    manifest_path, dataset_path = _write_inputs(tmp_path)
    server, state = _provider_server()
    client = OpenAIClient(
        api_key="test-key",
        base_url=f"http://127.0.0.1:{server.server_port}/v1",
        model="locked-model",
        timeout=10.0,
        max_retries=0,
    )
    try:
        receipt = await run_skill_selection_eval(
            _config(
                tmp_path,
                manifest_path,
                dataset_path,
                run_id="skill-selection-smoke",
                required_case_count=4,
            ),
            client,
        )
    finally:
        server.shutdown()
        server.server_close()

    assert receipt["status"] == "SMOKE_PASS"
    assert receipt["exit_code"] == 0
    assert receipt["scoring"] == {
        "correct": 4,
        "incorrect": 0,
        "denominator": 4,
        "accuracy": 1.0,
        "minimum_required_accuracy": 1.0,
    }
    assert receipt["catalog_pin"]["skill_count"] == 2
    assert receipt["budget"]["max_provider_attempts_per_case"] == 1
    assert receipt["incorrect_case_ids"] == []
    serialized = json.dumps(receipt, sort_keys=True)
    assert '"expected_skill"' not in serialized
    assert "Find why reproducible" not in serialized
    assert "test-key" not in serialized
    for request in state.requests:
        user_message = request["messages"][-1]
        assert set(user_message) == {"role", "content"}
        assert "expected_skill" not in user_message["content"]
        assert request["tool_choice"] == {
            "type": "function",
            "function": {"name": "Skill"},
        }
        assert request["parallel_tool_calls"] is False
    results_text = (
        tmp_path / "eval_results" / "skill-selection-smoke" / "selections.jsonl"
    ).read_text(encoding="utf-8")
    assert '"expected_skill"' not in results_text
    assert (
        RunArtifacts(
            "skill-selection-smoke", tmp_path / "eval_results"
        ).verify_checksums()
        == []
    )


@pytest.mark.asyncio
async def test_execution_matrix_receipt_pins_archived_bytes(tmp_path: Path) -> None:
    manifest_path, dataset_path = _write_inputs(tmp_path)
    repository_root = Path(__file__).resolve().parents[2]
    matrix_path = tmp_path / "skills-matrix.json"
    _write_execution_matrix(
        matrix_path,
        manifest_path,
        skill_selection_module.source_pin(repository_root),
    )
    matrix_payload = json.loads(matrix_path.read_text(encoding="utf-8"))
    matrix_payload["run_id"] = "sk-1234567890"
    matrix_path.write_text(json.dumps(matrix_payload), encoding="utf-8")
    server, _ = _provider_server()
    client = OpenAIClient(
        api_key="test-key",
        base_url=f"http://127.0.0.1:{server.server_port}/v1",
        model="locked-model",
        timeout=10.0,
        max_retries=0,
    )
    config = replace(
        _config(
            tmp_path,
            manifest_path,
            dataset_path,
            run_id="skill-selection-matrix-artifact",
            required_case_count=4,
        ),
        execution_matrix_path=matrix_path,
    )
    try:
        receipt = await run_skill_selection_eval(config, client)
        resumed = await run_skill_selection_eval(replace(config, resume=True), client)
    finally:
        server.shutdown()
        server.server_close()

    archived = (
        tmp_path
        / "eval_results"
        / "skill-selection-matrix-artifact"
        / "skill-execution-matrix.json"
    )
    assert receipt["execution_matrix"]["artifact_sha256"] == hashlib.sha256(
        archived.read_bytes()
    ).hexdigest()
    assert receipt["raw_evidence"]["execution_matrix_sha256"] == receipt[
        "execution_matrix"
    ]["artifact_sha256"]
    assert receipt["raw_evidence"]["execution_matrix_catalog_sha256"] == receipt[
        "execution_matrix"
    ]["catalog_sha256"]
    assert receipt["raw_evidence"]["checkpoint_sha256"] == hashlib.sha256(
        (tmp_path / "eval_results" / config.run_id / "checkpoint.sqlite3").read_bytes()
    ).hexdigest()
    assert receipt["raw_evidence"]["selection_results_sha256"] == hashlib.sha256(
        (tmp_path / "eval_results" / config.run_id / "selections.jsonl").read_bytes()
    ).hexdigest()
    assert receipt["raw_evidence"]["run_manifest_sha256"] == hashlib.sha256(
        (tmp_path / "eval_results" / config.run_id / "run-manifest.json").read_bytes()
    ).hexdigest()
    assert resumed["execution_matrix"]["artifact_sha256"] == receipt[
        "execution_matrix"
    ]["artifact_sha256"]
    archived_payload = json.loads(archived.read_text(encoding="utf-8"))
    assert "run_id" not in archived_payload
    assert archived_payload["run_id_sha256"] == hashlib.sha256(
        b"sk-1234567890"
    ).hexdigest()
    assert receipt["artifacts"]["execution_matrix"] == archived.name
    assert (
        RunArtifacts(
            "skill-selection-matrix-artifact", tmp_path / "eval_results"
        ).verify_checksums()
        == []
    )


@pytest.mark.asyncio
async def test_accuracy_below_threshold_blocks_without_losing_denominator(
    tmp_path: Path,
) -> None:
    manifest_path, dataset_path = _write_inputs(tmp_path)
    server, _ = _provider_server(always_skill="debug")
    client = OpenAIClient(
        api_key="test-key",
        base_url=f"http://127.0.0.1:{server.server_port}/v1",
        model="locked-model",
        timeout=10.0,
        max_retries=0,
    )
    try:
        receipt = await run_skill_selection_eval(
            _config(
                tmp_path,
                manifest_path,
                dataset_path,
                run_id="skill-selection-below-threshold",
                required_case_count=4,
                minimum_accuracy=0.75,
            ),
            client,
        )
    finally:
        server.shutdown()
        server.server_close()

    assert receipt["status"] == "BLOCKED"
    assert receipt["exit_code"] == 2
    assert receipt["scoring"]["correct"] == 2
    assert receipt["scoring"]["incorrect"] == 2
    assert receipt["scoring"]["denominator"] == 4
    assert len(receipt["incorrect_case_ids"]) == 2
    assert "accuracy" in {failure["category"] for failure in receipt["failures"]}


@pytest.mark.asyncio
async def test_provider_requests_respect_configured_concurrency_cap(
    tmp_path: Path,
) -> None:
    manifest_path, dataset_path = _write_inputs(tmp_path, requests_per_skill=6)
    server, state = _provider_server(delay_s=0.03)
    client = OpenAIClient(
        api_key="test-key",
        base_url=f"http://127.0.0.1:{server.server_port}/v1",
        model="locked-model",
        timeout=10.0,
        max_retries=0,
    )
    try:
        receipt = await run_skill_selection_eval(
            _config(
                tmp_path,
                manifest_path,
                dataset_path,
                run_id="skill-selection-concurrency",
                required_case_count=12,
                max_concurrency=3,
            ),
            client,
        )
    finally:
        server.shutdown()
        server.server_close()

    assert receipt["status"] == "SMOKE_PASS"
    assert state.max_active == 3
    assert receipt["budget"]["max_concurrency"] == 3


@pytest.mark.asyncio
async def test_resume_skips_checkpointed_cases_and_rebuilds_dataset_order(
    tmp_path: Path,
) -> None:
    manifest_path, dataset_path = _write_inputs(tmp_path)
    dataset = load_skill_selection_dataset(dataset_path, manifest_path)
    server, state = _provider_server()
    client = OpenAIClient(
        api_key="test-key",
        base_url=f"http://127.0.0.1:{server.server_port}/v1",
        model="locked-model",
        timeout=10.0,
        max_retries=0,
    )
    try:
        await run_skill_selection_eval(
            _config(
                tmp_path,
                manifest_path,
                dataset_path,
                run_id="skill-selection-resume-complete",
                required_case_count=4,
            ),
            client,
        )
        calls_before_resume = len(state.requests)

        receipt = await run_skill_selection_eval(
            _config(
                tmp_path,
                manifest_path,
                dataset_path,
                run_id="skill-selection-resume-complete",
                required_case_count=4,
                resume=True,
            ),
            client,
        )
    finally:
        server.shutdown()
        server.server_close()

    assert calls_before_resume == 4
    assert len(state.requests) == calls_before_resume
    assert receipt["scoring"]["denominator"] == 4
    assert receipt["checkpoint"] == {
        "contract_sha256": receipt["checkpoint"]["contract_sha256"],
        "resumed": True,
        "reused_case_count": 4,
        "provider_call_case_count": 0,
        "path": "checkpoint.sqlite3",
    }
    result_path = (
        tmp_path
        / "eval_results"
        / "skill-selection-resume-complete"
        / "selections.jsonl"
    )
    result_ids = [
        json.loads(line)["case_id"]
        for line in result_path.read_text(encoding="utf-8").splitlines()
    ]
    assert result_ids == [case.case_id for case in dataset.cases]
    assert (
        RunArtifacts(
            "skill-selection-resume-complete", tmp_path / "eval_results"
        ).verify_checksums()
        == []
    )


@pytest.mark.asyncio
async def test_resume_rejects_changed_budget_before_provider_calls(
    tmp_path: Path,
) -> None:
    manifest_path, dataset_path = _write_inputs(tmp_path)
    server, state = _provider_server()
    client = OpenAIClient(
        api_key="test-key",
        base_url=f"http://127.0.0.1:{server.server_port}/v1",
        model="locked-model",
        timeout=10.0,
        max_retries=0,
    )
    initial = _config(
        tmp_path,
        manifest_path,
        dataset_path,
        run_id="skill-selection-resume-budget",
        required_case_count=4,
    )
    try:
        await run_skill_selection_eval(initial, client)
        artifacts = RunArtifacts(
            "skill-selection-resume-budget", tmp_path / "eval_results"
        )
        checkpoint_path = artifacts.root / "checkpoint.sqlite3"
        checkpoint_sha256 = hashlib.sha256(checkpoint_path.read_bytes()).hexdigest()
        assert artifacts.verify_checksums() == []
        calls_before_resume = len(state.requests)
        with pytest.raises(ValueError, match="contract"):
            await run_skill_selection_eval(
                replace(initial, resume=True, max_output_tokens=16),
                client,
            )
    finally:
        server.shutdown()
        server.server_close()

    assert len(state.requests) == calls_before_resume
    assert hashlib.sha256(checkpoint_path.read_bytes()).hexdigest() == checkpoint_sha256
    assert artifacts.verify_checksums() == []


@pytest.mark.asyncio
async def test_non_resume_still_rejects_nonempty_run_directory(
    tmp_path: Path,
) -> None:
    manifest_path, dataset_path = _write_inputs(tmp_path)
    run_root = tmp_path / "eval_results" / "skill-selection-existing"
    run_root.mkdir(parents=True)
    (run_root / "existing.txt").write_text("do not overwrite", encoding="utf-8")
    server, state = _provider_server()
    client = OpenAIClient(
        api_key="test-key",
        base_url=f"http://127.0.0.1:{server.server_port}/v1",
        model="locked-model",
        timeout=10.0,
        max_retries=0,
    )
    try:
        with pytest.raises(ValueError, match="must be empty"):
            await run_skill_selection_eval(
                _config(
                    tmp_path,
                    manifest_path,
                    dataset_path,
                    run_id="skill-selection-existing",
                    required_case_count=4,
                ),
                client,
            )
    finally:
        server.shutdown()
        server.server_close()

    assert state.requests == []
    assert (run_root / "existing.txt").read_text(encoding="utf-8") == (
        "do not overwrite"
    )


def _checkpoint_case_ids(path: Path) -> set[str]:
    if not path.is_file():
        return set()
    try:
        with closing(
            sqlite3.connect(
                f"file:{path.as_posix()}?mode=ro",
                uri=True,
                timeout=0.1,
            )
        ) as connection:
            return {
                str(row[0]) for row in connection.execute("SELECT case_id FROM results")
            }
    except sqlite3.Error:
        return set()


def test_cli_resumes_after_real_process_termination_without_repeating_commits(
    tmp_path: Path,
) -> None:
    manifest_path, dataset_path = _write_inputs(tmp_path, requests_per_skill=10)
    dataset = load_skill_selection_dataset(dataset_path, manifest_path)
    server, state = _provider_server(delay_s=0.12)
    artifact_root = tmp_path / "eval_results"
    run_id = "skill-selection-process-recovery"
    checkpoint_path = artifact_root / run_id / "checkpoint.sqlite3"
    repo_root = Path(__file__).resolve().parents[2]
    command = [
        sys.executable,
        "-m",
        "eval.harness.skill_selection_eval",
        "--manifest",
        str(manifest_path),
        "--dataset",
        str(dataset_path),
        "--artifact-root",
        str(artifact_root),
        "--run-id",
        run_id,
        "--model",
        "locked-model",
        "--max-output-tokens",
        "32",
        "--timeout",
        "10",
        "--max-concurrency",
        "2",
        "--minimum-accuracy",
        "1.0",
        "--required-case-count",
        "20",
    ]
    environment = os.environ.copy()
    environment.update(
        {
            "LLM_PROVIDER": "openai",
            "OPENAI_API_KEY": "test-key",
            "OPENAI_BASE_URL": f"http://127.0.0.1:{server.server_port}/v1",
            "OPENAI_MODEL": "locked-model",
            "OPENAI_MAX_RETRIES": "0",
        }
    )
    process = subprocess.Popen(
        command,
        cwd=repo_root,
        env=environment,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    try:
        deadline = time.monotonic() + 15.0
        committed_before_kill: set[str] = set()
        while time.monotonic() < deadline:
            committed_before_kill = _checkpoint_case_ids(checkpoint_path)
            if len(committed_before_kill) >= 4:
                break
            if process.poll() is not None:
                stdout, stderr = process.communicate()
                pytest.fail(
                    "evaluator exited before the kill point: "
                    f"code={process.returncode}, stdout={stdout!r}, stderr={stderr!r}"
                )
            time.sleep(0.05)
        assert 4 <= len(committed_before_kill) < len(dataset.cases)

        process.terminate()
        process.wait(timeout=10.0)
        time.sleep(0.2)
        committed_before_resume = _checkpoint_case_ids(checkpoint_path)
        assert committed_before_kill <= committed_before_resume
        assert len(committed_before_resume) < len(dataset.cases)
        with state.lock:
            requests_before_resume = list(state.requests)

        resumed = subprocess.run(
            [*command, "--resume"],
            cwd=repo_root,
            env=environment,
            capture_output=True,
            text=True,
            timeout=30.0,
            check=False,
        )
        assert resumed.returncode == 0, resumed.stderr
    finally:
        if process.poll() is None:
            process.terminate()
            process.wait(timeout=10.0)
        server.shutdown()
        server.server_close()

    result_root = artifact_root / run_id
    receipt = json.loads((result_root / "receipt.json").read_text(encoding="utf-8"))
    assert receipt["status"] == "SMOKE_PASS"
    assert receipt["scoring"]["denominator"] == 20
    assert receipt["checkpoint"]["resumed"] is True
    assert receipt["checkpoint"]["reused_case_count"] == len(committed_before_resume)
    assert receipt["checkpoint"]["provider_call_case_count"] == (
        len(dataset.cases) - len(committed_before_resume)
    )

    final_prompts = Counter(
        str(request["messages"][-1]["content"]) for request in state.requests
    )
    initial_prompts = Counter(
        str(request["messages"][-1]["content"]) for request in requests_before_resume
    )
    case_prompts = {case.case_id: case.prompt for case in dataset.cases}
    for case_id in committed_before_resume:
        prompt = case_prompts[case_id]
        assert initial_prompts[prompt] == 1
        assert final_prompts[prompt] == 1

    result_rows = [
        json.loads(line)
        for line in (result_root / "selections.jsonl")
        .read_text(encoding="utf-8")
        .splitlines()
    ]
    assert [row["case_id"] for row in result_rows] == [
        case.case_id for case in dataset.cases
    ]
    with closing(sqlite3.connect(checkpoint_path)) as connection:
        persisted = "\n".join(connection.iterdump())
        assert connection.execute("SELECT COUNT(*) FROM results").fetchone()[0] == 20
        assert connection.execute("PRAGMA journal_mode").fetchone()[0] == "delete"
    assert "expected_skill" not in persisted
    assert "test-key" not in persisted
    assert all(case.prompt not in persisted for case in dataset.cases)
    assert RunArtifacts(run_id, artifact_root).verify_checksums() == []


def test_config_rejects_concurrency_above_global_provider_cap(tmp_path: Path) -> None:
    manifest_path, dataset_path = _write_inputs(tmp_path)
    config = _config(
        tmp_path,
        manifest_path,
        dataset_path,
        run_id="skill-selection-invalid-concurrency",
        required_case_count=4,
        max_concurrency=11,
    )

    with pytest.raises(ValueError, match="between 1 and 10"):
        config.validate()


@pytest.mark.asyncio
async def test_remote_provider_cannot_verify_reduced_formal_contract(
    tmp_path: Path,
) -> None:
    manifest_path, dataset_path = _write_inputs(tmp_path)
    client = _StaticRemoteClient(
        api_key="test-key",
        base_url="https://provider.example/v1",
        model="locked-model",
        max_retries=0,
    )

    receipt = await run_skill_selection_eval(
        _config(
            tmp_path,
            manifest_path,
            dataset_path,
            run_id="skill-selection-reduced-remote",
            required_case_count=4,
        ),
        client,
    )

    assert receipt["status"] == "BLOCKED"
    assert "formal_contract" in {failure["category"] for failure in receipt["failures"]}


@pytest.mark.asyncio
async def test_remote_provider_cannot_verify_mixed_model_revisions(
    tmp_path: Path,
) -> None:
    manifest_path, dataset_path = _write_inputs(tmp_path)
    client = _StaticRemoteClient(
        api_key="test-key",
        base_url="https://provider.example/v1",
        model="locked-model",
        max_retries=0,
    )

    receipt = await run_skill_selection_eval(
        _config(
            tmp_path,
            manifest_path,
            dataset_path,
            run_id="skill-selection-mixed-revisions",
            required_case_count=4,
        ),
        client,
    )

    assert receipt["status"] == "BLOCKED"
    assert receipt["provider"]["model_revision_status"] == ("MODEL_IDENTITY_UNVERIFIED")
    assert receipt["provider"]["system_fingerprints"] == ["fp-debug", "fp-inspect"]
    assert "model_revision" in {failure["category"] for failure in receipt["failures"]}


@pytest.mark.asyncio
async def test_provider_output_over_budget_blocks_all_affected_cases(
    tmp_path: Path,
) -> None:
    manifest_path, dataset_path = _write_inputs(tmp_path)
    server, _ = _provider_server(output_tokens=33)
    client = OpenAIClient(
        api_key="test-key",
        base_url=f"http://127.0.0.1:{server.server_port}/v1",
        model="locked-model",
        timeout=10.0,
        max_retries=0,
    )
    try:
        receipt = await run_skill_selection_eval(
            _config(
                tmp_path,
                manifest_path,
                dataset_path,
                run_id="skill-selection-output-over-budget",
                required_case_count=4,
            ),
            client,
        )
    finally:
        server.shutdown()
        server.server_close()

    assert receipt["status"] == "BLOCKED"
    assert receipt["operational_failure_case_ids"] == [
        "skill-selection-test-v1-00-00-00",
        "skill-selection-test-v1-00-01-00",
        "skill-selection-test-v1-01-00-00",
        "skill-selection-test-v1-01-01-00",
    ]
    assert "provider_integrity" in {
        failure["category"] for failure in receipt["failures"]
    }


@pytest.mark.asyncio
async def test_checkpoint_does_not_persist_prompt_echoed_in_provider_error(
    tmp_path: Path,
) -> None:
    manifest_path, dataset_path = _write_inputs(tmp_path)
    dataset = load_skill_selection_dataset(dataset_path, manifest_path)
    server, _ = _provider_server(echo_prompt_error=True)
    client = OpenAIClient(
        api_key="test-key",
        base_url=f"http://127.0.0.1:{server.server_port}/v1",
        model="locked-model",
        timeout=10.0,
        max_retries=0,
    )
    try:
        await run_skill_selection_eval(
            _config(
                tmp_path,
                manifest_path,
                dataset_path,
                run_id="skill-selection-prompt-error",
                required_case_count=4,
            ),
            client,
        )
    finally:
        server.shutdown()
        server.server_close()

    checkpoint_path = (
        tmp_path
        / "eval_results"
        / "skill-selection-prompt-error"
        / "checkpoint.sqlite3"
    )
    with closing(sqlite3.connect(checkpoint_path)) as connection:
        persisted = "\n".join(connection.iterdump())
    assert all(case.prompt not in persisted for case in dataset.cases)
