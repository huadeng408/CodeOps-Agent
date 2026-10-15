from __future__ import annotations

import json
import hashlib
import threading
import subprocess
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import parse_qs, urlsplit

import pytest


@pytest.mark.parametrize("mode", ["success", "multi-success", "multi-repeated-tool", "multi-missing-round", "multi-omit-request", "multi-omit-unadmitted", "multi-unavailable", "unavailable", "missing-pins", "secret-name", "secret-event", "secret-status", "raw-prompt", "broken-sequence", "binary-mismatch", "source-mismatch", "sensitive-id"])
def test_external_product_trace_command_reports_only_verified_readback(tmp_path, capsys, mode):
    from eval.product_trace import main

    multi = mode.startswith("multi-")
    class Unavailable(BaseHTTPRequestHandler):
        def do_GET(self):
            query = parse_qs(urlsplit(self.path).query)
            unavailable = mode == "unavailable" or (mode == "multi-unavailable" and (
                "session.run_id:" + json.dumps(run_ids[1]) in query.get("attribute", []) or query.get("trace_id") == ["b" * 32]))
            self.send_response(503 if unavailable else 200)
            self.end_headers()
            if unavailable:
                self.wfile.write(b"provider detail must not enter the report")
                return
            spans = []
            for index, (name, attributes) in enumerate([
                ("agent.main", {"session.id": session_id, "session.run_id": run_id}),
                ("chat", {}),
                ("execute_tool Write", {"gen_ai.tool.call.id": "tool-1", "gen_ai.tool.name": "Write"}),
                ("provider.call", {"model.call_id": "call-1", "model.purpose": "foreground", "gen_ai.request.model": "fixture"}),
                ("provider.attempt", {"model.call_id": "call-1", "model.purpose": "foreground", "gen_ai.request.model": "fixture"}),
            ], start=1):
                spans.append({"name": name, "context": {"trace_id": "a" * 32, "span_id": str(index) * 16},
                              "parent_id": None if index == 1 else "2" * 16 if index == 4 else "4" * 16 if index == 5 else "1" * 16,
                              "end_time": "2026-10-15T00:00:01Z", "attributes": attributes})
            sensitive = "sk-" + "fixture" * 5
            if mode == "secret-name": spans[-1]["name"] = sensitive
            elif mode == "secret-event": spans[-1]["events"] = [{"attributes": {"exception.message": sensitive}}]
            elif mode == "secret-status": spans[-1]["status_message"] = sensitive
            elif mode == "raw-prompt": spans[-1]["attributes"]["gen_ai.input.messages"] = "PRIVATE_PROMPT_SENTINEL"
            if multi and ("session.run_id:" + json.dumps(run_ids[1]) in query.get("attribute", []) or query.get("trace_id") == ["b" * 32]):
                spans = [
                    {"name": "agent.main", "context": {"trace_id": "b" * 32, "span_id": "6" * 16}, "attributes": {"session.id": session_id, "session.run_id": run_ids[1]}},
                    {"name": "chat", "context": {"trace_id": "b" * 32, "span_id": "7" * 16}, "parent_id": "6" * 16, "attributes": {}},
                    {"name": "provider.call", "context": {"trace_id": "b" * 32, "span_id": "8" * 16}, "parent_id": "7" * 16, "attributes": {"model.call_id": "call-2", "model.purpose": "foreground", "gen_ai.request.model": "fixture"}},
                    {"name": "provider.attempt", "context": {"trace_id": "b" * 32, "span_id": "9" * 16}, "parent_id": "8" * 16, "attributes": {"model.call_id": "call-2", "model.purpose": "foreground", "gen_ai.request.model": "fixture"}},
                ]
                for span in spans:
                    span["end_time"] = "2026-10-15T00:00:01Z"
                if mode == "multi-repeated-tool":
                    spans.append({"name": "execute_tool Write", "context": {"trace_id": "b" * 32, "span_id": "a" * 16}, "parent_id": "6" * 16, "end_time": "2026-10-15T00:00:01Z", "attributes": {"gen_ai.tool.call.id": "tool-1", "gen_ai.tool.name": "Write"}})
                if mode == "multi-missing-round":
                    spans = []
            self.wfile.write(json.dumps({"data": spans[:1] if "name=agent.main" in self.path else spans}).encode())

        def log_message(self, *_args):
            pass

    fixture = Path(__file__).parents[1] / "fixtures" / "product_trace"
    if multi:
        scenario = ["repeat-tool"] if mode == "multi-repeated-tool" else ["unadmitted-run"] if mode == "multi-omit-unadmitted" else []
        subprocess.run(["go", "run", "./tests/fixtures/product_trace/generate_multirun.go", str(tmp_path), *scenario], cwd=Path(__file__).parents[2], check=True, capture_output=True, timeout=120)
        fixture = tmp_path
    files = {name: json.loads((fixture / (name + ".json")).read_text(encoding="utf-8")) for name in ("session", "before", "after")}
    if mode == "sensitive-id":
        files["session"] = json.loads((fixture / "session-sensitive-id.json").read_text(encoding="utf-8"))
    session_id = files["session"]["events"][0]["session_id"]
    run_ids = [event["payload"]["run_id"] for event in files["session"]["events"] if event["type"] == "session/continued"]
    run_id = run_ids[0]
    # The native Budget.Reserve API has no purpose; the fixture is generated by
    # its public Ledger seam with the same foreground fact shape as the Gateway.
    source_root = Path(__file__).parents[2]
    binary = tmp_path / "product.exe"
    binary.write_bytes(b"fixture-binary")
    files["inputs"] = {"source_sha256": {"eval/product_trace.py": hashlib.sha256((source_root / "eval/product_trace.py").read_bytes()).hexdigest()},
                       "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
                        "task_image": "sha256:" + "c" * 64, "model": "fixture", "trace_backend": {"image": "sha256:" + "d" * 64}}
    if mode == "missing-pins":
        files["inputs"] = {}
    if mode == "broken-sequence":
        files["session"]["events"][1]["seq"] = 99
    if mode == "binary-mismatch":
        binary.write_bytes(b"changed")
    if mode == "source-mismatch":
        files["inputs"]["source_sha256"]["eval/product_trace.py"] = "a" * 64
    for name, value in files.items():
        target = tmp_path / (name + ".json")
        if name in {"session", "before", "after"} and not (name == "session" and mode == "broken-sequence"):
            target.write_bytes((fixture / (name + ".json")).read_bytes())
        else:
            target.write_text(json.dumps(value, ensure_ascii=False), encoding="utf-8")
    if mode == "sensitive-id":
        # Keep the exact canonical escaped payload bytes for native verification.
        (tmp_path / "session.json").write_bytes((fixture / "session-sensitive-id.json").read_bytes())
    output = tmp_path / "readback"
    server = ThreadingHTTPServer(("127.0.0.1", 0), Unavailable)
    thread = threading.Thread(target=server.serve_forever)
    thread.start()
    try:
        result = main(["--phoenix-url", f"http://127.0.0.1:{server.server_port}", "--project", "default",
                       "--started-at", "2026-10-15T00:00:00Z", "--run-id", run_id,
                       *(["--run-id", run_ids[1]] if multi and mode != "multi-omit-request" else []),
                       "--session-events", str(tmp_path / "session.json"), "--budget-before", str(tmp_path / "before.json"),
                       "--budget-after", str(tmp_path / "after.json"), "--inputs", str(tmp_path / "inputs.json"),
                       "--binary", str(binary), "--output", str(output), "--attempts", "1", "--interval", "0"])
    finally:
        server.shutdown()
        thread.join()
        server.server_close()
    receipt = json.loads((output / "receipt.json").read_text(encoding="utf-8"))
    if mode in {"success", "multi-success", "multi-repeated-tool"}:
        assert result == 0 and receipt["status"] == "VERIFIED"
        assert receipt["capture_state"] == "complete" and receipt["backend_readback"] is True
        assert receipt["report"]["span_count"] == (10 if mode == "multi-repeated-tool" else 9 if multi else 5) and len(receipt["input_sha256"]) == 4
        assert receipt["report"]["expected_tools"] == (2 if mode == "multi-repeated-tool" else 1)
    elif mode in {"multi-omit-request", "multi-omit-unadmitted"}:
        assert result == 3 and receipt["status"] == "BLOCKED"
        assert receipt["capture_state"] == "unknown" and receipt["backend_readback"] is False
        assert receipt["input_error"] == "ValueError"
    elif multi:
        assert result == 3 and receipt["status"] == "BLOCKED"
        assert receipt["capture_state"] == "partial" and receipt["backend_readback"] is True
        assert receipt["report"]["expected_model_calls"] == 2
        assert receipt["report"]["verdict"] != "PASS"
        if mode == "multi-unavailable":
            attempt = receipt["attempts"][0]
            assert attempt["unavailable_runs"] == [{"run_id": run_ids[1], "error": "PhoenixReadbackError"}]
            capture = json.loads((output / attempt["artifact"]).read_text())
            assert len(capture["spans"]) == 5
    else:
        assert result == 3 and receipt["status"] == "BLOCKED"
        assert receipt["capture_state"] == "unknown" and receipt["backend_readback"] is False
        if mode in {"unavailable", "secret-name", "secret-event", "secret-status", "raw-prompt"}:
            assert receipt["attempts"][0]["error"] == "PhoenixReadbackError"
        else:
            assert receipt["input_error"] == "ValueError"
    assert "provider detail" not in json.dumps(receipt) + capsys.readouterr().out
    for artifact in output.glob("*.json"):
        text = artifact.read_text(encoding="utf-8")
        assert "sk-" + "fixture" * 5 not in text and "PRIVATE_PROMPT_SENTINEL" not in text
