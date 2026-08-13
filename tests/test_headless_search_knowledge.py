from __future__ import annotations

import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from threading import Thread

from opentelemetry import trace

from eval.adapter import EvalInstance, EvalResult
import eval.driver_headless as driver_headless
from eval.driver_headless import HeadlessDriver, LocalToolExecutor
from eval.harness.trace_join import eval_join_context


def test_search_knowledge_calls_go_endpoint_with_o3_policy_and_run_id(
    monkeypatch, tmp_path
) -> None:
    received: dict[str, object] = {}

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self) -> None:  # noqa: N802
            received["path"] = self.path
            received["authorization"] = self.headers.get("X-Internal-Token")
            received["traceparent"] = self.headers.get("traceparent")
            received["baggage"] = self.headers.get("baggage")
            received["payload"] = json.loads(
                self.rfile.read(int(self.headers["Content-Length"])).decode("utf-8")
            )
            body = json.dumps(
                {
                    "code": 200,
                    "data": {
                        "results": [
                            {
                                "fileName": "go-spec.md",
                                "chunkId": 4,
                                "documentId": "go-spec",
                                "textContent": "Interfaces are satisfied implicitly.",
                                "score": 0.91,
                            }
                        ]
                    },
                }
            ).encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, _format: str, *_args: object) -> None:
            pass

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = Thread(target=server.serve_forever, daemon=True)
    thread.start()
    monkeypatch.setenv("CODE_AGENT_RAG_SERVER_URL", f"http://127.0.0.1:{server.server_port}")
    monkeypatch.setenv("CODE_AGENT_RAG_INTERNAL_SECRET", "test-only-internal-secret")
    monkeypatch.setenv("CODE_AGENT_RAG_USER_ID", "7")
    try:
        span_context = trace.SpanContext(
            trace_id=int("1" * 32, 16),
            span_id=int("2" * 16, 16),
            is_remote=False,
            trace_flags=trace.TraceFlags(1),
        )
        with trace.use_span(trace.NonRecordingSpan(span_context)):
            with eval_join_context("o3-run", "o3-instance"):
                executor = LocalToolExecutor(str(tmp_path))
                result = executor.execute(
                    "SearchKnowledge", json.dumps({"query": "What is an interface?"})
                )
    finally:
        server.shutdown()
        thread.join()
        server.server_close()

    assert result.exit_code == 0
    assert result.error == ""
    assert "Interfaces are satisfied implicitly." in result.output
    assert "What is an interface?" not in result.output
    assert received["path"] == "/internal/orchestrator/knowledge-search"
    assert received["authorization"] == "test-only-internal-secret"
    assert str(received["traceparent"]).startswith(f"00-{'1' * 32}-")
    assert str(received["traceparent"]).endswith("-01")
    assert "eval.run_id=o3-run" in str(received["baggage"])
    assert "eval.instance_id=o3-instance" in str(received["baggage"])
    assert received["payload"] == {
        "user": {"id": 7, "orgTags": "", "primaryOrg": ""},
        "query": "What is an interface?",
        "topK": 5,
        "mode": "hybrid",
        "disableRerank": False,
        "runId": "o3-run",
    }
    assert executor.safe_evidence() == {
        "retrieval_hits": [
            {
                "rank": 1,
                "document_id": "go-spec",
                "chunk_id": 4,
                "score": 0.91,
            }
        ]
    }


def test_search_knowledge_fails_closed_without_internal_rag_configuration(
    monkeypatch, tmp_path
) -> None:
    for name in (
        "CODE_AGENT_RAG_SERVER_URL",
        "CODE_AGENT_RAG_INTERNAL_SECRET",
        "CODE_AGENT_RAG_USER_ID",
    ):
        monkeypatch.delenv(name, raising=False)

    result = LocalToolExecutor(str(tmp_path)).execute(
        "SearchKnowledge", json.dumps({"query": "interface"})
    )

    assert result.exit_code == 1
    assert "CODE_AGENT_RAG_SERVER_URL" in result.error


def test_search_knowledge_uses_configured_request_timeout_for_live_rag(
    monkeypatch, tmp_path
) -> None:
    """A slow, real retrieval must not be discarded at the old fixed 30 seconds."""
    observed: dict[str, int] = {}

    class Response:
        status = 200

        def __enter__(self):
            return self

        def __exit__(self, *_args):
            return False

        def read(self) -> bytes:
            return json.dumps(
                {"code": 200, "data": {"results": [{"documentId": "doc", "chunkId": 1}]}}
            ).encode()

    def urlopen(_request, timeout: int):
        observed["timeout"] = timeout
        return Response()

    monkeypatch.setattr(driver_headless.urllib.request, "urlopen", urlopen)
    monkeypatch.setenv("CODE_AGENT_RAG_SERVER_URL", "http://example.test")
    monkeypatch.setenv("CODE_AGENT_RAG_INTERNAL_SECRET", "test-only-internal-secret")
    monkeypatch.setenv("CODE_AGENT_RAG_USER_ID", "7")
    monkeypatch.setenv("CODE_AGENT_RAG_TIMEOUT_SECONDS", "90")
    span_context = trace.SpanContext(
        trace_id=int("5" * 32, 16),
        span_id=int("6" * 16, 16),
        is_remote=False,
        trace_flags=trace.TraceFlags(1),
    )
    with trace.use_span(trace.NonRecordingSpan(span_context)):
        with eval_join_context("o3-run", "o3-instance"):
            result = LocalToolExecutor(str(tmp_path)).execute(
                "SearchKnowledge", json.dumps({"query": "interface"})
            )

    assert result.exit_code == 0
    assert observed["timeout"] == 90


def test_search_knowledge_fails_closed_when_response_has_no_stable_hit(
    monkeypatch, tmp_path
) -> None:
    class Handler(BaseHTTPRequestHandler):
        def do_POST(self) -> None:  # noqa: N802
            body = json.dumps({"code": 200, "data": {"results": []}}).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, _format: str, *_args: object) -> None:
            pass

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = Thread(target=server.serve_forever, daemon=True)
    thread.start()
    monkeypatch.setenv("CODE_AGENT_RAG_SERVER_URL", f"http://127.0.0.1:{server.server_port}")
    monkeypatch.setenv("CODE_AGENT_RAG_INTERNAL_SECRET", "test-only-internal-secret")
    monkeypatch.setenv("CODE_AGENT_RAG_USER_ID", "7")
    try:
        span_context = trace.SpanContext(
            trace_id=int("3" * 32, 16),
            span_id=int("4" * 16, 16),
            is_remote=False,
            trace_flags=trace.TraceFlags(1),
        )
        with trace.use_span(trace.NonRecordingSpan(span_context)):
            with eval_join_context("o3-run", "o3-instance"):
                result = LocalToolExecutor(str(tmp_path)).execute(
                    "SearchKnowledge", json.dumps({"query": "interface"})
                )
    finally:
        server.shutdown()
        thread.join()
        server.server_close()

    assert result.exit_code == 1
    assert "stable hit" in result.error


def test_strict_o3_runner_failure_never_falls_back_or_mutates_task(
    monkeypatch, tmp_path
) -> None:
    driver = HeadlessDriver(strict_o3=True)
    instance = EvalInstance(
        instance_id="o3-instance",
        task_description="Use SearchKnowledge to answer from the pinned corpus.",
    )
    original_description = instance.task_description

    def fail_runner(*_args, **_kwargs):
        raise RuntimeError("runner protocol failed")

    def direct_call(*_args, **_kwargs):
        raise AssertionError("strict O3 must never call the direct LLM fallback")

    monkeypatch.setattr(driver, "_solve_with_runner", fail_runner)
    monkeypatch.setattr(driver, "_solve_direct", direct_call)

    result = driver._do_solve_inner(
        instance,
        str(tmp_path),
        "trace-123",
        cancel_event=None,
    )

    assert isinstance(result, EvalResult)
    assert result.instance_id == "o3-instance"
    assert result.trace_id == "trace-123"
    assert "strict O3 ConversationRunner failure" in result.error
    assert "runner protocol failed" in result.error
    assert instance.task_description == original_description


def test_strict_o3_rejects_direct_only_execution(tmp_path) -> None:
    driver = HeadlessDriver(use_runner=False, strict_o3=True)
    result = driver._do_solve_inner(
        EvalInstance(instance_id="o3-instance", task_description="answer"),
        str(tmp_path),
        "trace-123",
        cancel_event=None,
    )

    assert "requires ConversationRunner" in result.error


def test_strict_o3_tool_executor_denies_non_rag_tools(tmp_path) -> None:
    executor = LocalToolExecutor(str(tmp_path), allowed_tools=frozenset({"SearchKnowledge"}))

    result = executor.execute("Read", json.dumps({"path": "anything.txt"}))

    assert result.exit_code == 1
    assert "not allowed" in result.error
