"""Run a bounded local Faster-Whisper evidence and quality receipt.

The script deliberately reports QUALITY_BLOCKED when no independently
licensed/reference transcript is supplied. It never derives WER from ASR text.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import platform
import sys
import time
from pathlib import Path

ROOT = Path(__file__).parents[2]
sys.path.insert(0, str(ROOT))

from orchestrator.rag.audio import (
    audio_chunks_to_evidence,
    compute_word_error_rate,
    group_transcript_segments,
    transcribe_audio,
    write_audio_evidence_manifest,
)


def run(args: argparse.Namespace) -> dict[str, object]:
    source = args.input.resolve()
    output = args.out.resolve()
    if output.exists():
        raise FileExistsError(f"output directory already exists: {output}")
    output.mkdir(parents=True)
    started = time.perf_counter()
    segments = transcribe_audio(
        source,
        model_id=args.model,
        device=args.device,
        compute_type=args.compute_type,
        cache_dir=args.cache_dir,
    )
    chunks = group_transcript_segments(
        args.audio_id,
        segments,
        max_duration_ms=args.max_duration_ms,
        overlap_ms=args.overlap_ms,
    )
    source_sha256 = hashlib.sha256(source.read_bytes()).hexdigest()
    records = audio_chunks_to_evidence(
        chunks,
        source_sha256=source_sha256,
        parser_version=args.parser_version,
        model_id=args.model,
        model_version=args.model_revision,
    )
    manifest = write_audio_evidence_manifest(output / "audio-evidence.jsonl", records)
    reference = args.reference.read_text(encoding="utf-8") if args.reference else ""
    wer = compute_word_error_rate(
        " ".join(segment.text for segment in segments),
        reference,
        reference_source=str(args.reference.resolve()) if args.reference else "",
    )
    receipt = {
        "status": "ASR_EVIDENCE_VERIFIED_QUALITY_BLOCKED" if wer["status"] != "VERIFIED" else "ASR_EVIDENCE_AND_WER_VERIFIED",
        "source_sha256": source_sha256,
        "model": {"id": args.model, "revision": args.model_revision, "device": args.device, "compute_type": args.compute_type},
        "segments": len(segments),
        "chunks": len(chunks),
        "evidence_records": len(records),
        "manifest_sha256": hashlib.sha256(manifest.read_bytes()).hexdigest(),
        "wer": wer,
        "runtime_seconds": round(time.perf_counter() - started, 3),
        "python": platform.python_version(),
        "not_claimed": ["diarization", "retrieval_recall", "production_quality"],
    }
    (output / "receipt.json").write_text(json.dumps(receipt, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    return receipt


def main() -> int:
    parser = argparse.ArgumentParser(description="Verify local audio RAG evidence")
    parser.add_argument("--input", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--cache-dir", type=Path, required=True)
    parser.add_argument("--audio-id", default="audio-source")
    parser.add_argument("--model", default="Systran/faster-whisper-tiny")
    parser.add_argument("--model-revision", default="d90ca5fe260221311c53c58e")
    parser.add_argument("--parser-version", default="1.2.1")
    parser.add_argument("--device", choices=("cpu", "cuda"), default="cpu")
    parser.add_argument("--compute-type", default="int8")
    parser.add_argument("--max-duration-ms", type=int, default=60_000)
    parser.add_argument("--overlap-ms", type=int, default=1_500)
    parser.add_argument("--reference", type=Path)
    args = parser.parse_args()
    print(json.dumps(run(args), indent=2, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
