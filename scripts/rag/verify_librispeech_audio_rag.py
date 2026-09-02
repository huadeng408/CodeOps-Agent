"""Run a real, reproducible LibriSpeech audio-RAG integration receipt.

The source manifest carries only hashes of independent reference transcripts
from the pinned LibriSpeech dataset.  The runner reads the transcript from the
version-pinned upstream response at runtime, verifies its hash, and never uses
ASR output as a reference.  It also never creates or switches an Elasticsearch
alias.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import sys
import urllib.error
import urllib.parse
import urllib.request
import wave
from collections.abc import Mapping
from pathlib import Path, PureWindowsPath
from typing import Any

ROOT = Path(__file__).parents[2]
sys.path.insert(0, str(ROOT))

from orchestrator.rag.audio import (
    AudioChunk,
    audio_chunks_to_evidence,
    compute_word_error_rate,
    group_transcript_segments,
    transcribe_audio,
    write_audio_evidence_manifest,
)

SOURCE_API = "https://huggingface.co/api/datasets/openslr/librispeech_asr"
ROWS_API = "https://datasets-server.huggingface.co/first-rows"
DEFAULT_MANIFEST = ROOT / "data" / "eval" / "multimodal" / "audio" / "librispeech-test-v1" / "manifest.json"
_SHA256 = set("0123456789abcdef")


def _text_sha256(value: str) -> str:
    """Hash the normalized transcript representation used by the source pin."""
    return hashlib.sha256(value.strip().encode("utf-8")).hexdigest()


def _is_safe_audio_row_id(value: str) -> bool:
    """Accept dataset IDs that remain a single filename on both host OSes."""
    if not value or value in {".", ".."} or any(char in value for char in "/\\\x00:"):
        return False
    windows_path = PureWindowsPath(value)
    return (
        not Path(value).is_absolute()
        and not windows_path.is_absolute()
        and not windows_path.drive
        and windows_path.name == value
    )


def validate_source_manifest(manifest: Mapping[str, Any]) -> tuple[list[dict[str, Any]], str]:
    """Validate rows without allowing gold transcript text in the manifest."""
    if manifest.get("schema_version") != "audio-source-manifest/v1":
        raise ValueError("audio source manifest schema_version is invalid")
    dataset_id = manifest.get("dataset_id")
    revision = manifest.get("dataset_revision")
    if dataset_id != "openslr/librispeech_asr":
        raise ValueError("audio source manifest dataset_id is not LibriSpeech")
    if not isinstance(revision, str) or len(revision) != 40 or set(revision.lower()) - _SHA256:
        raise ValueError("audio source manifest dataset_revision must be a 40-character commit")
    license_value = manifest.get("license")
    if not isinstance(license_value, str):
        raise ValueError("audio source manifest license must be a string")  # noqa: TRY004
    license_name = license_value.strip()
    if license_name.upper() != "CC-BY-4.0":
        raise ValueError("audio source manifest requires an explicit CC-BY-4.0 license")
    if manifest.get("config") != "clean" or manifest.get("split") != "test":
        raise ValueError("audio source manifest must pin the clean/test split")
    rows = manifest.get("rows")
    if not isinstance(rows, list) or not rows:
        raise ValueError("audio source manifest rows are required")
    validated: list[dict[str, Any]] = []
    seen: set[str] = set()
    for row in rows:
        if not isinstance(row, Mapping):
            raise ValueError("audio source manifest row must be an object")  # noqa: TRY004
        row_id_value = row.get("id")
        reference_digest = row.get("reference_text_sha256")
        query_value = row.get("query_text")
        if not isinstance(row_id_value, str):
            raise ValueError("audio source manifest row id must be a string")  # noqa: TRY004
        if "reference_text" in row:
            raise ValueError("audio source manifest must not include reference_text")
        if not isinstance(reference_digest, str):
            raise ValueError(  # noqa: TRY004
                f"audio source manifest reference_text_sha256 must be a string for {row_id_value}"
            )
        if not isinstance(query_value, str):
            raise ValueError(  # noqa: TRY004
                f"audio source manifest query_text must be a string for {row_id_value}"
            )
        row_id = row_id_value.strip()
        digest = reference_digest.strip().lower()
        query = query_value.strip()
        row_index = row.get("row_index")
        if not row_id or row_id in seen:
            raise ValueError("audio source manifest row IDs must be non-empty and unique")
        if not _is_safe_audio_row_id(row_id):
            raise ValueError(f"audio source manifest row id is not a safe filename: {row_id}")
        if len(digest) != 64 or set(digest) - _SHA256:
            raise ValueError(f"audio source manifest reference_text_sha256 is invalid for {row_id}")
        if not query:
            raise ValueError(f"audio source manifest query_text is missing for {row_id}")
        if not isinstance(row_index, int) or isinstance(row_index, bool) or row_index < 0:
            raise ValueError(f"audio source manifest row_index is invalid for {row_id}")
        seen.add(row_id)
        validated.append(
            {
                "row_index": row_index,
                "id": row_id,
                "reference_text_sha256": digest,
                "query_text": query,
            }
        )
    reference_source_value = manifest.get("reference_source", "LibriSpeech dataset transcript")
    if not isinstance(reference_source_value, str):
        raise ValueError(  # noqa: TRY004
            "audio source manifest reference_source must be a string"
        )
    reference_source = reference_source_value.strip()
    if not reference_source:
        raise ValueError("audio source manifest reference_source is required")
    return validated, reference_source


def concatenate_wavs(paths: list[Path], output: Path) -> list[tuple[int, int]]:
    """Concatenate compatible WAV payloads and return each source time range."""
    if not paths:
        raise ValueError("at least one WAV input is required")
    output.parent.mkdir(parents=True, exist_ok=True)
    params = None
    frames: list[bytes] = []
    ranges: list[tuple[int, int]] = []
    cursor = 0
    for path in paths:
        with wave.open(str(path), "rb") as handle:
            current = handle.getparams()
            if params is None:
                params = current
            elif (
                current.nchannels,
                current.sampwidth,
                current.framerate,
                current.comptype,
            ) != (
                params.nchannels,
                params.sampwidth,
                params.framerate,
                params.comptype,
            ):
                raise ValueError("all WAV inputs must have matching channels, width, rate and compression")
            payload = handle.readframes(handle.getnframes())
            frames.append(payload)
            start_ms = round(cursor * 1000 / params.framerate)
            cursor += handle.getnframes()
            end_ms = round(cursor * 1000 / params.framerate)
            ranges.append((start_ms, end_ms))
    assert params is not None
    with wave.open(str(output), "wb") as handle:
        handle.setparams(params)
        for payload in frames:
            handle.writeframes(payload)
    return ranges


def build_audio_qrels(rows: list[Mapping[str, Any]], chunks: list[AudioChunk]) -> list[dict[str, Any]]:
    """Bind each reference range to one unique chunk by maximum overlap.

    Dataset audio can include trailing silence that Faster-Whisper does not
    timestamp. Requiring full containment would reject valid speech evidence;
    a tie between overlapping chunks remains fail-closed.
    """
    qrels: list[dict[str, Any]] = []
    for row in rows:
        start_ms = int(row["start_ms"])
        end_ms = int(row["end_ms"])
        if end_ms <= start_ms:
            raise ValueError(f"audio qrel range is invalid: {row['id']}")
        scored = []
        for chunk in chunks:
            overlap_ms = max(0, min(end_ms, chunk.end_ms) - max(start_ms, chunk.start_ms))
            if overlap_ms > 0:
                scored.append((overlap_ms, chunk))
        if not scored:
            raise ValueError(f"audio qrel range does not map to exactly one chunk: {row['id']}")
        best_overlap = max(item[0] for item in scored)
        matches = [chunk for overlap_ms, chunk in scored if overlap_ms == best_overlap]
        if len(matches) != 1:
            raise ValueError(f"audio qrel range maps ambiguously to chunks: {row['id']}")
        chunk = matches[0]
        qrels.append(
            {
                "query_id": str(row["id"]),
                "query_text": str(row["query_text"]),
                "audio_id": chunk.audio_id,
                "start_ms": start_ms,
                "end_ms": end_ms,
                "overlap_ms": best_overlap,
                "expected_chunk_id": f"{chunk.audio_id}:audio:{chunk.start_ms}-{chunk.end_ms}",
                "relevance": 1,
                "label_source": "openslr/librispeech_asr:independent-reference-transcript",
                "review_status": "DESIGNED_PUBLIC_DEV",
            }
        )
    return qrels


def score_retrieval_rows(rows: list[Mapping[str, Any]]) -> dict[str, float]:
    """Report transparent rank metrics for development rows only."""
    if not rows:
        return {"recall_at_1": 0.0, "recall_at_10": 0.0, "mrr_at_10": 0.0}
    top_one = 0
    top_ten = 0
    reciprocal_sum = 0.0
    for row in rows:
        expected = str(row.get("expected_chunk_id", ""))
        returned = [str(item) for item in row.get("returned_chunk_ids", [])]
        if returned and returned[0] == expected:
            top_one += 1
        try:
            rank = returned[:10].index(expected) + 1
        except ValueError:
            rank = 0
        if rank:
            top_ten += 1
            reciprocal_sum += 1.0 / rank
    count = float(len(rows))
    return {
        "recall_at_1": top_one / count,
        "recall_at_10": top_ten / count,
        "mrr_at_10": reciprocal_sum / count,
    }


def _request_json(
    url: str,
    *,
    timeout: int = 30,
    request_log: list[dict[str, Any]] | None = None,
) -> Mapping[str, Any]:
    request = urllib.request.Request(url, headers={"Accept": "application/json"})
    with urllib.request.urlopen(request, timeout=timeout) as response:
        raw_response = response.read()
        status = getattr(response, "status", None)
    if request_log is not None:
        request_bytes = (request.get_method() + "\n" + url + "\n").encode("utf-8")
        request_log.append(
            {
                "method": request.get_method(),
                "url": url,
                "request_sha256": hashlib.sha256(request_bytes).hexdigest(),
                "response_sha256": hashlib.sha256(raw_response).hexdigest(),
                "response_bytes": len(raw_response),
                "status": status,
            }
        )
    payload = json.loads(raw_response.decode("utf-8"))
    if not isinstance(payload, Mapping):
        raise ValueError(f"expected JSON object from {url}")  # noqa: TRY004
    return payload


def validate_source_readback(
    source: Mapping[str, Any], *, revision: str, license_name: str
) -> None:
    """Require the live dataset metadata to confirm the local source pin."""
    if source.get("sha") != revision:
        raise ValueError("LibriSpeech source revision readback mismatch")
    card_data = source.get("cardData")
    licenses = card_data.get("license") if isinstance(card_data, Mapping) else None
    if isinstance(licenses, str):
        licenses = [licenses]
    normalized = {
        str(item).strip().lower()
        for item in licenses
        if isinstance(item, str) and item.strip()
    } if isinstance(licenses, list) else set()
    if license_name.strip().lower() not in normalized:
        raise ValueError("LibriSpeech source license readback mismatch")


def _fetch_rows(
    manifest: Mapping[str, Any],
    rows: list[dict[str, Any]],
    *,
    request_log: list[dict[str, Any]] | None = None,
) -> dict[str, Mapping[str, Any]]:
    def fetch_json(url: str) -> Mapping[str, Any]:
        # Keep the no-log call shape compatible with callers that replace the
        # transport in deterministic tests.
        if request_log is None:
            return _request_json(url)
        return _request_json(url, request_log=request_log)

    revision = str(manifest["dataset_revision"])
    source = fetch_json(f"{SOURCE_API}/revision/{revision}")
    validate_source_readback(source, revision=revision, license_name=str(manifest["license"]))
    query = urllib.parse.urlencode(
        {
            "dataset": manifest["dataset_id"],
            "config": manifest["config"],
            "split": manifest["split"],
            "revision": revision,
            "offset": min(int(row["row_index"]) for row in rows),
            "length": max(int(row["row_index"]) for row in rows) + 1,
        }
    )
    payload = fetch_json(f"{ROWS_API}?{query}")
    found: dict[str, Mapping[str, Any]] = {}
    for item in payload.get("rows", []):
        row = item.get("row", {}) if isinstance(item, Mapping) else {}
        if isinstance(row, Mapping) and str(row.get("id", "")) in {str(x["id"]) for x in rows}:
            found[str(row["id"])] = row
    if set(found) != {str(row["id"]) for row in rows}:
        raise ValueError("LibriSpeech metadata response did not contain every pinned row")
    for row in rows:
        upstream = found[str(row["id"])]
        reference = str(upstream.get("text", "")).strip()
        if not reference or _text_sha256(reference) != row["reference_text_sha256"]:
            raise ValueError(f"reference transcript mismatch for {row['id']}")
    return found


def _download(url: str, path: Path) -> None:
    request = urllib.request.Request(url, headers={"Accept": "audio/wav"})
    with urllib.request.urlopen(request, timeout=60) as response:
        payload = response.read()
    if not payload:
        raise ValueError(f"empty audio response for {path.name}")
    path.write_bytes(payload)


def _es_request(base_url: str, method: str, path: str, payload: Any | None = None) -> Mapping[str, Any]:
    body = None if payload is None else json.dumps(payload, ensure_ascii=False).encode("utf-8")
    request = urllib.request.Request(base_url.rstrip("/") + path, data=body, method=method)
    if body is not None:
        request.add_header("Content-Type", "application/json")
    with urllib.request.urlopen(request, timeout=60) as response:
        payload = json.loads(response.read().decode("utf-8") or "{}")
    if not isinstance(payload, Mapping):
        raise ValueError(  # noqa: TRY004
            f"invalid Elasticsearch response for {method} {path}"
        )
    return payload


def _index_mapping() -> dict[str, Any]:
    return {
        "mappings": {
            "properties": {
                "text_content": {"type": "text"},
                "embedding_text": {"type": "text"},
                "vector_id": {"type": "keyword"},
                "document_id": {"type": "keyword"},
                "source_sha256": {"type": "keyword"},
                "modality": {"type": "keyword"},
                "start_ms": {"type": "integer"},
                "end_ms": {"type": "integer"},
                "parent_chunk_id": {"type": "keyword"},
            }
        }
    }


def _write_json(path: Path, payload: Any) -> None:
    path.write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


def _write_checksums(root: Path, paths: tuple[Path, ...] | list[Path]) -> Path:
    """Write sorted SHA-256 pins for the supplied artifact tree entries."""
    root = root.resolve()
    expanded: dict[str, Path] = {}
    for path in paths:
        resolved = path.resolve()
        try:
            resolved.relative_to(root)
        except ValueError as exc:
            raise ValueError(f"checksum path is outside output directory: {path}") from exc
        candidates = resolved.rglob("*") if resolved.is_dir() else (resolved,)
        for candidate in candidates:
            if not candidate.is_file():
                continue
            relative = candidate.relative_to(root).as_posix()
            if relative == "checksums.sha256":
                continue
            expanded[relative] = candidate
    lines = [
        f"{hashlib.sha256(expanded[relative].read_bytes()).hexdigest()}  {relative}"
        for relative in sorted(expanded)
    ]
    checksum_path = root / "checksums.sha256"
    checksum_path.write_text("\n".join(lines) + ("\n" if lines else ""), encoding="utf-8")
    return checksum_path


def run(
    *,
    source_manifest_path: Path,
    output_dir: Path,
    cache_dir: Path,
    es_url: str,
    index: str,
    model: str,
    model_revision: str,
    device: str,
    compute_type: str,
) -> dict[str, Any]:
    if output_dir.exists():
        raise FileExistsError(f"output directory already exists: {output_dir}")
    manifest = json.loads(source_manifest_path.read_text(encoding="utf-8"))
    rows, reference_source = validate_source_manifest(manifest)
    output_dir.mkdir(parents=True)
    source_dir = output_dir / "source"
    source_http_requests: list[dict[str, Any]] = []
    upstream = _fetch_rows(manifest, rows, request_log=source_http_requests)
    downloaded: list[Path] = []
    for row in rows:
        path = source_dir / f"{row['id']}.wav"
        path.parent.mkdir(parents=True, exist_ok=True)
        audio = upstream[row["id"]].get("audio")
        source_url = audio[0].get("src") if isinstance(audio, list) and audio else ""
        if not isinstance(source_url, str) or not source_url:
            raise ValueError(f"missing audio URL for {row['id']}")
        _download(source_url, path)
        downloaded.append(path)

    combined = source_dir / "combined.wav"
    ranges = concatenate_wavs(downloaded, combined)
    prepared_rows: list[dict[str, Any]] = []
    for row, (start_ms, end_ms), path in zip(rows, ranges, downloaded):
        prepared = dict(row)
        prepared.update(
            {
                "reference_text": str(upstream[row["id"]].get("text", "")).strip(),
                "start_ms": start_ms,
                "end_ms": end_ms,
                "audio_sha256": hashlib.sha256(path.read_bytes()).hexdigest(),
            }
        )
        prepared_rows.append(prepared)
    reference_path = output_dir / "reference.txt"
    reference_path.write_text(" ".join(row["reference_text"] for row in prepared_rows) + "\n", encoding="utf-8")
    combined_sha256 = hashlib.sha256(combined.read_bytes()).hexdigest()

    segments = transcribe_audio(
        combined,
        model_id=model,
        model_revision=model_revision,
        device=device,
        compute_type=compute_type,
        cache_dir=cache_dir,
        beam_size=1,
        vad_filter=False,
    )
    chunks = group_transcript_segments(
        "librispeech-clean-test-v1",
        segments,
        max_duration_ms=60_000,
        overlap_ms=1_500,
    )
    records = audio_chunks_to_evidence(
        chunks,
        source_sha256=combined_sha256,
        parser_version="faster-whisper-1.2.1",
        model_id=model,
        model_version=model_revision,
        corpus_generation="audio-librispeech-clean-test-v1",
    )
    evidence_path = write_audio_evidence_manifest(output_dir / "audio-evidence.jsonl", records)
    wer = compute_word_error_rate(
        " ".join(segment.text for segment in segments),
        reference_path.read_text(encoding="utf-8"),
        reference_source=reference_source,
    )
    qrels = build_audio_qrels(prepared_rows, chunks)
    qrels_path = output_dir / "qrels.jsonl"
    qrels_path.write_text("".join(json.dumps(row, ensure_ascii=False, sort_keys=True) + "\n" for row in qrels), encoding="utf-8")

    _es_request(es_url, "PUT", f"/{index}", _index_mapping())
    bulk_lines: list[str] = []
    record_by_chunk_id: dict[str, Mapping[str, Any]] = {}
    for record in records:
        document = dict(record.index_document)
        child_id = record.evidence.child_id
        document["child_id"] = child_id
        record_by_chunk_id[child_id] = document
        bulk_lines.extend([json.dumps({"index": {"_index": index, "_id": document["vector_id"]}}), json.dumps(document, ensure_ascii=False)])
    bulk_request = urllib.request.Request(
        es_url.rstrip("/") + "/_bulk",
        data=("\n".join(bulk_lines) + "\n").encode("utf-8"),
        method="POST",
        headers={"Content-Type": "application/x-ndjson"},
    )
    with urllib.request.urlopen(bulk_request, timeout=60) as response:
        bulk = json.loads(response.read().decode("utf-8"))
    if bulk.get("errors"):
        raise RuntimeError("audio Elasticsearch bulk indexing returned errors")
    _es_request(es_url, "POST", f"/{index}/_refresh")

    readback_count = 0
    for document in record_by_chunk_id.values():
        payload = _es_request(es_url, "GET", f"/{index}/_doc/{urllib.parse.quote(str(document['vector_id']), safe='')}")
        source = payload.get("_source", {})
        if payload.get("found") is not True or source.get("source_sha256") != combined_sha256 or source.get("child_id") != document["child_id"]:
            raise RuntimeError(f"audio ES readback mismatch for {document['child_id']}")
        readback_count += 1

    retrieval_rows: list[dict[str, Any]] = []
    for qrel in qrels:
        result = _es_request(es_url, "POST", f"/{index}/_search", {"query": {"match": {"text_content": qrel["query_text"]}}, "size": 10, "_source": ["child_id", "start_ms", "end_ms"]})
        hits = result.get("hits", {}).get("hits", [])
        returned = [hit.get("_source", {}).get("child_id") for hit in hits]
        retrieval_rows.append({**qrel, "returned_chunk_ids": returned, "expected_returned": qrel["expected_chunk_id"] in returned})

    manifest_copy = output_dir / "source-manifest.json"
    _write_json(manifest_copy, manifest)
    receipt = {
        "status": "VERIFIED_AUDIO_WER_AND_ES_READBACK" if wer["status"] == "VERIFIED" and all(row["expected_returned"] for row in retrieval_rows) else "AUDIO_INTEGRATION_PARTIAL",
        "scope": "licensed-librispeech-audio-faster-whisper-isolated-es",
        "dataset": {"id": manifest["dataset_id"], "revision": manifest["dataset_revision"], "config": manifest["config"], "split": manifest["split"], "license": manifest["license"], "rows": len(rows)},
        "source_rows": [{"id": row["id"], "audio_sha256": row["audio_sha256"], "start_ms": row["start_ms"], "end_ms": row["end_ms"]} for row in prepared_rows],
        "combined_audio_sha256": combined_sha256,
        "reference_sha256": hashlib.sha256(reference_path.read_bytes()).hexdigest(),
        "reference_source": reference_source,
        "source_http": {
            "request_count": len(source_http_requests),
            "requests": source_http_requests,
        },
        "model": {"id": model, "revision": model_revision, "device": device, "compute_type": compute_type},
        "segments": len(segments),
        "chunks": len(chunks),
        "evidence_records": len(records),
        "wer": wer,
        "qrels_count": len(qrels),
        "retrieval": {"status": "VERIFIED" if all(row["expected_returned"] for row in retrieval_rows) else "FAILED", **score_retrieval_rows(retrieval_rows), "rows": retrieval_rows},
        "elasticsearch": {"index": index, "document_count": _es_request(es_url, "GET", f"/{index}/_count").get("count", 0), "readback_count": readback_count, "bulk_errors": False, "alias_created": False, "alias_switched": False, "physical_index_isolation": True},
        "artifacts": {"source_manifest": str(manifest_copy), "reference": str(reference_path), "evidence": str(evidence_path), "qrels": str(qrels_path)},
        "quality_status": "DEVELOPMENT_SMOKE_NOT_GOLD",
        "query_provenance": "AGENT_AUTHORED_PUBLIC_DEV_FROM_PINNED_TRANSCRIPTS",
        "not_claimed": ["speaker_diarization", "production_alias", "official_benchmark_score", "production_retrieval_quality", "human_reviewed_audio_qrels"],
    }
    receipt_path = output_dir / "receipt.json"
    _write_json(receipt_path, receipt)
    _write_checksums(
        output_dir,
        (*downloaded, combined, manifest_copy, reference_path, evidence_path, qrels_path, receipt_path),
    )
    return receipt


def main() -> int:
    parser = argparse.ArgumentParser(description="Run the pinned LibriSpeech audio RAG receipt")
    parser.add_argument("--source-manifest", type=Path, default=DEFAULT_MANIFEST)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--cache-dir", type=Path, required=True)
    parser.add_argument("--es-url", default="http://127.0.0.1:9200")
    parser.add_argument("--index", default="audio_rag_librispeech_clean_test_v1_20260828")
    parser.add_argument("--model", default="Systran/faster-whisper-tiny")
    parser.add_argument("--model-revision", default="d90ca5fe260221311c53c58e660288d3deb8d356")
    parser.add_argument("--device", choices=("cpu", "cuda"), default="cpu")
    parser.add_argument("--compute-type", default="int8")
    args = parser.parse_args()
    print(json.dumps(run(source_manifest_path=args.source_manifest.resolve(), output_dir=args.out.resolve(), cache_dir=args.cache_dir.resolve(), es_url=args.es_url, index=args.index, model=args.model, model_revision=args.model_revision, device=args.device, compute_type=args.compute_type), ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
