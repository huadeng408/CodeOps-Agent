from __future__ import annotations

import hashlib
import json
import os
import subprocess
import sys
from collections.abc import Mapping, Sequence
from dataclasses import replace
from pathlib import Path
from typing import Any

import pytest

from eval.harness.artifacts import RunArtifacts
from eval.harness.independent_agent_e2e import (
    EMPTY_DIRTY_HASH,
    REQUIRED_CHECKS,
    REQUIRED_SPANS,
    IndependentAgentE2EConfig,
    _subprocess_environment,
    run_independent_agent_e2e,
)
from eval.harness.trace_contract import CapturedSpan
from eval.release_gate import _agent_e2e_predicate

_GIT_SHA = "a" * 40
_TRACE_ID = "b" * 32
_SANDBOX_ENV = {
    "CODE_AGENT_E2E_SANDBOX_BACKEND": "docker",
    "CODE_AGENT_E2E_SANDBOX_IMAGE": "alpine:3.20",
}


def _config(tmp_path: Path, run_id: str = "agent-run") -> IndependentAgentE2EConfig:
    return IndependentAgentE2EConfig(
        run_id=run_id,
        artifact_root=tmp_path / "artifacts",
        repo_root=tmp_path,
        provider="openai",
        phoenix_url="http://127.0.0.1:6006",
        phoenix_project="code-agent",
        trace_readback_attempts=1,
        trace_readback_interval_seconds=0,
    )


def test_unlabeled_otlp_uses_phoenix_default_project(tmp_path: Path) -> None:
    config = IndependentAgentE2EConfig(
        run_id="agent-run",
        artifact_root=tmp_path / "artifacts",
        repo_root=tmp_path,
    )

    assert config.phoenix_project == "default"


@pytest.mark.parametrize("run_id", ["run with spaces", "run,other=value", "x" * 97])
def test_run_id_must_be_safe_for_trace_propagation(
    tmp_path: Path, run_id: str
) -> None:
    config = IndependentAgentE2EConfig(
        run_id=run_id,
        artifact_root=tmp_path / "artifacts",
        repo_root=tmp_path,
    )

    with pytest.raises(ValueError, match="safe trace"):
        config.validate()


def _clean_source_pin(_root: str | Path) -> dict[str, Any]:
    return {
        "git_sha": _GIT_SHA,
        "dirty_hash": EMPTY_DIRTY_HASH,
        "untracked_files": 0,
    }


def _route_event(
    *,
    session_id: str,
    turn: int,
    verified: bool = True,
    provider: str = "openai",
    run_id: str = "agent-run",
    model: str = "gpt-test",
    requested_model: str = "",
) -> dict[str, Any]:
    return {
        "schema_version": 1,
        "phase": "model_after",
        "eval_run_id": run_id,
        "session_id": session_id,
        "turn": turn,
        "provider_backed": True,
        "provider": provider,
        "model": model,
        "generation": 1,
        "model_identity": {
            "requested_model": requested_model or model,
            "reported_model": "gpt-test-2026-09",
            "response_id": f"response-{turn}",
            "system_fingerprint": "fp-build-1" if verified else "",
            "identity_verified": verified,
        },
    }


def _executor(
    *,
    missing_check: str = "",
    verified_identity: bool = True,
    provider_backed: bool = True,
    route_session: str = "",
    route_provider: str = "openai",
    route_run_id: str = "agent-run",
    route_model: str = "gpt-test",
    route_requested_model: str = "",
):
    def execute(
        command: Sequence[str],
        _cwd: Path,
        environ: Mapping[str, str],
        _timeout: float,
    ) -> subprocess.CompletedProcess[str]:
        route_path = Path(
            environ["CODE_AGENT_INDEPENDENT_AGENTS_E2E_PROVIDER_ROUTE_RECEIPT"]
        )
        events = [
            _route_event(
                session_id=route_session or "parent-session",
                turn=1,
                verified=verified_identity,
                provider=route_provider,
                run_id=route_run_id,
                model=route_model,
                requested_model=route_requested_model,
            ),
            _route_event(
                session_id="child-session",
                turn=2,
                verified=verified_identity,
                provider=route_provider,
                run_id=route_run_id,
                model=route_model,
                requested_model=route_requested_model,
            ),
        ]
        route_path.write_text(
            "".join(json.dumps(event) + "\n" for event in events),
            encoding="utf-8",
        )
        checks = {name: True for name in REQUIRED_CHECKS}
        if missing_check:
            checks.pop(missing_check)
        process_receipt = {
            "status": "IMPLEMENTED",
            "run_id": environ["CODE_AGENT_INDEPENDENT_AGENTS_E2E_RUN_ID"],
            "git_sha": _GIT_SHA,
            "exit_code": 0,
            "provider_backed": provider_backed,
            "provider_kind": "openai",
            "provider_call_count": len(events),
            "sandbox_backend": environ["CODE_AGENT_E2E_SANDBOX_BACKEND"],
            "source_dirty": False,
            "provider_route_sha256": hashlib.sha256(
                route_path.read_bytes()
            ).hexdigest(),
            "parent_session_id": "parent-session",
            "child_session_id": "child-session",
            "release_checks": checks,
        }
        Path(environ["CODE_AGENT_INDEPENDENT_AGENTS_E2E_RECEIPT"]).write_text(
            json.dumps(process_receipt) + "\n",
            encoding="utf-8",
        )
        return subprocess.CompletedProcess(command, 0, stdout="ok\n", stderr="")

    return execute


def _spans(
    *,
    missing_span: str = "",
    multiple_traces: bool = False,
    unfinished_span: str = "",
    error_span: str = "",
    child_binding: str = "child-session",
) -> list[CapturedSpan]:
    result: list[CapturedSpan] = []
    ordered = [
        "eval.run",
        "agent.main",
        "agent.subagent",
        *sorted(REQUIRED_SPANS - {"agent.main", "agent.subagent"}),
    ]
    previous_span_id = ""
    for index, name in enumerate(ordered, start=1):
        if name == missing_span:
            continue
        trace_id = "c" * 32 if multiple_traces and index == 1 else _TRACE_ID
        if name == "eval.run":
            instance_id = ""
        elif name == "agent.main":
            instance_id = "parent-session"
        else:
            instance_id = child_binding
        span_id = f"{index:016x}"
        result.append(
            CapturedSpan(
                name=name,
                trace_id=trace_id,
                span_id=span_id,
                parent_span_id=previous_span_id,
                status="ERROR" if name == error_span else "OK",
                ended=name != unfinished_span,
                attributes={
                    "eval.run_id": "agent-run",
                    **(
                        {"eval.instance_id": instance_id}
                        if instance_id
                        else {}
                    ),
                },
            )
        )
        previous_span_id = span_id
    return result


def _reader(spans: list[CapturedSpan]):
    def read(
        _url: str,
        _project: str,
        _start: str,
        _run_id: str,
        _instances: Sequence[str],
    ) -> list[CapturedSpan]:
        return spans

    return read


def _run(
    tmp_path: Path,
    *,
    executor=None,
    spans: list[CapturedSpan] | None = None,
    environ: Mapping[str, str] | None = None,
):
    return run_independent_agent_e2e(
        _config(tmp_path),
        environ=(
            {"OPENAI_API_KEY": "test-secret", **_SANDBOX_ENV}
            if environ is None
            else environ
        ),
        process_executor=executor or _executor(),
        phoenix_reader=_reader(_spans() if spans is None else spans),
        source_pin_reader=_clean_source_pin,
    )


def test_missing_provider_credentials_blocks_without_running_process(
    tmp_path: Path,
) -> None:
    invoked = False

    def fail_if_called(*_args, **_kwargs):
        nonlocal invoked
        invoked = True
        raise AssertionError("process must not run without approved credentials")

    receipt = _run(tmp_path, executor=fail_if_called, environ={})

    assert receipt["status"] == "BLOCKED"
    assert receipt["exit_code"] == 3
    assert receipt["process"]["invoked"] is False
    assert invoked is False
    assert "provider_credentials" in {
        failure["category"] for failure in receipt["failures"]
    }


@pytest.mark.parametrize(
    "environment",
    [
        {"OPENAI_API_KEY": "test-secret"},
        {
            "OPENAI_API_KEY": "test-secret",
            "CODE_AGENT_E2E_SANDBOX_BACKEND": "docker",
        },
        {
            "OPENAI_API_KEY": "test-secret",
            "CODE_AGENT_E2E_SANDBOX_BACKEND": "wsl2",
            "CODE_AGENT_E2E_SANDBOX_IMAGE": "alpine:3.20",
        },
        {
            "OPENAI_API_KEY": "test-secret",
            "CODE_AGENT_E2E_SANDBOX_BACKEND": "auto",
            "CODE_AGENT_E2E_SANDBOX_IMAGE": "alpine:3.20",
        },
    ],
)
def test_incomplete_sandbox_config_blocks_without_running_process(
    tmp_path: Path, environment: Mapping[str, str]
) -> None:
    invoked = False

    def fail_if_called(*_args, **_kwargs):
        nonlocal invoked
        invoked = True
        raise AssertionError("process must not run without an explicit sandbox")

    receipt = _run(tmp_path, executor=fail_if_called, environ=environment)

    assert receipt["status"] == "BLOCKED"
    assert receipt["process"]["invoked"] is False
    assert invoked is False
    assert "sandbox_config" in {failure["category"] for failure in receipt["failures"]}


def test_unverified_provider_identity_blocks_release_receipt(tmp_path: Path) -> None:
    receipt = _run(tmp_path, executor=_executor(verified_identity=False))

    assert receipt["status"] == "BLOCKED"
    assert receipt["provider"]["model_revision_status"] == ("MODEL_IDENTITY_UNVERIFIED")
    assert "model_identity" in {failure["category"] for failure in receipt["failures"]}


def test_fixture_process_receipt_cannot_be_promoted(tmp_path: Path) -> None:
    receipt = _run(tmp_path, executor=_executor(provider_backed=False))

    assert receipt["status"] == "BLOCKED"
    assert receipt["provider"]["backed"] is False
    assert "provider_backing" in {
        failure["category"] for failure in receipt["failures"]
    }


@pytest.mark.parametrize(
    "executor",
    [
        _executor(route_session="other-session"),
        _executor(route_provider="anthropic"),
        _executor(route_run_id="other-run"),
        _executor(route_requested_model="other-model"),
    ],
)
def test_provider_route_must_bind_run_provider_model_and_sessions(
    tmp_path: Path, executor
) -> None:
    receipt = _run(tmp_path, executor=executor)

    assert receipt["status"] == "BLOCKED"
    assert "provider_route_binding" in {
        failure["category"] for failure in receipt["failures"]
    }


def test_missing_required_agent_check_blocks_release_receipt(tmp_path: Path) -> None:
    receipt = _run(tmp_path, executor=_executor(missing_check="mcp_call"))

    assert receipt["status"] == "BLOCKED"
    assert receipt["checks"]["mcp_call"] is False
    assert any(
        failure["category"] == "agent_checks" and "mcp_call" in failure["message"]
        for failure in receipt["failures"]
    )


@pytest.mark.parametrize(
    ("spans", "expected_category"),
    [
        ([], "trace_readback"),
        (_spans(missing_span="eval.run"), "trace_spans"),
        (_spans(missing_span="memory.recall"), "trace_spans"),
        (_spans(multiple_traces=True), "trace_topology"),
        (_spans(unfinished_span="memory.recall"), "trace_completion"),
        (_spans(error_span="memory.reflect"), "trace_status"),
        (_spans(child_binding="parent-session"), "trace_session_binding"),
    ],
)
def test_incomplete_phoenix_evidence_blocks_release_receipt(
    tmp_path: Path,
    spans: list[CapturedSpan],
    expected_category: str,
) -> None:
    receipt = _run(tmp_path, spans=spans)

    assert receipt["status"] == "BLOCKED"
    assert expected_category in {failure["category"] for failure in receipt["failures"]}


def test_complete_pinned_evidence_emits_verified_release_receipt(
    tmp_path: Path,
) -> None:
    receipt = _run(tmp_path)

    assert receipt["status"] == "VERIFIED"
    assert receipt["exit_code"] == 0
    assert receipt["provider"] == {
        "backed": True,
        "kind": "openai",
        "model_revision_status": "MODEL_IDENTITY_VERIFIED",
        "reported_models": ["gpt-test-2026-09"],
        "call_count": 2,
    }
    assert all(receipt["checks"].values())
    assert receipt["trace"]["backend_readback"] is True
    assert receipt["trace"]["single_trace"] is True
    assert receipt["trace_id"] == _TRACE_ID
    assert receipt["failures"] == []
    _agent_e2e_predicate(receipt)
    assert all(
        len(value) == 64
        for key, value in receipt["raw_evidence"].items()
        if key.endswith("_sha256")
    )

    run_root = _config(tmp_path).artifact_root / "agent-run"
    assert RunArtifacts("agent-run", run_root.parent).verify_checksums() == []
    identities = json.loads(
        (run_root / "provider-identities.json").read_text(encoding="utf-8")
    )
    assert set(identities["calls"][0]["model_identity"]) <= {
        "endpoint_host",
        "requested_model",
        "reported_model",
        "response_id",
        "system_fingerprint",
        "created",
        "identity_verified",
    }


def test_provider_subprocess_environment_is_allowlisted(tmp_path: Path) -> None:
    environment = _subprocess_environment(
        "openai",
        {
            "PATH": os.environ.get("PATH", ""),
            "OPENAI_API_KEY": "test-secret",
            "OPENAI_MODEL": "gpt-test",
            "ANTHROPIC_API_KEY": "must-not-pass",
            "CODE_AGENT_PROVIDER_CONFIG": str(tmp_path / "provider.json"),
            "CODE_AGENT_PROVIDER_PROFILE": "local-secret-profile",
            "CODE_AGENT_E2E_SANDBOX_BACKEND": "docker",
            "CODE_AGENT_E2E_SANDBOX_IMAGE": "alpine:3.20",
            "CODE_AGENT_E2E_WSL_DISTRO": "must-not-pass-for-docker",
            "CODE_AGENT_E2E_SANDBOX_SECRET": "must-not-pass",
            "UNRELATED_SECRET": "must-not-pass",
        },
        run_id="agent-run",
        process_receipt=tmp_path / "process.json",
        route_receipt=tmp_path / "route.jsonl",
        phoenix_url="http://127.0.0.1:6006",
    )

    assert environment["OPENAI_API_KEY"] == "test-secret"
    assert environment["OPENAI_MODEL"] == "gpt-test"
    assert environment["CODE_AGENT_EVAL_RUN_ID"] == "agent-run"
    assert environment["LLM_PROVIDER"] == "openai"
    assert environment["CODE_AGENT_E2E_SANDBOX_BACKEND"] == "docker"
    assert environment["CODE_AGENT_E2E_SANDBOX_IMAGE"] == "alpine:3.20"
    assert "ANTHROPIC_API_KEY" not in environment
    assert "CODE_AGENT_PROVIDER_CONFIG" not in environment
    assert "CODE_AGENT_PROVIDER_PROFILE" not in environment
    assert "CODE_AGENT_E2E_WSL_DISTRO" not in environment
    assert "CODE_AGENT_E2E_SANDBOX_SECRET" not in environment
    assert "UNRELATED_SECRET" not in environment


def test_wsl2_subprocess_environment_requires_and_forwards_distro(
    tmp_path: Path,
) -> None:
    environment = _subprocess_environment(
        "openai",
        {
            "OPENAI_API_KEY": "test-secret",
            "CODE_AGENT_E2E_SANDBOX_BACKEND": "wsl2",
            "CODE_AGENT_E2E_SANDBOX_IMAGE": "alpine:3.20",
            "CODE_AGENT_E2E_WSL_DISTRO": "Ubuntu-24.04",
        },
        run_id="agent-run",
        process_receipt=tmp_path / "process.json",
        route_receipt=tmp_path / "route.jsonl",
        phoenix_url="http://127.0.0.1:6006",
    )

    assert environment["CODE_AGENT_E2E_SANDBOX_BACKEND"] == "wsl2"
    assert environment["CODE_AGENT_E2E_SANDBOX_IMAGE"] == "alpine:3.20"
    assert environment["CODE_AGENT_E2E_WSL_DISTRO"] == "Ubuntu-24.04"


def test_stdio_mcp_fixture_lists_and_calls_e2e_echo() -> None:
    repo = Path(__file__).resolve().parents[2]
    requests = [
        {"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {}},
        {"jsonrpc": "2.0", "method": "notifications/initialized"},
        {"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": {}},
        {
            "jsonrpc": "2.0",
            "id": 3,
            "method": "tools/call",
            "params": {"name": "e2e_echo", "arguments": {"text": "MCP_E2E_ECHO_OK"}},
        },
    ]
    completed = subprocess.run(
        [
            sys.executable,
            str(repo / "tests" / "e2e" / "independent_agents_runtime_server.py"),
            "--mcp-stdio",
        ],
        cwd=repo,
        input="".join(json.dumps(request) + "\n" for request in requests),
        capture_output=True,
        text=True,
        timeout=30,
        check=False,
    )

    assert completed.returncode == 0, completed.stderr
    responses = [json.loads(line) for line in completed.stdout.splitlines()]
    assert [response["id"] for response in responses] == [1, 2, 3]
    assert responses[1]["result"]["tools"][0]["name"] == "e2e_echo"
    assert responses[2]["result"]["content"][0]["text"] == "e2e_echo: MCP_E2E_ECHO_OK"


def test_phoenix_readback_polls_until_bound_trace_is_complete(
    tmp_path: Path,
) -> None:
    calls = 0

    def reader(
        _url: str,
        _project: str,
        _start: str,
        _run_id: str,
        _instances: Sequence[str],
    ) -> list[CapturedSpan]:
        nonlocal calls
        calls += 1
        return [] if calls == 1 else _spans()

    receipt = run_independent_agent_e2e(
        replace(_config(tmp_path), trace_readback_attempts=2),
        environ={"OPENAI_API_KEY": "test-secret", **_SANDBOX_ENV},
        process_executor=_executor(),
        phoenix_reader=reader,
        source_pin_reader=_clean_source_pin,
        sleep_fn=lambda _seconds: None,
    )

    assert calls == 2
    assert receipt["status"] == "VERIFIED"
    assert receipt["trace"]["all_ended"] is True
    assert receipt["trace"]["main_session_bound"] is True
    assert receipt["trace"]["subagent_session_bound"] is True


@pytest.mark.integration
@pytest.mark.skipif(
    os.environ.get("CODE_AGENT_RUN_INDEPENDENT_AGENT_FIXTURE_ASSERTION") != "1",
    reason="set CODE_AGENT_RUN_INDEPENDENT_AGENT_FIXTURE_ASSERTION=1 for process fixture assertion",
)
def test_actual_fixture_process_stays_non_provider_backed(tmp_path: Path) -> None:
    repo = Path(__file__).resolve().parents[2]
    receipt_path = tmp_path / "fixture-process.json"
    environment = {
        key: value
        for key, value in os.environ.items()
        if not key.upper().startswith(("OPENAI_", "ANTHROPIC_", "OTEL_"))
        and key.upper()
        not in {
            "CODE_AGENT_E2E_PROVIDER",
            "CODE_AGENT_PROVIDER_CONFIG",
            "CODE_AGENT_PROVIDER_PROFILE",
        }
    }
    environment.update(
        {
            "CODE_AGENT_RUN_INDEPENDENT_AGENTS_E2E": "1",
            "CODE_AGENT_INDEPENDENT_AGENTS_E2E_RUN_ID": "pytest-fixture-assertion",
            "CODE_AGENT_INDEPENDENT_AGENTS_E2E_RECEIPT": str(receipt_path),
        }
    )
    completed = subprocess.run(
        [
            "go",
            "test",
            "./tests/e2e",
            "-run",
            "^TestProductionIndependentAgentsAndMemoryProcess$",
            "-count=1",
        ],
        cwd=repo,
        env=environment,
        capture_output=True,
        text=True,
        timeout=180,
        check=False,
    )

    assert completed.returncode == 0
    process_receipt = json.loads(receipt_path.read_text(encoding="utf-8"))
    assert process_receipt["provider_backed"] is False
    assert process_receipt["evidence_type"].startswith("fixture-backed")
    assert process_receipt["release_checks"]["mcp_call"] is False
    assert process_receipt["release_checks"]["sandbox_enforced"] is False
