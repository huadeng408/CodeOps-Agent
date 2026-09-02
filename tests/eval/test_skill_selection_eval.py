from __future__ import annotations

import hashlib
import json
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

import pytest

from eval.harness.artifacts import RunArtifacts
from eval.harness.skill_selection_eval import (
    SkillSelectionEvalConfig,
    load_skill_selection_dataset,
    run_skill_selection_eval,
)
from orchestrator.llm import ChatResponse, ToolCall, Usage
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


def _provider_server(
    *,
    always_skill: str = "",
    delay_s: float = 0.0,
    output_tokens: int = 5,
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
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(encoded)))
                self.end_headers()
                self.wfile.write(encoded)
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
