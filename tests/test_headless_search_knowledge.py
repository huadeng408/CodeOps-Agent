from __future__ import annotations

import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from threading import Thread

from opentelemetry import trace

from eval.driver_headless import LocalToolExecutor
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
                result = LocalToolExecutor(str(tmp_path)).execute(
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
    assert received["traceparent"] == f"00-{'1' * 32}-{'2' * 16}-01"
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
