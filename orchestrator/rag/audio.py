from __future__ import annotations

from dataclasses import dataclass
import json
import math
import os
from pathlib import Path
import tempfile
from typing import Any, Callable

from .evidence import EvidenceCoordinates, EvidenceUnit
from .trace import otel_span


@dataclass(frozen=True)
class TranscriptSegment:
    text: str
    start_ms: int
    end_ms: int
    confidence: float


@dataclass(frozen=True)
class AudioChunk:
    audio_id: str
    text: str
    start_ms: int
    end_ms: int

    @property
    def duration_ms(self) -> int:
        return self.end_ms - self.start_ms

    @property
    def citation(self) -> tuple[str, int, int]:
        return (self.audio_id, self.start_ms, self.end_ms)


@dataclass(frozen=True)
class AudioEvidenceRecord:
    """One timestamped audio unit and its existing text-index representation."""

    evidence: EvidenceUnit
    index_document: dict[str, Any]


def transcribe_audio(
    source: Path | str,
    *,
    model_id: str,
    device: str = "cpu",
    compute_type: str = "int8",
    cache_dir: Path | str | None = None,
    beam_size: int = 1,
    vad_filter: bool = False,
    model_factory: Callable[..., Any] | None = None,
) -> list[TranscriptSegment]:
    """Run Faster-Whisper locally and retain only timestamped ASR segments.

    The model import is deliberately lazy: document-only deployments do not
    acquire CUDA/FFmpeg dependencies until an audio task actually arrives.
    """
    audio_path = Path(source)
    if not audio_path.is_file():
        raise FileNotFoundError(f"audio source does not exist: {audio_path}")
    if not model_id.strip() or beam_size < 1:
        raise ValueError("audio transcription requires a model ID and positive beam size")
    kwargs: dict[str, Any] = {"device": device, "compute_type": compute_type}
    if cache_dir is not None:
        kwargs["download_root"] = str(cache_dir)
    if model_factory is None:
        from faster_whisper import WhisperModel

        model = WhisperModel(model_id, **kwargs)
    else:
        model = model_factory(model_id=model_id, **kwargs)
    raw_segments, _ = model.transcribe(
        str(audio_path), beam_size=beam_size, vad_filter=vad_filter
    )
    result: list[TranscriptSegment] = []
    for segment in raw_segments:
        text = str(getattr(segment, "text", "")).strip()
        start_ms = round(float(getattr(segment, "start", 0.0)) * 1000)
        end_ms = round(float(getattr(segment, "end", 0.0)) * 1000)
        if not text or end_ms <= start_ms:
            continue
        average_logprob = float(getattr(segment, "avg_logprob", 0.0))
        confidence = round(max(0.0, min(1.0, math.exp(average_logprob))), 4)
        result.append(TranscriptSegment(text, start_ms, end_ms, confidence))
    return result


def group_transcript_segments(
    audio_id: str,
    segments: list[TranscriptSegment],
    *,
    max_duration_ms: int = 60_000,
    overlap_ms: int = 1_500,
) -> list[AudioChunk]:
    """Group timestamped ASR turns without fabricating transcript overlap."""

    if not audio_id or max_duration_ms < 20_000 or max_duration_ms > 60_000:
        raise ValueError("audio chunks must use a non-empty ID and a 20-60 second limit")
    if overlap_ms < 1_000 or overlap_ms > 2_000:
        raise ValueError("audio overlap must be between one and two seconds")
    ordered = sorted((item for item in segments if item.text.strip()), key=lambda item: item.start_ms)
    chunks: list[AudioChunk] = []
    current: list[TranscriptSegment] = []
    start_ms = 0
    for segment in ordered:
        if current and segment.end_ms - start_ms > max_duration_ms:
            chunks.append(_chunk(audio_id, current, start_ms, current[-1].end_ms))
            current = []
            start_ms = chunks[-1].end_ms - overlap_ms
        if not current:
            start_ms = start_ms if chunks else segment.start_ms
        current.append(segment)
    if current:
        chunks.append(_chunk(audio_id, current, start_ms, current[-1].end_ms))
    return chunks


def _chunk(audio_id: str, segments: list[TranscriptSegment], start_ms: int, end_ms: int) -> AudioChunk:
    return AudioChunk(audio_id, " ".join(item.text.strip() for item in segments), start_ms, end_ms)


def audio_chunks_to_evidence(
    chunks: list[AudioChunk],
    *,
    source_sha256: str,
    parser_version: str,
    model_id: str,
    model_version: str,
    corpus_generation: str = "audio-asr-v1",
) -> list[AudioEvidenceRecord]:
    """Make timestamped audio chunks compatible with the shared text index.

    Audio retrieval uses ASR text for matching, while all playback provenance
    remains in `EvidenceUnit.coordinates`.  The caller owns embedding and bulk
    indexing through the established ingestion service.
    """
    if not source_sha256 or len(source_sha256) != 64:
        raise ValueError("audio evidence requires a SHA-256 source identity")
    if not parser_version or not model_id or not model_version:
        raise ValueError("audio evidence requires parser and model versions")

    records: list[AudioEvidenceRecord] = []
    for ordinal, chunk in enumerate(sorted(chunks, key=lambda item: (item.audio_id, item.start_ms, item.end_ms)), 1):
        if not chunk.audio_id or not chunk.text.strip() or chunk.start_ms < 0 or chunk.end_ms <= chunk.start_ms:
            raise ValueError("audio chunks require non-empty text and increasing timestamps")
        child_id = f"{chunk.audio_id}:audio:{chunk.start_ms}-{chunk.end_ms}"
        parent_id = f"{chunk.audio_id}:audio:transcript"
        evidence = EvidenceUnit(
            document_id=chunk.audio_id,
            child_id=child_id,
            parent_id=parent_id,
            source_sha256=source_sha256,
            parser_name="faster-whisper",
            parser_version=parser_version,
            modality="audio",
            coordinates=EvidenceCoordinates(
                asset_refs=(chunk.audio_id,),
                start_ms=chunk.start_ms,
                end_ms=chunk.end_ms,
            ),
            model_id=model_id,
            model_version=model_version,
        )
        index_document = {
            "vector_id": f"audio:{source_sha256[:16]}:{chunk.start_ms}-{chunk.end_ms}",
            "file_md5": chunk.audio_id,
            "chunk_id": ordinal,
            "text_content": chunk.text,
            "embedding_text": chunk.text,
            "document_id": chunk.audio_id,
            "source_sha256": source_sha256,
            "source_id": chunk.audio_id,
            "parent_chunk_id": parent_id,
            "element_types": ["audio"],
            "asset_refs": [chunk.audio_id],
            "parser_name": "faster-whisper",
            "parser_version": parser_version,
            "model_id": model_id,
            "model_version": model_version,
            "corpus_generation": corpus_generation,
            "modality": "audio",
            "start_ms": chunk.start_ms,
            "end_ms": chunk.end_ms,
        }
        records.append(AudioEvidenceRecord(evidence=evidence, index_document=index_document))
    return records


def write_audio_evidence_manifest(path: Path | str, records: list[AudioEvidenceRecord]) -> Path:
    """Atomically persist evidence and index payloads without transcript-bearing telemetry."""
    output = Path(path)
    if not records:
        raise ValueError("audio evidence manifest requires at least one record")
    source_hashes = {record.evidence.source_sha256 for record in records}
    if len(source_hashes) != 1:
        raise ValueError("audio evidence manifest must contain one source identity")
    start_ms = min(record.evidence.coordinates.start_ms or 0 for record in records)
    end_ms = max(record.evidence.coordinates.end_ms or 0 for record in records)
    output.parent.mkdir(parents=True, exist_ok=True)
    rows = [
        {
            "evidence": record.evidence.model_dump(mode="json"),
            "index_document": record.index_document,
        }
        for record in records
    ]
    with otel_span(
        "ingest.audio",
        {
            "gen_ai.operation.name": "ingest",
            "rag.document_hash": next(iter(source_hashes)),
            "rag.document_length": end_ms - start_ms,
            "rag.top_n": len(records),
        },
    ):
        descriptor, temporary = tempfile.mkstemp(
            prefix=f".{output.name}.", suffix=".tmp", dir=output.parent
        )
        try:
            with os.fdopen(descriptor, "w", encoding="utf-8", newline="\n") as handle:
                for row in rows:
                    handle.write(json.dumps(row, ensure_ascii=False, sort_keys=True))
                    handle.write("\n")
            os.replace(temporary, output)
        except Exception:
            try:
                os.unlink(temporary)
            except FileNotFoundError:
                pass
            raise
    return output
