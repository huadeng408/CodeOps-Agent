from __future__ import annotations

from dataclasses import dataclass


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
