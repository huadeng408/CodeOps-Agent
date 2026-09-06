from __future__ import annotations

import json
import os
import subprocess
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

import pytest

from eval.harness.artifacts import RunArtifacts
from eval.harness.context_token_eval import load_context_token_task


def _provider_server(
    *,
    provider: str = "openai",
    input_token_values: tuple[int, int] = (1_000, 200),
    cached_input_token_values: tuple[int, int] = (0, 0),
    output_values: tuple[dict[str, object], dict[str, object]] | None = None,
    reported_models: tuple[str, str] = ("locked-model", "locked-model"),
    fail_from_request: int | None = None,
):
    requests: list[dict[str, object]] = []
    input_tokens = iter(input_token_values)
    cached_input_tokens = iter(cached_input_token_values)
    outputs = iter(
        output_values
        or (
            {"executor_class": "ProviderWorkerExecutor"},
            {"executor_class": "ProviderWorkerExecutor"},
        )
    )
    models = iter(reported_models)

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self) -> None:
            length = int(self.headers.get("Content-Length", "0"))
            payload = json.loads(self.rfile.read(length).decode("utf-8"))
            requests.append(payload)
            if fail_from_request is not None and len(requests) >= fail_from_request:
                body = b'{"error":{"message":"provider unavailable"}}'
                self.send_response(503)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)
                return
            if provider == "anthropic":
                response_payload = {
                    "id": f"message-{len(requests)}",
                    "type": "message",
                    "role": "assistant",
                    "model": next(models),
                    "content": [
                        {"type": "text", "text": json.dumps(next(outputs))}
                    ],
                    "usage": {
                        "input_tokens": next(input_tokens),
                        "cache_read_input_tokens": next(cached_input_tokens),
                        "output_tokens": 8,
                    },
                }
            else:
                response_payload = {
                    "id": f"response-{len(requests)}",
                    "model": next(models),
                    "system_fingerprint": "revision-1",
                    "choices": [
                        {
                            "message": {
                                "role": "assistant",
                                "content": json.dumps(next(outputs)),
                            }
                        }
                    ],
                    "usage": {
                        "prompt_tokens": next(input_tokens),
                        "completion_tokens": 8,
                    },
                }
            body = json.dumps(response_payload).encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, format: str, *args) -> None:
            return

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    return server, requests


def _write_task(
    root: Path,
    *,
    corpus_paths: list[str] | None = None,
    expected: dict[str, object] | None = None,
) -> tuple[Path, Path, str]:
    project_root = root / "repo"
    project_root.mkdir()
    (project_root / "target.py").write_text(
        "class ProviderWorkerExecutor:\n    pass\n", encoding="utf-8"
    )
    (project_root / "noise.py").write_text(
        "noise = 'irrelevant'\n" * 2_000, encoding="utf-8"
    )
    task_path = root / "task.json"
    question = "Return the executor class as one JSON object."
    task_path.write_text(
        json.dumps(
            {
                "schema_version": 1,
                "task_id": "locked-context-task-v1",
                "question": question,
                "corpus_paths": corpus_paths or ["noise.py", "target.py"],
                "p3_paths": ["target.py"],
                "expected": expected or {"executor_class": "ProviderWorkerExecutor"},
            }
        ),
        encoding="utf-8",
    )
    return project_root, task_path, question


def _run_cli(
    tmp_path: Path,
    server: ThreadingHTTPServer,
    *,
    provider: str = "openai",
    run_id: str,
    expected: dict[str, object] | None = None,
) -> subprocess.CompletedProcess[str]:
    project_root, task_path, _ = _write_task(tmp_path, expected=expected)
    env = os.environ.copy()
    if provider == "anthropic":
        env.update(
            {
                "LLM_PROVIDER": "anthropic",
                "ANTHROPIC_API_KEY": "",
                "ANTHROPIC_AUTH_TOKEN": "test-auth-token",
                "ANTHROPIC_BASE_URL": f"http://127.0.0.1:{server.server_port}",
                "ANTHROPIC_MODEL": "locked-model",
                "ANTHROPIC_TIMEOUT": "5",
                "ANTHROPIC_MAX_RETRIES": "0",
            }
        )
    else:
        env.update(
            {
                "LLM_PROVIDER": "openai",
                "OPENAI_API_KEY": "test-key",
                "OPENAI_BASE_URL": f"http://127.0.0.1:{server.server_port}",
                "OPENAI_MODEL": "locked-model",
                "OPENAI_TIMEOUT": "5",
                "OPENAI_MAX_RETRIES": "0",
            }
        )
    return subprocess.run(
        [
            sys.executable,
            "-m",
            "eval.harness.context_token_eval",
            "--task",
            str(task_path),
            "--project-root",
            str(project_root),
            "--artifact-root",
            str(tmp_path / "eval_results"),
            "--run-id",
            run_id,
            "--model",
            "locked-model",
            "--max-output-tokens",
            "128",
            "--minimum-reduction",
            "0.60",
        ],
        cwd=Path(__file__).resolve().parents[2],
        env=env,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        timeout=30,
        check=False,
    )


def test_cli_accepts_anthropic_client(monkeypatch, tmp_path: Path, capsys) -> None:
    """The context lane is provider-neutral when a client implements LLMClient."""
    from eval.harness import context_token_eval as mod
    from orchestrator.llm.providers import AnthropicClient

    client = AnthropicClient(
        api_key="auth-token-fixture",
        base_url="https://relay.example/anthropic",
        model="relay-model",
    )
    observed: dict[str, object] = {}

    async def fake_run(config, passed_client):
        observed["client"] = passed_client
        return {
            "status": "SMOKE_PASS",
            "run_id": config.run_id,
            "comparison": {"input_token_reduction": 0.0},
            "exit_code": 0,
        }

    monkeypatch.setattr(mod, "build_default_client", lambda: client)
    monkeypatch.setattr(mod, "run_context_token_eval", fake_run)

    rc = mod.main(
        [
            "--task",
            str(tmp_path / "task.json"),
            "--run-id",
            "context-anthropic",
            "--model",
            "relay-model",
        ]
    )

    assert rc == 0
    assert observed["client"] is client
    assert "context-anthropic" in capsys.readouterr().out


def test_cli_runs_anthropic_provider_end_to_end(tmp_path: Path) -> None:
    server, requests = _provider_server(provider="anthropic")
    try:
        completed = _run_cli(
            tmp_path,
            server,
            provider="anthropic",
            run_id="context-token-anthropic",
        )
    finally:
        server.shutdown()
        server.server_close()

    assert completed.returncode == 0, completed.stderr
    assert len(requests) == 2
    assert {request["model"] for request in requests} == {"locked-model"}
    assert all(request["messages"][0]["role"] == "user" for request in requests)
    assert "noise = 'irrelevant'" in str(requests[0]["messages"][0]["content"])
    assert "noise = 'irrelevant'" not in str(
        requests[1]["messages"][0]["content"]
    )

    receipt = json.loads(
        (
            tmp_path
            / "eval_results"
            / "context-token-anthropic"
            / "receipt.json"
        ).read_text(encoding="utf-8")
    )
    assert receipt["status"] == "SMOKE_PASS"
    assert receipt["evidence_scope"] == "LOCAL_PROVIDER_INTEGRATION"
    assert receipt["arms"]["baseline"]["provider_input_tokens"] == 1_000
    assert receipt["arms"]["layered"]["provider_input_tokens"] == 200
    assert receipt["provider"]["model_revision_status"] == "MODEL_IDENTITY_UNVERIFIED"


def test_cli_compares_provider_reported_tokens_with_locked_inputs(
    tmp_path: Path,
) -> None:
    question = "Return the executor class as one JSON object."
    server, requests = _provider_server()
    try:
        completed = _run_cli(tmp_path, server, run_id="context-token-test")
    finally:
        server.shutdown()
        server.server_close()

    assert completed.returncode == 0, completed.stderr
    assert len(requests) == 2
    assert {request["model"] for request in requests} == {"locked-model"}
    assert {request["max_tokens"] for request in requests} == {128}
    assert {request["temperature"] for request in requests} == {0.0}
    user_prompts = [str(request["messages"][1]["content"]) for request in requests]
    assert all(prompt.startswith(question) for prompt in user_prompts)
    assert all('"expected"' not in prompt for prompt in user_prompts)
    assert "noise = 'irrelevant'" in user_prompts[0]
    assert "noise = 'irrelevant'" not in user_prompts[1]
    assert "P0 directory summary" in user_prompts[1]
    assert "P1 node summaries" in user_prompts[1]

    run_root = tmp_path / "eval_results" / "context-token-test"
    receipt = json.loads((run_root / "receipt.json").read_text(encoding="utf-8"))
    assert receipt["status"] == "SMOKE_PASS"
    assert receipt["evidence_scope"] == "LOCAL_PROVIDER_INTEGRATION"
    assert receipt["arms"]["baseline"]["provider_input_tokens"] == 1_000
    assert receipt["arms"]["layered"]["provider_input_tokens"] == 200
    assert receipt["comparison"]["input_token_reduction"] == 0.8
    assert receipt["comparison"]["outcome_regressed"] is False
    assert receipt["data_pin"]["task_sha256"]
    assert receipt["data_pin"]["corpus_sha256"]
    assert receipt["prompt_pins"]["task_sha256"]
    serialized_receipt = json.dumps(receipt, sort_keys=True)
    assert '"expected"' not in serialized_receipt
    assert "ProviderWorkerExecutor" not in serialized_receipt
    assert "test-key" not in serialized_receipt
    assert (run_root / "checksums.sha256").is_file()
    assert (
        RunArtifacts("context-token-test", tmp_path / "eval_results").verify_checksums()
        == []
    )


def test_context_comparison_counts_cached_input_tokens(tmp_path: Path) -> None:
    """Provider cache hits remain part of the context sent to the model."""
    server, _ = _provider_server(
        provider="anthropic",
        input_token_values=(142, 708),
        cached_input_token_values=(21_248, 0),
    )
    try:
        completed = _run_cli(
            tmp_path,
            server,
            provider="anthropic",
            run_id="context-token-cached-input",
        )
    finally:
        server.shutdown()
        server.server_close()

    assert completed.returncode == 0, completed.stderr
    receipt = json.loads(
        (
            tmp_path
            / "eval_results"
            / "context-token-cached-input"
            / "receipt.json"
        ).read_text(encoding="utf-8")
    )
    assert receipt["arms"]["baseline"]["provider_input_tokens"] == 142
    assert receipt["arms"]["baseline"]["cached_input_tokens"] == 21_248
    assert receipt["comparison"]["baseline_input_token_denominator"] == 21_390
    assert receipt["comparison"]["layered_input_token_denominator"] == 708
    assert receipt["comparison"]["input_token_reduction"] == pytest.approx(
        (21_390 - 708) / 21_390
    )


def test_layered_outcome_regression_blocks_high_reduction(tmp_path: Path) -> None:
    server, _ = _provider_server(
        output_values=(
            {"executor_class": "ProviderWorkerExecutor"},
            {"executor_class": "WrongExecutor"},
        )
    )
    try:
        completed = _run_cli(tmp_path, server, run_id="context-token-regression")
    finally:
        server.shutdown()
        server.server_close()

    assert completed.returncode == 2
    receipt = json.loads(
        (
            tmp_path / "eval_results" / "context-token-regression" / "receipt.json"
        ).read_text(encoding="utf-8")
    )
    assert receipt["status"] == "BLOCKED"
    assert receipt["comparison"]["input_token_reduction"] == 0.8
    assert receipt["comparison"]["outcome_regressed"] is True
    assert "outcome" in {failure["category"] for failure in receipt["failures"]}


def test_missing_provider_usage_blocks_even_when_outputs_pass(tmp_path: Path) -> None:
    server, _ = _provider_server(input_token_values=(1_000, 0))
    try:
        completed = _run_cli(tmp_path, server, run_id="context-token-no-usage")
    finally:
        server.shutdown()
        server.server_close()

    assert completed.returncode == 2
    receipt = json.loads(
        (
            tmp_path / "eval_results" / "context-token-no-usage" / "receipt.json"
        ).read_text(encoding="utf-8")
    )
    assert receipt["status"] == "BLOCKED"
    assert "provider_usage" in {failure["category"] for failure in receipt["failures"]}


def test_provider_model_mismatch_blocks_comparison(tmp_path: Path) -> None:
    server, _ = _provider_server(reported_models=("locked-model", "other-model"))
    try:
        completed = _run_cli(tmp_path, server, run_id="context-token-model-mismatch")
    finally:
        server.shutdown()
        server.server_close()

    assert completed.returncode == 2
    receipt = json.loads(
        (
            tmp_path / "eval_results" / "context-token-model-mismatch" / "receipt.json"
        ).read_text(encoding="utf-8")
    )
    assert "model_identity" in {failure["category"] for failure in receipt["failures"]}


def test_task_rejects_corpus_path_traversal(tmp_path: Path) -> None:
    _, task_path, _ = _write_task(tmp_path, corpus_paths=["../secret.py", "target.py"])

    with pytest.raises(ValueError, match="unsafe corpus path"):
        load_context_token_task(task_path)


def test_scorer_accepts_only_locked_semantic_alternatives(tmp_path: Path) -> None:
    server, _ = _provider_server(
        output_values=(
            {
                "executor_class": "ProviderWorkerExecutor",
                "completion_factory": "orchestrator.workflows.models.WorkerResult.completed",
            },
            {
                "executor_class": "ProviderWorkerExecutor",
                "completion_factory": "WorkerResult.completed",
            },
        )
    )
    expected = {
        "executor_class": "ProviderWorkerExecutor",
        "completion_factory": [
            "WorkerResult.completed",
            "orchestrator.workflows.models.WorkerResult.completed",
        ],
    }
    try:
        completed = _run_cli(
            tmp_path,
            server,
            run_id="context-token-alternatives",
            expected=expected,
        )
    finally:
        server.shutdown()
        server.server_close()

    assert completed.returncode == 0, completed.stderr
    receipt = json.loads(
        (
            tmp_path / "eval_results" / "context-token-alternatives" / "receipt.json"
        ).read_text(encoding="utf-8")
    )
    assert receipt["exit_code"] == completed.returncode
    assert receipt["arms"]["baseline"]["outcome_passed"] is True
    assert receipt["arms"]["layered"]["outcome_passed"] is True


def test_provider_failure_still_finalizes_blocked_receipt(tmp_path: Path) -> None:
    server, _ = _provider_server(fail_from_request=2)
    try:
        completed = _run_cli(tmp_path, server, run_id="context-token-provider-error")
    finally:
        server.shutdown()
        server.server_close()

    assert completed.returncode == 2
    run_root = tmp_path / "eval_results" / "context-token-provider-error"
    receipt = json.loads((run_root / "receipt.json").read_text(encoding="utf-8"))
    assert receipt["exit_code"] == completed.returncode
    assert receipt["status"] == "BLOCKED"
    assert receipt["arms"]["baseline"]["call_completed"] is True
    assert receipt["arms"]["layered"]["call_completed"] is False
    assert "provider_call" in {failure["category"] for failure in receipt["failures"]}
    assert (
        RunArtifacts(
            "context-token-provider-error", tmp_path / "eval_results"
        ).verify_checksums()
        == []
    )
