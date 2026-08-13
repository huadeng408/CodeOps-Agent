from __future__ import annotations

import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from threading import Thread

from eval import run_o3


def test_default_o3_command_is_read_only_preflight(monkeypatch, capsys) -> None:
    for name in run_o3.REQUIRED_EXECUTION_ENV:
        monkeypatch.delenv(name, raising=False)

    exit_code = run_o3.main([])

    captured = capsys.readouterr()
    assert exit_code == 2
    assert "BLOCKED:" in captured.out
    assert "--execute" in captured.out
    assert "LOCAL_LLM_API_KEY" not in captured.out


def test_execution_preflight_never_echoes_secret_values(monkeypatch, capsys) -> None:
    for name in run_o3.REQUIRED_EXECUTION_ENV:
        monkeypatch.setenv(name, "super-secret-value")
    monkeypatch.setenv("CODE_AGENT_RAG_USER_ID", "not-an-integer")

    exit_code = run_o3.main(["--execute"])

    captured = capsys.readouterr()
    assert exit_code == 2
    assert "super-secret-value" not in captured.out + captured.err
    assert "CODE_AGENT_RAG_USER_ID must be a positive integer" in captured.out


def test_execution_preflight_requires_healthy_services_and_pinned_live_corpus(
    monkeypatch,
) -> None:
    requests: list[str] = []

    class Handler(BaseHTTPRequestHandler):
        def do_GET(self) -> None:  # noqa: N802 - stdlib handler signature
            requests.append(self.path)
            if self.path == "/healthz":
                body = json.dumps({"status": "ok", "embedding_preflight": "ok"}).encode()
            elif self.path == "/health":
                body = b'{"status":"ok"}'
            elif self.path == "/_cluster/health":
                body = b'{"status":"green"}'
            elif self.path == "/_alias/knowledge_base_current":
                body = b'{"knowledge_base_v2_bge_m3":{"aliases":{"knowledge_base_current":{}}}}'
            else:
                self.send_response(404)
                self.end_headers()
                return
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def do_POST(self) -> None:  # noqa: N802 - stdlib handler signature
            requests.append(self.path)
            if self.path == "/knowledge_base_v2_bge_m3/_search":
                body = json.dumps(
                    {
                        "hits": {"total": {"value": 1}},
                        "aggregations": {
                            "corpus_generations": {
                                "buckets": [{"key": "techdocs-2026-07-30-v1", "doc_count": 1}]
                            }
                        },
                    }
                ).encode()
            else:
                self.send_response(404)
                self.end_headers()
                return
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
    base_url = f"http://127.0.0.1:{server.server_port}"
    try:
        for name in run_o3.REQUIRED_EXECUTION_ENV:
            monkeypatch.setenv(name, "configured")
        monkeypatch.setenv("CODE_AGENT_RAG_USER_ID", "1")
        monkeypatch.setenv("CODE_AGENT_RAG_SERVER_URL", base_url)
        monkeypatch.setenv("PHOENIX_URL", base_url)
        monkeypatch.setenv("CODE_AGENT_RERANKER_URL", base_url)
        monkeypatch.setenv("CODE_AGENT_ELASTICSEARCH_URL", base_url)
        monkeypatch.setenv("CODE_AGENT_RAG_READ_ALIAS", "knowledge_base_current")

        assert run_o3._execution_preflight() is None
    finally:
        server.shutdown()
        thread.join()
        server.server_close()

    assert "/health" in requests
    assert "/_cluster/health" in requests
    assert "/_alias/knowledge_base_current" in requests
    assert "/knowledge_base_v2_bge_m3/_search" in requests


def test_execution_preflight_rejects_live_alias_drift(monkeypatch) -> None:
    class Handler(BaseHTTPRequestHandler):
        def do_GET(self) -> None:  # noqa: N802 - stdlib handler signature
            if self.path in {"/healthz", "/health"}:
                body = b'{"status":"ok","embedding_preflight":"ok"}'
            elif self.path == "/_cluster/health":
                body = b'{"status":"green"}'
            elif self.path == "/_alias/knowledge_base_current":
                body = b'{"knowledge_base":{"aliases":{"knowledge_base_current":{}}}}'
            else:
                self.send_response(404)
                self.end_headers()
                return
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
    base_url = f"http://127.0.0.1:{server.server_port}"
    try:
        for name in run_o3.REQUIRED_EXECUTION_ENV:
            monkeypatch.setenv(name, "configured")
        monkeypatch.setenv("CODE_AGENT_RAG_USER_ID", "1")
        monkeypatch.setenv("CODE_AGENT_RAG_SERVER_URL", base_url)
        monkeypatch.setenv("PHOENIX_URL", base_url)
        monkeypatch.setenv("CODE_AGENT_RERANKER_URL", base_url)
        monkeypatch.setenv("CODE_AGENT_ELASTICSEARCH_URL", base_url)
        monkeypatch.setenv("CODE_AGENT_RAG_READ_ALIAS", "knowledge_base_current")

        assert run_o3._execution_preflight() == "Elasticsearch alias target mismatch"
    finally:
        server.shutdown()
        thread.join()
        server.server_close()
