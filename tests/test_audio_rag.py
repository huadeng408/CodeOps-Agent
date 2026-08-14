from __future__ import annotations

from orchestrator.rag.audio import TranscriptSegment, group_transcript_segments


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
