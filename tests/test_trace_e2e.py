from __future__ import annotations

import json
import os
import shutil
import subprocess
import tomllib
from pathlib import Path
from urllib.parse import parse_qs, urlparse

import pytest

from tests.integration.trace_e2e import (
    SpanRecord,
    build_model_url,
    build_spans_url,
    parse_model_ids,
    parse_spans_page,
    poll_for_trace,
    sanitize_text,
    select_run_trace,
)

RUN_ID = "run-123"
TRACE_ID = "a" * 32
ROOT_ID = "1" * 16


def span(
    name: str,
    span_id: str,
    parent_id: str | None,
    *,
    attributes: dict | None = None,
    status_code: str = "OK",
) -> SpanRecord:
    return SpanRecord(
        trace_id=TRACE_ID,
        span_id=span_id,
        parent_id=parent_id,
        name=name,
        status_code=status_code,
        attributes=attributes or {},
    )


def valid_spans(*, flattened_result: bool = False) -> list[SpanRecord]:
    if flattened_result:
        attributes = {
            "gen_ai.tool.call.result": f"TRACE_E2E_FIXTURE:{RUN_ID}",
        }
    else:
        attributes = {
            "gen_ai": {
                "tool": {
                    "call": {
                        "result": f"TRACE_E2E_FIXTURE:{RUN_ID}",
                    }
                }
            }
        }
    return [
        span("invoke_agent code-agent", ROOT_ID, None),
        span("chat", "2" * 16, ROOT_ID),
        span(
            "execute_tool Read",
            "3" * 16,
            ROOT_ID,
            attributes=attributes,
        ),
        span("chat", "4" * 16, ROOT_ID),
    ]


def test_select_run_trace_requires_fixture_result_and_cross_runtime_tree():
    result = select_run_trace(valid_spans(), RUN_ID)

    assert result.trace_id == TRACE_ID
    assert [item.runtime for item in result.required_spans] == [
        "go",
        "go",
        "python",
        "python",
    ]


def test_select_run_trace_accepts_flattened_tool_result_attribute():
    result = select_run_trace(valid_spans(flattened_result=True), RUN_ID)

    assert result.trace_id == TRACE_ID


def test_select_run_trace_rejects_historical_trace_without_run_marker():
    spans = valid_spans()
    spans[2] = span("execute_tool Read", "3" * 16, ROOT_ID)

    with pytest.raises(AssertionError, match=f"TRACE_E2E_FIXTURE:{RUN_ID}"):
        select_run_trace(spans, RUN_ID)


def test_select_run_trace_requires_two_chat_spans():
    spans = valid_spans()
    spans.pop()

    with pytest.raises(AssertionError, match="at least two chat spans"):
        select_run_trace(spans, RUN_ID)


def test_select_run_trace_rejects_error_tool_span():
    spans = valid_spans()
    spans[2] = span(
        "execute_tool Read",
        "3" * 16,
        ROOT_ID,
        attributes={
            "gen_ai.tool.call.result": f"TRACE_E2E_FIXTURE:{RUN_ID}",
        },
        status_code="ERROR",
    )

    with pytest.raises(AssertionError, match="ERROR status"):
        select_run_trace(spans, RUN_ID)


def test_select_run_trace_rejects_detached_chat_parent():
    spans = valid_spans()
    spans[-1] = span("chat", "4" * 16, "f" * 16)

    with pytest.raises(AssertionError, match="do not reach invoke_agent"):
        select_run_trace(spans, RUN_ID)


def test_build_model_url_normalizes_deepseek_base_url():
    assert (
        build_model_url("https://api.deepseek.com")
        == "https://api.deepseek.com/v1/models"
    )
    assert (
        build_model_url("https://api.deepseek.com/v1")
        == "https://api.deepseek.com/v1/models"
    )


def test_build_spans_url_encodes_time_project_and_cursor():
    url = build_spans_url(
        "http://127.0.0.1:6006",
        "default project",
        "2026-07-29T01:02:03+00:00",
        "cursor/2",
    )
    parsed = urlparse(url)

    assert parsed.path == "/v1/projects/default%20project/spans"
    assert parse_qs(parsed.query) == {
        "start_time": ["2026-07-29T01:02:03+00:00"],
        "limit": ["100"],
        "cursor": ["cursor/2"],
    }


def test_parse_model_ids_reads_openai_compatible_payload():
    assert parse_model_ids({"data": [{"id": "deepseek-v4-pro"}]}) == {
        "deepseek-v4-pro"
    }


def test_parse_model_ids_rejects_missing_data_list():
    with pytest.raises(ValueError, match="data list"):
        parse_model_ids({"models": []})


def test_parse_spans_page_reads_full_phoenix_span_shape():
    payload = {
        "data": [
            {
                "name": "chat",
                "context": {"trace_id": TRACE_ID, "span_id": "2" * 16},
                "parent_id": ROOT_ID,
                "status_code": "OK",
                "attributes": {"gen_ai": {"system": "openai"}},
                "span_kind": "CLIENT",
                "start_time": "2026-07-29T01:02:03Z",
                "end_time": "2026-07-29T01:02:04Z",
            }
        ],
        "next_cursor": "cursor-2",
    }

    spans, cursor = parse_spans_page(payload)

    assert spans == [
        SpanRecord(
            trace_id=TRACE_ID,
            span_id="2" * 16,
            parent_id=ROOT_ID,
            name="chat",
            status_code="OK",
            attributes={"gen_ai": {"system": "openai"}},
        )
    ]
    assert cursor == "cursor-2"


def test_parse_spans_page_rejects_malformed_context():
    with pytest.raises(ValueError, match="malformed span"):
        parse_spans_page({"data": [{"name": "chat"}]})


def test_poll_for_trace_returns_immediate_match():
    result = poll_for_trace(
        lambda: valid_spans(),
        RUN_ID,
        timeout=1,
        monotonic=lambda: 0.0,
        sleep=lambda _: None,
    )

    assert result.trace_id == TRACE_ID


def test_poll_for_trace_times_out_with_safe_candidates():
    now = [0.0]

    def advance(seconds: float) -> None:
        now[0] += seconds

    with pytest.raises(TimeoutError) as exc_info:
        poll_for_trace(
            lambda: [span("chat", "2" * 16, ROOT_ID)],
            RUN_ID,
            timeout=1,
            monotonic=lambda: now[0],
            sleep=advance,
        )

    message = str(exc_info.value)
    assert "chat" in message
    assert TRACE_ID in message
    assert "attributes" not in message


def test_sanitize_text_removes_exact_api_key():
    secret = "sk-test"
    source = json.dumps({"error": f"bad Authorization Bearer {secret}"})

    sanitized = sanitize_text(source, secret)

    assert secret not in sanitized
    assert "<redacted>" in sanitized


def test_runner_uses_environment_credential_without_file_fallback():
    powershell = shutil.which("powershell.exe")
    if not powershell:
        pytest.skip("Windows PowerShell is unavailable")
    script = Path(__file__).parents[1] / "scripts" / "test-trace-e2e.ps1"
    environment = os.environ.copy()
    environment["OPENAI_API_KEY"] = "sk-test"

    result = subprocess.run(
        [
            powershell,
            "-NoProfile",
            "-ExecutionPolicy",
            "Bypass",
            "-File",
            str(script),
            "-ValidateApiKeyOnly",
        ],
        capture_output=True,
        text=True,
        env=environment,
        timeout=15,
        check=False,
    )

    assert result.returncode == 0, result.stderr
    assert "api_key=valid" in result.stdout
    assert "sk-test" not in result.stdout + result.stderr

    source = script.read_text(encoding="utf-8")
    assert "ApiKeyFile" not in source
    assert "Get-Content -LiteralPath" not in source


def test_trace_runner_routes_custom_phoenix_url_to_otlp_http_endpoint():
    powershell = shutil.which("pwsh") or shutil.which("powershell.exe")
    if not powershell:
        pytest.skip("PowerShell is unavailable")
    root = Path(__file__).parents[1]
    endpoint_helper = root / "scripts" / "lib" / "receipt-endpoints.ps1"
    command = (
        f". '{endpoint_helper}'; "
        "Resolve-OtlpHttpTraceEndpoint -PhoenixUrl 'http://127.0.0.1:6111/'"
    )

    completed = subprocess.run(
        [powershell, "-NoProfile", "-Command", command],
        cwd=root,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        timeout=15,
        check=False,
    )

    assert completed.returncode == 0, completed.stderr
    assert completed.stdout.strip() == "http://127.0.0.1:6111/v1/traces"

    source = (root / "scripts" / "test-trace-e2e.ps1").read_text(encoding="utf-8")
    assert "OTEL_EXPORTER_OTLP_ENDPOINT = $otlpTraceEndpoint" in source
    assert "EnvironmentVariables.Remove('OTEL_EXPORTER_OTLP_ENDPOINT')" not in source


def test_compose_uses_official_phoenix_image():
    compose = (Path(__file__).parents[1] / "docker-compose.yml").read_text(
        encoding="utf-8"
    )

    assert "image: arizephoenix/phoenix:latest" in compose
    assert "image: arize/phoenix:latest" not in compose


def test_runner_allows_slow_first_phoenix_pull():
    script = (Path(__file__).parents[1] / "scripts" / "test-trace-e2e.ps1").read_text(
        encoding="utf-8"
    )

    assert "[int]$PhoenixStartupTimeoutSeconds = 300" in script
    assert "-TimeoutSeconds $PhoenixStartupTimeoutSeconds" in script


def test_runner_uses_basic_parsing_for_windows_powershell_readiness_probe():
    script = (Path(__file__).parents[1] / "scripts" / "test-trace-e2e.ps1").read_text(
        encoding="utf-8"
    )

    assert "Invoke-WebRequest -UseBasicParsing -Uri $uri" in script


def test_runner_disables_fast_model_routing():
    script = (Path(__file__).parents[1] / "scripts" / "test-trace-e2e.ps1").read_text(
        encoding="utf-8"
    )

    assert "model_fast = 'disabled'" in script
    assert "MODEL_FAST = 'disabled'" in script


def test_trace_e2e_extra_declares_python_telemetry_dependencies():
    pyproject_path = Path(__file__).parents[1] / "pyproject.toml"
    pyproject = tomllib.loads(pyproject_path.read_text(encoding="utf-8"))
    # grpcio/protobuf moved to core dependencies (required by protobuf codegen)
    core_deps = pyproject["project"]["dependencies"]
    assert any(dependency.startswith("grpcio") for dependency in core_deps)
    assert any(dependency.startswith("protobuf") for dependency in core_deps)
    # trace-e2e carries OTel extras only
    trace_e2e = pyproject["project"]["optional-dependencies"]["trace-e2e"]
    assert any(dependency.startswith("opentelemetry-sdk") for dependency in trace_e2e)
    assert any(
        dependency.startswith("opentelemetry-exporter-otlp-proto-http")
        for dependency in trace_e2e
    )


def test_runner_preflights_python_telemetry_dependencies():
    script = (Path(__file__).parents[1] / "scripts" / "test-trace-e2e.ps1").read_text(
        encoding="utf-8"
    )

    assert "Assert-PythonTraceDependencies" in script
    assert '.[trace-e2e]' in script
