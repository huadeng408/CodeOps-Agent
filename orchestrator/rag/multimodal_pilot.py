"""Auditable, non-gold multimodal RAG pilot orchestration.

The pilot deliberately accepts service adapters instead of reaching into the
production database or index.  This keeps fixture tests deterministic while
allowing the real MinerU/Go RAG clients to be wired by an integration wrapper.
"""

from __future__ import annotations

import hashlib
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
from argparse import ArgumentParser
from collections.abc import Mapping
from dataclasses import asdict, dataclass
from pathlib import Path
from typing import Any, Callable

from eval.rag.citations import Citation as EvalCitation
from eval.rag.citations import report as citation_report
from orchestrator.rag.elements import map_mineru_output


@dataclass(frozen=True)
class Citation:
    citation_key: str
    document_id: str
    page_id: str
    element_ids: list[str]
    bbox_refs: list[str]


@dataclass(frozen=True)
class SearchHit:
    file_md5: str
    file_name: str
    chunk_id: int
    text: str
    score: float = 0.0
    document_id: str = ""
    page_id: str = ""
    element_ids: list[str] | None = None
    bbox_refs: list[str] | None = None
    citation_key: str = ""


@dataclass(frozen=True)
class IngestReceipt:
    file_md5: str
    file_name: str
    accepted: bool
    object_url: str = ""
    pipeline: Mapping[str, Any] | None = None


@dataclass(frozen=True)
class TraceStatus:
    available: bool
    reason: str = ""
    trace_id: str = ""
    verified: bool = False


@dataclass(frozen=True)
class PilotResult:
    output_dir: Path
    manifest: dict[str, Any]


def select_phoenix_run_trace(spans: list[dict[str, Any]], run_id: str) -> TraceStatus:
    required = ("rag.query_hash", "rag.top_n", "rag.retrieval_mode")
    matching = []
    for span in spans:
        attributes = span.get("attributes")
        if not isinstance(attributes, dict) or str(attributes.get("eval.run_id", "")) != run_id:
            continue
        if span.get("name") != "retrieve orchestrator /knowledge-search":
            continue
        matching.append(span)
    trace_ids = {
        str((span.get("context") or {}).get("trace_id", ""))
        for span in matching
        if str((span.get("context") or {}).get("trace_id", ""))
    }
    if len(trace_ids) != 1:
        raise AssertionError(f"expected one Phoenix trace for run {run_id}, found {sorted(trace_ids)}")
    for span in matching:
        if str(span.get("status_code", "")).upper() == "ERROR":
            raise AssertionError(f"Phoenix retrieve span failed for run {run_id}")
        attributes = span.get("attributes") or {}
        missing = [key for key in required if key not in attributes]
        if missing:
            raise AssertionError(f"Phoenix retrieve span is missing attributes: {missing}")
    return TraceStatus(available=True, reason="run-scoped retrieve span verified", trace_id=next(iter(trace_ids)), verified=True)


def evaluate_citations(citations: list[Citation], hits: list[SearchHit]) -> dict[str, Any]:
    """Deterministically check every citation points to an actual retrieved hit."""
    hit_keys = {item.citation_key for item in hits if item.citation_key}
    unsupported = [asdict(item) for item in citations if item.citation_key not in hit_keys]
    evaluator_citations = [
        EvalCitation(
            key=item.citation_key,
            supports_claim=item.citation_key in hit_keys,
        )
        for item in citations
    ]
    required_keys = [item.citation_key for item in hits if item.citation_key]
    metrics = citation_report(evaluator_citations, required_keys)
    return {
        "passed": bool(citations) and not unsupported,
        "total": len(citations),
        "supported": metrics.supported_citations,
        "unsupported": len(unsupported),
        "unsupported_citations": unsupported,
        "precision": metrics.precision,
        "recall": metrics.recall,
    }


def run_pilot(
    output_dir: str | Path,
    *,
    marker: str,
    ocr: Callable[[Path], dict[str, Any]],
    ingest: Callable[[Path], IngestReceipt],
    search: Callable[[str], list[SearchHit]],
    trace: Callable[[], TraceStatus],
    pdf_factory: Callable[[Path, str], None],
    provenance: dict[str, str] | None = None,
    poll_timeout_seconds: float = 30.0,
    poll_interval_seconds: float = 0.5,
) -> PilotResult:
    """Run one synthetic image-only PDF pilot and atomically publish evidence."""
    marker = marker.strip()
    if not marker:
        raise ValueError("pilot marker must not be blank")
    final_dir = Path(output_dir)
    if final_dir.exists():
        raise FileExistsError(f"pilot output already exists: {final_dir}")
    final_dir.parent.mkdir(parents=True, exist_ok=True)

    with tempfile.TemporaryDirectory(prefix=f"{final_dir.name}.", dir=final_dir.parent) as temp:
        work_dir = Path(temp)
        pdf_path = work_dir / "image-only.pdf"
        pdf_factory(pdf_path, marker)
        if not pdf_path.is_file() or not pdf_path.read_bytes().startswith(b"%PDF-"):
            raise RuntimeError("pilot PDF factory did not produce a PDF")
        source_sha256 = _sha256_file(pdf_path)

        parsed = ocr(pdf_path)
        parser = str(parsed.get("parser", "")).strip().lower()
        mode = str(parsed.get("mode", "")).strip().lower()
        parsed_text = str(parsed.get("text", ""))
        if parser == "tika":
            raise RuntimeError("Tika must never be used for PDF pilot")
        if parser != "mineru" or mode != "ocr":
            raise RuntimeError("pilot requires explicit MinerU OCR")
        if marker not in parsed_text:
            raise RuntimeError("OCR marker missing from MinerU output")
        mineru_dir = work_dir / "mineru"
        mineru_dir.mkdir()
        content_list_path = mineru_dir / "content_list.json"
        middle_path = mineru_dir / "middle.json"
        _write_ocr_artifact(parsed, "content_list", content_list_path)
        _write_ocr_artifact(parsed, "middle", middle_path)

        receipt = ingest(pdf_path)
        if not receipt.accepted or not receipt.file_md5.strip():
            raise RuntimeError("RAG ingest was not accepted")
        if not isinstance(receipt.pipeline, Mapping) or receipt.pipeline.get("verified") is not True:
            raise RuntimeError("pipeline verification is required before PASS")
        verified_pipeline = dict(receipt.pipeline)
        safe_receipt = {
            "file_md5": receipt.file_md5,
            "file_name": receipt.file_name or pdf_path.name,
            "accepted": receipt.accepted,
            "pipeline": verified_pipeline,
        }
        hits = _poll_hits(search, marker, receipt.file_md5, poll_timeout_seconds, poll_interval_seconds)
        # The indexed citation namespace is authoritative for the join.  The
        # source provenance document id may be a logical URI while the current
        # structured worker uses fileMd5-derived ids for MinerU elements.
        document_id = str(hits[0].document_id).strip()
        if not document_id:
            raise RuntimeError("retrieval hit is missing stable documentId")
        if any(str(item.document_id).strip() != document_id for item in hits):
            raise RuntimeError("retrieval hits contain multiple documentId namespaces")
        elements = map_mineru_output(content_list_path, middle_path, document_id=document_id)
        ocr_element_ids = {item.element_id for item in elements}
        citations = [_citation_from_hit(item, ocr_element_ids) for item in hits[:1]]
        citation_report = evaluate_citations(citations, hits)
        if not citation_report["passed"]:
            raise RuntimeError("citation does not point to an actual retrieval hit")
        trace_status = trace()
        if not isinstance(trace_status, TraceStatus):
            raise TypeError("trace adapter must return TraceStatus")
        if trace_status.verified is not True:
            raise RuntimeError("trace verification is required before PASS")

        source_manifest = {"file_name": receipt.file_name or pdf_path.name, "file_md5": receipt.file_md5, "sha256": source_sha256}
        source_manifest.update(provenance or {})
        manifest: dict[str, Any] = {
            "schema_version": "multimodal-rag-pilot.v1",
            "pilot": True,
            "gold": False,
            "review_status": "UNREVIEWED",
            "dataset_role": "non_gold_smoke",
            "citation_quality": "NOT_EVALUATED",
            "source": source_manifest,
            "parser": {
                "name": "mineru",
                "mode": "ocr",
                "version": str(parsed.get("parser_version", "")),
                "backend": str(parsed.get("backend", "")),
            },
            "ocr_provenance": {
                "content_list": "mineru/content_list.json",
                "middle": "mineru/middle.json",
                "elements": [
                    {
                        "element_id": item.element_id,
                        "page_id": f"{item.document_id}:p{item.page_index}",
                        "page_index": item.page_index,
                        "bbox": item.bbox,
                        "type": item.type,
                    }
                    for item in elements
                ],
            },
            "trace": asdict(trace_status),
            "pipeline": verified_pipeline,
            "rollback_boundary": {
                "action": "No automatic cleanup; removal requires a later explicit operation.",
                "file_md5": receipt.file_md5,
                "file_name": receipt.file_name or pdf_path.name,
                "document_id": (provenance or {}).get("document_id", ""),
            },
        }
        evidence = {
            "query": marker,
            "hits": [asdict(item) for item in hits],
            "citations": [asdict(item) for item in citations],
            "answer": f"The pilot marker was retrieved. Citation: {citations[0].citation_key}.",
        }
        summary = {
            "status": "PASS",
            "ocr_marker": marker,
            "ingest": safe_receipt,
            "retrieval_count": len(hits),
            "retrieval_citation_wiring": citation_report,
            "citation_quality": "NOT_EVALUATED",
            "trace": asdict(trace_status),
            "pipeline": verified_pipeline,
        }
        _write_json(work_dir / "manifest.json", manifest)
        _write_json(work_dir / "summary.json", summary)
        _write_json(work_dir / "evidence.json", evidence)
        checksums = {
            name: _sha256_file(work_dir / name)
            for name in (
                "image-only.pdf",
                "manifest.json",
                "summary.json",
                "evidence.json",
                "mineru/content_list.json",
                "mineru/middle.json",
            )
        }
        _write_json(work_dir / "checksums.json", checksums)
        _verify_checksums(work_dir, checksums)
        os.replace(work_dir, final_dir)
    return PilotResult(final_dir, manifest)


def _write_ocr_artifact(parsed: dict[str, Any], name: str, destination: Path) -> None:
    data = parsed.get(f"{name}_bytes")
    if isinstance(data, bytes) and data:
        destination.write_bytes(data)
        return
    source_value = str(parsed.get(f"{name}_path", "")).strip()
    source = Path(source_value)
    if source_value and source.is_file():
        shutil.copyfile(source, destination)
        return
    raise RuntimeError(f"MinerU OCR artifact is missing: {name}")


def _citation_from_hit(hit: SearchHit, ocr_element_ids: set[str]) -> Citation:
    element_ids = list(hit.element_ids or [])
    bbox_refs = list(hit.bbox_refs or [])
    if not hit.document_id or not hit.page_id or not element_ids or not bbox_refs or not hit.citation_key:
        raise RuntimeError("retrieval hit is missing stable citation provenance")
    missing = sorted(set(element_ids) - ocr_element_ids)
    if missing:
        raise RuntimeError(f"citation elements are absent from MinerU artifacts: {missing}")
    return Citation(
        citation_key=hit.citation_key,
        document_id=hit.document_id,
        page_id=hit.page_id,
        element_ids=element_ids,
        bbox_refs=bbox_refs,
    )


def _poll_hits(search: Callable[[str], list[SearchHit]], query: str, file_md5: str, timeout: float, interval: float) -> list[SearchHit]:
    deadline = time.monotonic() + max(0.0, timeout)
    while True:
        hits = list(search(query))
        matching = [item for item in hits if item.file_md5 == file_md5 and query in item.text]
        if matching:
            return matching
        if time.monotonic() >= deadline:
            raise RuntimeError("SearchKnowledge did not hit the ingested fileMd5")
        if interval > 0:
            time.sleep(interval)


def _write_json(path: Path, payload: Any) -> None:
    path.write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


def _sha256_file(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def _verify_checksums(directory: Path, checksums: dict[str, str]) -> None:
    for name, expected in checksums.items():
        if _sha256_file(directory / name) != expected:
            raise RuntimeError(f"artifact checksum mismatch: {name}")


def verify_artifact(directory: str | Path) -> None:
    """Verify a published pilot artifact against its checksum ledger."""
    root = Path(directory)
    checksums = json.loads((root / "checksums.json").read_text(encoding="utf-8"))
    if not isinstance(checksums, dict) or not checksums:
        raise RuntimeError("artifact checksum ledger is missing or empty")
    _verify_checksums(root, {str(name): str(value) for name, value in checksums.items()})


class HTTPPilotServices:
    """Thin adapters for the existing internal Go RAG and Phoenix endpoints."""

    def __init__(
        self,
        server_url: str,
        internal_token: str,
        user_id: int,
        org_tag: str,
        phoenix_url: str,
        *,
        source_id: str,
        source_path: str,
        source_url: str,
        source_commit: str,
        target_index: str,
        corpus_generation: str,
        run_id: str,
    ) -> None:
        self.server_url = server_url.rstrip("/")
        self.internal_token = internal_token
        self.user_id = user_id
        self.org_tag = org_tag
        self.phoenix_url = phoenix_url.rstrip("/")
        self.provenance = {
            "source_id": source_id,
            "source_path": source_path,
            "source_url": source_url,
            "source_commit": source_commit,
            "target_index": target_index,
            "corpus_generation": corpus_generation,
            "run_id": run_id,
            "document_id": f"{source_id}@{source_commit}:{source_path}",
        }

    def ingest(self, pdf_path: Path) -> IngestReceipt:
        boundary = "codeagent-pilot-boundary"
        source_sha256 = _sha256_file(pdf_path)
        fields = {
            "userId": str(self.user_id),
            "orgTag": self.org_tag,
            "isPublic": "false",
            "sourceId": self.provenance["source_id"],
            "sourcePath": self.provenance["source_path"],
            "sourceUrl": self.provenance["source_url"],
            "sourceCommit": self.provenance["source_commit"],
            "sourceSha256": source_sha256,
            "targetIndex": self.provenance["target_index"],
            "corpusGeneration": self.provenance["corpus_generation"],
            "runId": self.provenance["run_id"],
        }
        body = _multipart_body(
            boundary,
            fields,
            "file",
            pdf_path.name,
            pdf_path.read_bytes(),
        )
        payload = self._request_json(
            f"{self.server_url}/internal/orchestrator/knowledge-ingest",
            body=body,
            content_type=f"multipart/form-data; boundary={boundary}",
        )
        data = payload.get("data") or {}
        file_md5 = str(data.get("fileMd5", ""))
        accepted = 200 <= int(payload.get("code", 0)) < 300
        pipeline = self._wait_pipeline(file_md5) if accepted and file_md5 else None
        return IngestReceipt(
            file_md5=file_md5,
            file_name=str(data.get("fileName", pdf_path.name)),
            object_url=str(data.get("objectUrl", "")),
            accepted=accepted,
            pipeline=pipeline,
        )

    def _wait_pipeline(self, file_md5: str) -> dict[str, Any]:
        deadline = time.monotonic() + 180
        query = urllib.parse.urlencode({"runId": self.provenance["run_id"], "fileMd5": file_md5})
        url = f"{self.server_url}/internal/orchestrator/pipeline-status?{query}"
        last_status: dict[str, Any] = {}
        while time.monotonic() < deadline:
            request = urllib.request.Request(url, headers={"X-Internal-Token": self.internal_token, "Accept": "application/json"})
            with urllib.request.urlopen(request, timeout=10) as response:
                payload = json.loads(response.read())
            if not isinstance(payload, dict):
                raise RuntimeError("pipeline status response is not an object")
            last_status = payload
            expected_run_id = self.provenance["run_id"]
            if str(payload.get("runId", "")) != expected_run_id:
                raise RuntimeError("pipeline status runId does not match current pilot run")
            if str(payload.get("fileMd5", "")) != file_md5:
                raise RuntimeError("pipeline status fileMd5 does not match ingested file")
            stages = payload.get("stages", [])
            expected_stages = ("parse", "chunk", "embed", "index")
            if not isinstance(stages, list) or len(stages) != len(expected_stages):
                raise RuntimeError("pipeline status stages are incomplete or duplicated")
            stage_names = [item.get("stage") if isinstance(item, dict) else None for item in stages]
            if stage_names != list(expected_stages):
                raise RuntimeError("pipeline status stages are incomplete or duplicated")
            if payload.get("complete") is True:
                if any(not isinstance(item, dict) or item.get("status") != "SUCCESS" for item in stages):
                    raise RuntimeError("pipeline status contains a non-SUCCESS stage")
                return {"verified": True, "run_id": self.provenance["run_id"], "file_md5": file_md5, "stages": stages}
            terminal_failures = [
                item for item in stages
                if isinstance(item, dict) and str(item.get("status", "")).upper() in {"FAILED", "ERROR"}
            ]
            if terminal_failures:
                raise RuntimeError("pipeline status contains a failed stage")
            time.sleep(1)
        raise RuntimeError(f"pipeline did not complete for current run/file: {last_status}")

    def search(self, query: str) -> list[SearchHit]:
        body = json.dumps(
            {
                "user": {"id": self.user_id, "orgTags": self.org_tag, "primaryOrg": self.org_tag},
                "query": query,
                "runId": self.provenance["run_id"],
                "topK": 8,
                "mode": "bm25",
                "disableRerank": True,
            }
        ).encode()
        payload = self._request_json(
            f"{self.server_url}/internal/orchestrator/knowledge-search",
            body=body,
            content_type="application/json",
        )
        return [
            SearchHit(
                file_md5=str(item.get("fileMd5", "")),
                file_name=str(item.get("fileName", "")),
                chunk_id=int(item.get("chunkId", 0)),
                text=str(item.get("textContent", "")),
                score=float(item.get("score", 0.0)),
                document_id=str(item.get("documentId", "")),
                page_id=str(item.get("pageId", "")),
                element_ids=[str(value) for value in item.get("elementIds", [])],
                bbox_refs=[str(value) for value in item.get("bboxRefs", [])],
                citation_key=str(item.get("citationKey", "")),
            )
            for item in (payload.get("data") or {}).get("results", [])
        ]

    def trace_status(self) -> TraceStatus:
        deadline = time.monotonic() + 45
        start_time = urllib.parse.quote(time.strftime("%Y-%m-%dT00:00:00Z", time.gmtime()))
        url = f"{self.phoenix_url}/v1/projects/default/spans?start_time={start_time}&limit=100"
        last_error = "trace not queried"
        while time.monotonic() < deadline:
            try:
                request = urllib.request.Request(url, headers={"Accept": "application/json"})
                with urllib.request.urlopen(request, timeout=10) as response:
                    payload = json.loads(response.read())
                spans = payload.get("data", []) if isinstance(payload, dict) else []
                return select_phoenix_run_trace(spans, self.provenance["run_id"])
            except (OSError, urllib.error.URLError, json.JSONDecodeError, AssertionError) as exc:
                last_error = str(exc)
                time.sleep(1)
        raise RuntimeError(f"Phoenix run-scoped trace verification failed: {last_error}")

    def _request_json(self, url: str, *, body: bytes, content_type: str) -> dict[str, Any]:
        request = urllib.request.Request(url, data=body, method="POST")
        request.add_header("Content-Type", content_type)
        request.add_header("X-Internal-Token", self.internal_token)
        with urllib.request.urlopen(request, timeout=60) as response:
            if not 200 <= response.status < 300:
                raise RuntimeError(f"service request failed with HTTP {response.status}")
            decoded = json.loads(response.read())
        if not isinstance(decoded, dict):
            raise RuntimeError("service response is not a JSON object")
        return decoded


def run_mineru_ocr(pdf_path: Path, command: str, prefix_args: list[str], backend: str) -> dict[str, Any]:
    with tempfile.TemporaryDirectory(prefix="codeagent-pilot-mineru-") as temp:
        output = Path(temp) / "output"
        completed = subprocess.run(
            [command, *prefix_args, "-p", str(pdf_path), "-o", str(output), "-m", "ocr", "-b", backend],
            capture_output=True,
            timeout=600,
            check=False,
        )
        if completed.returncode != 0:
            detail = (completed.stderr or completed.stdout).decode(errors="replace").strip()
            raise RuntimeError(f"MinerU OCR failed ({completed.returncode}): {detail}")
        candidates = sorted(output.rglob("*content_list.json"))
        if not candidates:
            raise RuntimeError("MinerU OCR produced no content_list.json")
        items = json.loads(candidates[0].read_text(encoding="utf-8"))
        middle_candidates = sorted(output.rglob("*middle.json"))
        if not middle_candidates:
            raise RuntimeError("MinerU OCR produced no middle.json")
        middle_payload = json.loads(middle_candidates[0].read_text(encoding="utf-8"))
        text = "\n".join(
            str(item.get("text") or item.get("content") or item.get("caption") or "")
            for item in items
            if isinstance(item, dict)
        )
        parser_version = ""
        resolved_backend = backend
        if isinstance(middle_payload, dict):
            parser_version = str(middle_payload.get("_version_name") or middle_payload.get("version") or "")
            resolved_backend = str(middle_payload.get("_backend") or middle_payload.get("backend") or backend)
        return {
            "text": text,
            "parser": "mineru",
            "mode": "ocr",
            "content_list_bytes": candidates[0].read_bytes(),
            "middle_bytes": middle_candidates[0].read_bytes(),
            "parser_version": parser_version,
            "backend": resolved_backend,
        }


def generate_image_only_pdf(path: Path, marker: str) -> None:
    from PIL import Image, ImageDraw, ImageFont

    image = Image.new("RGB", (1600, 2200), "white")
    draw = ImageDraw.Draw(image)
    font_path = os.getenv("MINERU_E2E_FONT", r"C:\Windows\Fonts\arialbd.ttf")
    font = ImageFont.truetype(font_path, 72)
    draw.text((100, 300), "MULTIMODAL RAG PILOT", fill="black", font=font)
    draw.text((100, 500), marker, fill="black", font=font)
    image.save(path, format="PDF", resolution=150.0)


def _multipart_body(boundary: str, fields: dict[str, str], file_field: str, file_name: str, content: bytes) -> bytes:
    parts: list[bytes] = []
    for name, value in fields.items():
        parts.append(f'--{boundary}\r\nContent-Disposition: form-data; name="{name}"\r\n\r\n{value}\r\n'.encode())
    parts.append(
        f'--{boundary}\r\nContent-Disposition: form-data; name="{file_field}"; filename="{file_name}"\r\nContent-Type: application/pdf\r\n\r\n'.encode()
        + content
        + b"\r\n"
    )
    parts.append(f"--{boundary}--\r\n".encode())
    return b"".join(parts)


def main(argv: list[str] | None = None) -> int:
    parser = ArgumentParser(description="Run an auditable non-gold multimodal RAG pilot")
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--marker", required=True)
    parser.add_argument("--input-pdf", required=True, type=Path)
    parser.add_argument("--server-url", required=True)
    parser.add_argument("--user-id", required=True, type=int)
    parser.add_argument("--org-tag", required=True)
    parser.add_argument("--source-id", required=True)
    parser.add_argument("--source-path", required=True)
    parser.add_argument("--source-url", required=True)
    parser.add_argument("--source-commit", required=True)
    parser.add_argument("--target-index", default="knowledge_base_v2_bge_m3")
    parser.add_argument("--corpus-generation", default="techdocs-2026-07-30-v1")
    parser.add_argument("--run-id", default="")
    parser.add_argument("--mineru-command", default="mineru")
    parser.add_argument("--mineru-prefix-arg", action="append", default=[])
    parser.add_argument("--mineru-backend", default="pipeline")
    parser.add_argument("--phoenix-url", default="http://127.0.0.1:6006")
    parser.add_argument("--poll-timeout-seconds", type=float, default=180.0)
    args = parser.parse_args(argv)
    internal_token = os.getenv("CODE_AGENT_RAG_INTERNAL_SECRET", "").strip()
    if not internal_token:
        parser.error("CODE_AGENT_RAG_INTERNAL_SECRET must be set")
    if not args.input_pdf.is_file():
        parser.error("--input-pdf must name an existing PDF fixture")
    source_path = args.source_path.strip()
    run_id = args.run_id.strip() or f"multimodal-pilot-{args.marker}"
    services = HTTPPilotServices(
        args.server_url,
        internal_token,
        args.user_id,
        args.org_tag,
        args.phoenix_url,
        source_id=args.source_id,
        source_path=source_path,
        source_url=args.source_url,
        source_commit=args.source_commit,
        target_index=args.target_index,
        corpus_generation=args.corpus_generation,
        run_id=run_id,
    )
    try:
        result = run_pilot(
            args.output,
            marker=args.marker,
            ocr=lambda pdf: run_mineru_ocr(pdf, args.mineru_command, args.mineru_prefix_arg, args.mineru_backend),
            ingest=services.ingest,
            search=services.search,
            trace=services.trace_status,
            pdf_factory=lambda destination, _marker: shutil.copyfile(args.input_pdf, destination),
            provenance=services.provenance,
            poll_timeout_seconds=args.poll_timeout_seconds,
        )
    except Exception as exc:  # fail-closed CLI boundary
        print(f"multimodal pilot failed: {exc}", file=sys.stderr)
        return 1
    print(f"multimodal pilot PASS: {result.output_dir}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
