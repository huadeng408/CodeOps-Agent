from __future__ import annotations

import json

from orchestrator.rag.audio import (
    TranscriptSegment,
    audio_chunks_to_evidence,
    group_transcript_segments,
    transcribe_audio,
    write_audio_evidence_manifest,
)
from eval.harness.trace_capture import TraceCapture


def test_audio_groups_are_time_bounded_and_emit_playback_citations() -> None:
    segments = [
        TranscriptSegment("intro", 0, 15_000, 0.99),
        TranscriptSegment("details", 16_000, 30_000, 0.98),
        TranscriptSegment("decision", 34_000, 50_000, 0.97),
    ]

    chunks = group_transcript_segments("meeting-1", segments, max_duration_ms=35_000, overlap_ms=2_000)

    assert [(item.start_ms, item.end_ms) for item in chunks] == [(0, 30_000), (28_000, 50_000)]
    assert [item.text for item in chunks] == ["intro details", "decision"]
    assert chunks[1].citation == ("meeting-1", 28_000, 50_000)
    assert all(20_000 <= item.duration_ms <= 60_000 for item in chunks)


def test_audio_chunks_materialize_stable_evidence_and_index_documents() -> None:
    chunks = group_transcript_segments(
        "meeting-1",
        [
            TranscriptSegment("Budget review starts now.", 0, 15_000, 0.99),
            TranscriptSegment("The approved cap is forty thousand.", 16_000, 30_000, 0.98),
            TranscriptSegment("Decision recorded.", 34_000, 50_000, 0.97),
        ],
        max_duration_ms=35_000,
        overlap_ms=2_000,
    )

    records = audio_chunks_to_evidence(
        chunks,
        source_sha256="a" * 64,
        parser_version="1.2.1",
        model_id="Systran/faster-whisper-tiny",
        model_version="d90ca5fe",
    )

    assert [record.evidence.child_id for record in records] == [
        "meeting-1:audio:0-30000",
        "meeting-1:audio:28000-50000",
    ]
    assert records[0].evidence.modality == "audio"
    assert records[0].evidence.coordinates.start_ms == 0
    assert records[0].evidence.coordinates.end_ms == 30_000
    assert records[0].evidence.coordinates.asset_refs == ("meeting-1",)
    assert records[0].index_document["text_content"] == "Budget review starts now. The approved cap is forty thousand."
    assert records[0].index_document["parent_chunk_id"] == "meeting-1:audio:transcript"
    assert records[0].index_document["modality"] == "audio"


def test_audio_evidence_manifest_is_jsonl_and_trace_never_contains_transcript(tmp_path) -> None:
    chunks = [
        group_transcript_segments(
            "meeting-2",
            [
                TranscriptSegment("private transcript phrase", 0, 25_000, 0.91),
            ],
        )[0]
    ]
    records = audio_chunks_to_evidence(
        chunks,
        source_sha256="b" * 64,
        parser_version="1.2.1",
        model_id="Systran/faster-whisper-tiny",
        model_version="d90ca5fe",
    )
    capture = TraceCapture()
    assert capture.install()

    manifest = write_audio_evidence_manifest(tmp_path / "audio-evidence.jsonl", records)

    rows = [json.loads(line) for line in manifest.read_text(encoding="utf-8").splitlines()]
    assert rows[0]["evidence"]["coordinates"] == {
        "page_id": "",
        "page_span": [],
        "element_ids": [],
        "bbox_refs": [],
        "asset_refs": ["meeting-2"],
        "sheet_name": "",
        "cell_range": "",
        "start_ms": 0,
        "end_ms": 25000,
    }
    spans = [span for span in capture.spans() if span.name == "ingest.audio"]
    assert len(spans) == 1
    assert spans[0].attributes["rag.document_hash"] == "b" * 64
    assert spans[0].attributes["rag.document_length"] == 25_000
    assert "private transcript phrase" not in str(spans[0].attributes)


def test_transcribe_audio_converts_faster_whisper_segments_without_recording_text(tmp_path) -> None:
    source = tmp_path / "sample.flac"
    source.write_bytes(b"not-decoded-by-the-injected-model")
    calls = []

    class Segment:
        start = 1.25
        end = 3.5
        text = "  exact text stays in the evidence, never trace attributes  "
        avg_logprob = -0.2

    class Model:
        def transcribe(self, path, **kwargs):
            calls.append((path, kwargs))
            return iter([Segment()]), object()

    segments = transcribe_audio(
        source,
        model_id="Systran/faster-whisper-tiny",
        model_factory=lambda **kwargs: Model(),
        beam_size=1,
    )

    assert segments == [TranscriptSegment("exact text stays in the evidence, never trace attributes", 1250, 3500, 0.8187)]
    assert calls == [(str(source), {"beam_size": 1, "vad_filter": False})]
