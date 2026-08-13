from __future__ import annotations

import json
import os
import subprocess
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

import pytest

from orchestrator.rag.multimodal_pilot import (
    Citation,
    IngestReceipt,
    SearchHit,
    TraceStatus,
    evaluate_citations,
    run_pilot,
    verify_artifact,
	select_phoenix_run_trace,
)


MARKER = "MM_PILOT_MARKER_20260813"


def test_select_phoenix_run_trace_requires_unique_successful_retrieve() -> None:
    run_id = "pilot-run-1"
    spans = [
        {
            "name": "retrieve orchestrator /knowledge-search",
            "context": {"trace_id": "a" * 32, "span_id": "b" * 16},
            "status_code": "UNSET",
            "attributes": {
                "eval.run_id": run_id,
                "rag.query_hash": "ABC123",
                "rag.top_n": 8,
                "rag.retrieval_mode": "bm25",
            },
        }
    ]
    status = select_phoenix_run_trace(spans, run_id)
    assert status.verified is True
    assert status.trace_id == "a" * 32


@pytest.mark.parametrize(
    "spans",
    [
        [],
        [{"name": "retrieve orchestrator /knowledge-search", "context": {"trace_id": "a" * 32, "span_id": "b" * 16}, "status_code": "UNSET", "attributes": {"eval.run_id": "historical"}}],
        [{"name": "retrieve orchestrator /knowledge-search", "context": {"trace_id": "a" * 32, "span_id": "b" * 16}, "status_code": "ERROR", "attributes": {"eval.run_id": "pilot-run-1", "rag.query_hash": "x", "rag.top_n": 8, "rag.retrieval_mode": "bm25"}}],
    ],
)
def test_select_phoenix_run_trace_rejects_missing_historical_or_error(spans) -> None:
    with pytest.raises(AssertionError):
        select_phoenix_run_trace(spans, "pilot-run-1")


def _fixture_runner(tmp_path: Path):
	# fmt: off
    ocr_dir = tmp_path / "ocr-fixture"
    ocr_dir.mkdir(exist_ok=True)
    content_list = ocr_dir / "document_content_list.json"
    middle = ocr_dir / "document_middle.json"
    content_list.write_text(json.dumps([{"type": "text", "text": MARKER, "bbox": [0, 0, 10, 10], "page_idx": 0}]), encoding="utf-8")
    middle.write_text(json.dumps({"_version_name": "3.4.4", "_backend": "pipeline"}), encoding="utf-8")

    def ocr(pdf: Path):
        return {"text": f"OCR text {MARKER}", "parser": "mineru", "mode": "ocr", "content_list_path": str(content_list), "middle_path": str(middle), "parser_version": "3.4.4", "backend": "pipeline"}

    def ingest(pdf: Path) -> IngestReceipt:
        return IngestReceipt(
            file_md5="abc123",
            file_name=pdf.name,
            accepted=True,
            pipeline={"verified": True, "stages": [{"stage": "index", "status": "SUCCESS"}]},
        )

    def search(query: str) -> list[SearchHit]:
        return [SearchHit(file_md5="abc123", file_name="pilot.pdf", chunk_id=0, text="OCR text " + MARKER, score=1.0, document_id="doc-1", page_id="doc-1:p0", element_ids=["doc-1:p0:e0"], bbox_refs=["doc-1:p0:e0:0,0,10,10"], citation_key="doc-1#doc-1:p0#doc-1:p0:e0")]

    def trace() -> TraceStatus:
        return TraceStatus(available=True, reason="fixture", trace_id="a" * 32, verified=True)

    return ocr, ingest, search, trace


def test_run_pilot_writes_auditable_non_gold_artifacts(tmp_path: Path) -> None:
    ocr, ingest, search, trace = _fixture_runner(tmp_path)
    result = run_pilot(
        tmp_path / "out",
        marker=MARKER,
        ocr=ocr,
        ingest=ingest,
        search=search,
        trace=trace,
        pdf_factory=lambda path, marker: path.write_bytes(b"%PDF-image-only-" + marker.encode()),
        poll_interval_seconds=0,
    )

    manifest = json.loads((result.output_dir / "manifest.json").read_text())
    assert manifest["pilot"] is True
    assert manifest["gold"] is False
    assert manifest["review_status"] == "UNREVIEWED"
    assert manifest["dataset_role"] == "non_gold_smoke"
    assert manifest["citation_quality"] == "NOT_EVALUATED"
    assert manifest["parser"]["name"] == "mineru"
    assert manifest["parser"]["mode"] == "ocr"
    assert manifest["trace"]["verified"] is True
    assert (result.output_dir / "summary.json").exists()
    assert (result.output_dir / "evidence.json").exists()
    assert (result.output_dir / "checksums.json").exists()
    assert (result.output_dir / "mineru" / "content_list.json").exists()
    assert (result.output_dir / "mineru" / "middle.json").exists()
    assert manifest["parser"]["version"] == "3.4.4"
    assert manifest["parser"]["backend"] == "pipeline"
    assert manifest["ocr_provenance"]["elements"][0]["page_id"] == "doc-1:p0"
    assert manifest["ocr_provenance"]["elements"][0]["bbox"] == [0.0, 0.0, 10.0, 10.0]
    evidence = json.loads((result.output_dir / "evidence.json").read_text())
    assert evidence["citations"][0]["citation_key"] == "doc-1#doc-1:p0#doc-1:p0:e0"
    assert evidence["citations"][0]["element_ids"] == ["doc-1:p0:e0"]
    checksums = json.loads((result.output_dir / "checksums.json").read_text())
    assert "mineru/content_list.json" in checksums
    assert "mineru/middle.json" in checksums


@pytest.mark.parametrize(
    ("pipeline", "trace_status", "message"),
    [
        (None, TraceStatus(True, "fixture", "a" * 32, verified=True), "pipeline verification"),
        ({"verified": False, "stages": []}, TraceStatus(True, "fixture", "a" * 32, verified=True), "pipeline verification"),
        ({"verified": True, "stages": []}, TraceStatus(True, "fixture", "", verified=False), "trace verification"),
    ],
)
def test_run_pilot_requires_verified_pipeline_and_trace(
    tmp_path: Path,
    pipeline,
    trace_status: TraceStatus,
    message: str,
) -> None:
    ocr, _ingest, search, _trace = _fixture_runner(tmp_path)

    with pytest.raises(RuntimeError, match=message):
        run_pilot(
            tmp_path / "out",
            marker=MARKER,
            ocr=ocr,
            ingest=lambda pdf: IngestReceipt("abc123", pdf.name, True, pipeline=pipeline),
            search=search,
            trace=lambda: trace_status,
            pdf_factory=lambda path, marker: path.write_bytes(b"%PDF-1.7"),
            poll_interval_seconds=0,
        )


def test_run_pilot_fails_closed_when_ocr_marker_is_missing(tmp_path: Path) -> None:
    ocr, ingest, search, trace = _fixture_runner(tmp_path)
    ocr = lambda pdf: {"text": "wrong", "parser": "mineru", "mode": "ocr"}
    with pytest.raises(RuntimeError, match="OCR marker"):
        run_pilot(
            tmp_path / "out",
            marker=MARKER,
            ocr=ocr,
            ingest=ingest,
            search=search,
            trace=trace,
            pdf_factory=lambda path, marker: path.write_bytes(b"%PDF-1.7"),
            poll_interval_seconds=0,
        )


def test_citation_evaluator_rejects_unretrieved_file() -> None:
    hits = [SearchHit(file_md5="abc", file_name="a.pdf", chunk_id=1, text="x", score=1.0, citation_key="doc#p0#e0")]
    report = evaluate_citations(
        [Citation(citation_key="other#p0#e0", document_id="other", page_id="other:p0", element_ids=["other:p0:e0"], bbox_refs=["other:p0:e0:0,0,1,1"])],
        hits,
    )
    assert report["passed"] is False
    assert report["unsupported"] == 1
    assert report["precision"] == 0.0
    assert report["recall"] == 0.0


@pytest.mark.parametrize(
    ("ocr_result", "receipt", "hits", "message"),
    [
        ({"text": MARKER, "parser": "tika", "mode": "ocr"}, IngestReceipt("abc123", "pilot.pdf", True), [SearchHit("abc123", "pilot.pdf", 0, MARKER)], "Tika"),
        ({"text": MARKER, "parser": "mineru", "mode": "ocr"}, IngestReceipt("", "pilot.pdf", False), [], "ingest"),
        ({"text": MARKER, "parser": "mineru", "mode": "ocr"}, IngestReceipt("abc123", "pilot.pdf", True), [SearchHit("different", "pilot.pdf", 0, MARKER)], "fileMd5"),
        ({"text": MARKER, "parser": "mineru", "mode": "ocr"}, IngestReceipt("abc123", "pilot.pdf", True), [SearchHit("abc123", "pilot.pdf", 0, "unrelated chunk")], "fileMd5"),
    ],
)
def test_run_pilot_rejects_invalid_pipeline_state(tmp_path: Path, ocr_result, receipt, hits, message: str) -> None:
    content_list = tmp_path / "content_list.json"
    middle = tmp_path / "middle.json"
    content_list.write_text(json.dumps([{"type": "text", "text": MARKER, "bbox": [0, 0, 10, 10], "page_idx": 0}]), encoding="utf-8")
    middle.write_text(json.dumps({"_version_name": "3.4.4", "_backend": "pipeline"}), encoding="utf-8")
    ocr_result = {**ocr_result, "content_list_path": str(content_list), "middle_path": str(middle)}
    if receipt.accepted and receipt.file_md5:
        receipt = IngestReceipt(
            receipt.file_md5,
            receipt.file_name,
            receipt.accepted,
            receipt.object_url,
            pipeline={"verified": True, "stages": [{"stage": "index", "status": "SUCCESS"}]},
        )
    with pytest.raises(RuntimeError, match=message):
        run_pilot(
            tmp_path / "out",
            marker=MARKER,
            ocr=lambda _: ocr_result,
            ingest=lambda _: receipt,
            search=lambda _: hits,
            trace=lambda: TraceStatus(True, "fixture", "a" * 32, verified=True),
            pdf_factory=lambda path, marker: path.write_bytes(b"%PDF-1.7"),
            poll_timeout_seconds=0,
            poll_interval_seconds=0,
        )


def test_verify_artifact_fails_after_evidence_tamper(tmp_path: Path) -> None:
    ocr, ingest, search, trace = _fixture_runner(tmp_path)
    result = run_pilot(
        tmp_path / "out",
        marker=MARKER,
        ocr=ocr,
        ingest=ingest,
        search=search,
        trace=trace,
        pdf_factory=lambda path, marker: path.write_bytes(b"%PDF-1.7"),
        poll_interval_seconds=0,
    )
    (result.output_dir / "evidence.json").write_text("{}\n", encoding="utf-8")
    with pytest.raises(RuntimeError, match="checksum mismatch"):
        verify_artifact(result.output_dir)


def test_powershell_wrapper_invokes_pilot_module() -> None:
    wrapper = Path(__file__).parents[1] / "scripts" / "multimodal-rag-pilot.ps1"
    source = wrapper.read_text(encoding="utf-8")
    assert "orchestrator.rag.multimodal_pilot" in source
    assert "CODE_AGENT_RAG_INTERNAL_SECRET" in source
    assert "--internal-token" not in source
    assert "docker compose down" not in source
    assert "docker compose rm" not in source
    assert "docker volume" not in source


def test_powershell_wrapper_uses_a_fresh_explicit_run_id() -> None:
    wrapper = Path(__file__).parents[1] / "scripts" / "multimodal-rag-pilot.ps1"
    source = wrapper.read_text(encoding="utf-8")
    assert "[Guid]::NewGuid()" in source
    assert "--run-id $runId" in source
    assert '$marker = "MINERU REAL OCR 20260729"' in source


def test_powershell_wrapper_reuses_scoped_runtime_secret_helpers() -> None:
    wrapper = Path(__file__).parents[1] / "scripts" / "multimodal-rag-pilot.ps1"
    source = wrapper.read_text(encoding="utf-8")
    assert 'rag-agent-e2e-runtime.ps1' in source
    assert "$internalSecret = Resolve-InternalSecret" in source
    assert "Invoke-WithPaismartInternalToken -Secret $internalSecret" in source
    assert "Invoke-WithOrchestratorSharedSecret -Secret $internalSecret" in source
    assert '$env:ORCHESTRATOR_SHARED_SECRET = $internalSecret' not in source
    assert '$env:PAISMART_INTERNAL_TOKEN = $internalSecret' not in source


def test_powershell_wrapper_starts_only_the_approved_docker_services() -> None:
    wrapper = Path(__file__).parents[1] / "scripts" / "multimodal-rag-pilot.ps1"
    source = wrapper.read_text(encoding="utf-8")
    compose_lines = [line.strip() for line in source.splitlines() if "docker compose up -d" in line]
    assert compose_lines == [
        "& docker compose up -d mysql redis minio minio-init zookeeper kafka kafka-init es embedding phoenix"
    ]
    assert "docker compose up -d tika" not in source
    assert "codeagent-tika" not in source
    assert "127.0.0.1:9998" not in source


def test_powershell_wrapper_owns_only_services_it_starts() -> None:
    wrapper = Path(__file__).parents[1] / "scripts" / "multimodal-rag-pilot.ps1"
    source = wrapper.read_text(encoding="utf-8")
    assert 'Test-HttpEndpoint "http://127.0.0.1:8090/healthz"' in source
    assert 'Test-HttpEndpoint "$ServerUrl/healthz"' in source
    assert "$workerStartedHere = $true" in source
    assert "$serverStartedHere = $true" in source
    assert "if ($workerStartedHere -and $null -ne $workerProcess)" in source
    assert "if ($serverStartedHere -and $null -ne $serverProcess)" in source
    assert "Stop-Process -Id $workerProcess.Id" in source
    assert "Stop-Process -Id $serverProcess.Id" in source


def test_powershell_wrapper_waits_for_every_required_dependency() -> None:
    wrapper = Path(__file__).parents[1] / "scripts" / "multimodal-rag-pilot.ps1"
    source = wrapper.read_text(encoding="utf-8")
    for dependency in (
        "codeagent-mysql",
        "codeagent-redis",
        "codeagent-minio",
        "codeagent-minio-init",
        "codeagent-zookeeper",
        "codeagent-kafka",
        "codeagent-kafka-init",
        "codeagent-es",
        "codeagent-embedding",
        "codeagent-phoenix",
    ):
        assert dependency in source
    for endpoint in (
        "http://127.0.0.1:9000/minio/health/live",
        "http://127.0.0.1:9200/_cluster/health",
        "http://127.0.0.1:8009/health",
        "$PhoenixUrl/healthz",
    ):
        assert endpoint in source
    assert "Wait-Until" in source


def test_powershell_wrapper_retains_redacted_logs_only_on_failure() -> None:
    wrapper = Path(__file__).parents[1] / "scripts" / "multimodal-rag-pilot.ps1"
    source = wrapper.read_text(encoding="utf-8")
    assert '.Replace($internalSecret, "[REDACTED]")' in source
    assert "if ($completed)" in source
    assert "if (-not $completed)" in source
    assert "temporary logs retained" in source
    assert "Remove-Item -LiteralPath $serverExecutable" in source
    assert "Remove-Item -LiteralPath $serverStdout, $serverStderr, $workerStdout, $workerStderr" in source
    assert "Write-Host \"artifact: $artifactDir\"" in source


def test_powershell_wrapper_returns_nonzero_without_environment_secret(tmp_path: Path) -> None:
    wrapper = Path(__file__).parents[1] / "scripts" / "multimodal-rag-pilot.ps1"
    env = os.environ.copy()
    env.pop("CODE_AGENT_RAG_INTERNAL_SECRET", None)
    result = subprocess.run(
        [
            "powershell.exe",
            "-NoProfile",
            "-ExecutionPolicy",
            "Bypass",
            "-File",
            str(wrapper),
            "-Output",
            str(tmp_path / "artifact"),
            "-UserId",
            "1",
            "-OrgTag",
            "contract",
            "-StartupTimeoutSeconds",
            "1",
        ],
        cwd=Path(__file__).parents[1],
        env=env,
        capture_output=True,
        text=True,
        timeout=30,
        check=False,
    )
    assert result.returncode != 0
    assert "CODE_AGENT_RAG_INTERNAL_SECRET must be set" in result.stderr


def test_cli_runs_explicit_subprocess_against_injected_services(tmp_path: Path) -> None:
    marker = "MM_PILOT_SUBPROCESS_20260813"
    fake_mineru = tmp_path / "fake_mineru.py"
    fake_mineru.write_text(
        """import json, pathlib, sys
args = sys.argv[1:]
assert args[args.index('-m') + 1] == 'ocr'
out = pathlib.Path(args[args.index('-o') + 1]) / 'document' / 'ocr'
out.mkdir(parents=True)
(out / 'document_content_list.json').write_text(json.dumps([{'type':'text','text':'MM_PILOT_SUBPROCESS_20260813','bbox':[0,0,10,10],'page_idx':0}]))
(out / 'document_middle.json').write_text(json.dumps({'version':'fixture','backend':'pipeline'}))
""",
        encoding="utf-8",
    )
    requests: list[bytes] = []

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_args) -> None:
            return None

        def do_GET(self) -> None:
            if self.path.startswith("/internal/orchestrator/pipeline-status"):
                body = json.dumps({"runId": "multimodal-pilot-MM_PILOT_SUBPROCESS_20260813", "fileMd5": "fixture-md5", "complete": True, "stages": [{"stage": stage, "status": "SUCCESS"} for stage in ("parse", "chunk", "embed", "index")]}).encode()
            else:
                body = json.dumps({"data": [{"name": "retrieve orchestrator /knowledge-search", "context": {"trace_id": "a" * 32, "span_id": "b" * 16}, "status_code": "UNSET", "attributes": {"eval.run_id": "multimodal-pilot-MM_PILOT_SUBPROCESS_20260813", "rag.query_hash": "ABC", "rag.top_n": 8, "rag.retrieval_mode": "bm25"}}]}).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def do_POST(self) -> None:
            length = int(self.headers.get("Content-Length", "0"))
            request_body = self.rfile.read(length)
            requests.append(request_body)
            if self.path.endswith("knowledge-ingest"):
                data = {"fileMd5": "fixture-md5", "fileName": "image-only.pdf", "objectUrl": "https://storage.invalid/object?X-Amz-Signature=must-not-persist"}
            else:
                document_id = "fixture-source@0123456789abcdef0123456789abcdef01234567:tests/fixtures/fixture.pdf"
                element_id = document_id + ":p0:e0"
                data = {"results": [{"fileMd5": "fixture-md5", "fileName": "image-only.pdf", "chunkId": 7, "textContent": marker, "score": 1.0, "documentId": document_id, "pageId": document_id + ":p0", "elementIds": [element_id], "bboxRefs": [element_id + ":0,0,10,10"], "citationKey": document_id + "#" + document_id + ":p0#" + element_id}]}
            body = json.dumps({"code": 200, "message": "ok", "data": data}).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    output = tmp_path / "artifact"
    input_pdf = tmp_path / "fixture.pdf"
    input_pdf.write_bytes(b"%PDF-1.7\nfixture")
    try:
        result = subprocess.run(
            [
                sys.executable,
                "-m",
                "orchestrator.rag.multimodal_pilot",
                "--output",
                str(output),
                "--marker",
                marker,
                "--input-pdf",
                str(input_pdf),
                "--server-url",
                f"http://127.0.0.1:{server.server_port}",
                "--user-id",
                "1",
                "--org-tag",
                "fixture",
                "--source-id",
                "fixture-source",
                "--source-path",
                "tests/fixtures/fixture.pdf",
                "--source-url",
                "https://example.invalid/repo/blob/0123456789abcdef0123456789abcdef01234567/tests/fixtures/fixture.pdf",
                "--source-commit",
                "0123456789abcdef0123456789abcdef01234567",
                "--mineru-command",
                sys.executable,
                "--mineru-prefix-arg",
                str(fake_mineru),
                "--phoenix-url",
                f"http://127.0.0.1:{server.server_port}",
                "--poll-timeout-seconds",
                "1",
            ],
            cwd=Path(__file__).parents[1],
            env={**os.environ, "CODE_AGENT_RAG_INTERNAL_SECRET": "fixture-secret"},
            capture_output=True,
            text=True,
            timeout=30,
            check=False,
        )
    finally:
        server.shutdown()
        server.server_close()

    assert result.returncode == 0, result.stdout + result.stderr
    assert json.loads((output / "summary.json").read_text())["status"] == "PASS"
    trace = json.loads((output / "manifest.json").read_text())["trace"]
    assert trace["available"] is True
    assert trace["verified"] is True
    assert trace["trace_id"] == "a" * 32
    assert "fixture-secret" not in "".join(path.read_text(errors="ignore") for path in output.glob("*.json"))
    assert "X-Amz-Signature" not in "".join(path.read_text(errors="ignore") for path in output.glob("*.json"))
    checksums = json.loads((output / "checksums.json").read_text())
    assert "image-only.pdf" in checksums
    ingest_body = requests[0]
    search_body = json.loads(requests[1])
    for field in (b"sourceId", b"sourcePath", b"sourceUrl", b"sourceCommit", b"sourceSha256", b"targetIndex", b"corpusGeneration", b"runId"):
        assert b'name="' + field + b'"' in ingest_body
    assert b"techdocs-2026-07-30-v1" in ingest_body
    assert b"knowledge_base_v2_bge_m3" in ingest_body
    assert search_body["runId"] == "multimodal-pilot-MM_PILOT_SUBPROCESS_20260813"
    manifest = json.loads((output / "manifest.json").read_text())
    assert manifest["source"]["corpus_generation"] == "techdocs-2026-07-30-v1"
    assert manifest["source"]["target_index"] == "knowledge_base_v2_bge_m3"
    assert manifest["pipeline"]["verified"] is True
    assert [item["stage"] for item in manifest["pipeline"]["stages"]] == ["parse", "chunk", "embed", "index"]


@pytest.mark.parametrize(
    "payload",
    [
        {"runId": "wrong-run", "fileMd5": "fixture-md5", "complete": True, "stages": [{"stage": stage, "status": "SUCCESS"} for stage in ("parse", "chunk", "embed", "index")]},
        {"runId": "expected-run", "fileMd5": "wrong-md5", "complete": True, "stages": [{"stage": stage, "status": "SUCCESS"} for stage in ("parse", "chunk", "embed", "index")]},
        {"runId": "expected-run", "fileMd5": "fixture-md5", "complete": True, "stages": [{"stage": "index", "status": "SUCCESS"}]},
        {"runId": "expected-run", "fileMd5": "fixture-md5", "complete": True, "stages": [{"stage": stage, "status": "SUCCESS"} for stage in ("parse", "chunk", "embed", "index", "index")]},
        {"runId": "expected-run", "fileMd5": "fixture-md5", "complete": True, "stages": [{"stage": stage, "status": "SUCCESS" if stage != "embed" else "PROCESSING"} for stage in ("parse", "chunk", "embed", "index")]},
    ],
)
def test_http_pipeline_adapter_rejects_wrong_scope_or_incomplete_stages(monkeypatch, payload) -> None:
    from orchestrator.rag.multimodal_pilot import HTTPPilotServices

    service = HTTPPilotServices(
        "http://127.0.0.1:8081", "secret", 1, "org", "http://127.0.0.1:6006",
        source_id="source", source_path="fixture.pdf", source_url="https://example.invalid/fixture.pdf",
        source_commit="0" * 40, target_index="index", corpus_generation="generation", run_id="expected-run",
    )

    class Response:
        def __enter__(self): return self
        def __exit__(self, *_args): return False
        def read(self): return json.dumps(payload).encode()

    monkeypatch.setattr("urllib.request.urlopen", lambda *_args, **_kwargs: Response())
    with pytest.raises(RuntimeError, match="pipeline status"):
        service._wait_pipeline("fixture-md5")
