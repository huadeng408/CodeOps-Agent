from __future__ import annotations

import hashlib
import io
import json
import urllib.request
import wave
from pathlib import Path

import pytest

import scripts.rag.verify_librispeech_audio_rag as audio_runner
from orchestrator.rag.audio import AudioChunk
from scripts.rag.verify_librispeech_audio_rag import (
    build_audio_qrels,
    concatenate_wavs,
    score_retrieval_rows,
    validate_source_manifest,
    validate_source_readback,
)


def test_committed_manifest_pins_reference_hashes_without_gold_text() -> None:
    manifest_path = (
        Path(__file__).resolve().parents[1]
        / "data"
        / "eval"
        / "multimodal"
        / "audio"
        / "librispeech-test-v1"
        / "manifest.json"
    )
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))

    for row in manifest["rows"]:
        assert "reference_text" not in row
        digest = row["reference_text_sha256"]
        assert len(digest) == 64
        assert digest == digest.lower()
        assert not set(digest) - set("0123456789abcdef")


def test_checksum_writer_pins_nested_source_audio(tmp_path: Path) -> None:
    source = tmp_path / "source"
    source.mkdir()
    first = source / "first.wav"
    combined = source / "combined.wav"
    receipt = tmp_path / "receipt.json"
    first.write_bytes(b"first")
    combined.write_bytes(b"combined")
    receipt.write_text("{}\n", encoding="utf-8")

    checksum_path = audio_runner._write_checksums(
        tmp_path,
        (first, combined, receipt),
    )

    entries = {
        line.split("  ", maxsplit=1)[1]
        for line in checksum_path.read_text(encoding="utf-8").splitlines()
    }
    assert entries == {"source/first.wav", "source/combined.wav", "receipt.json"}


def test_source_manifest_requires_pinned_license_and_independent_reference() -> None:
    manifest = {
        "schema_version": "audio-source-manifest/v1",
        "dataset_id": "openslr/librispeech_asr",
        "dataset_revision": "a" * 40,
        "config": "clean",
        "split": "test",
        "license": "CC-BY-4.0",
        "rows": [
            {
                "row_index": 0,
                "id": "sample-0",
                "reference_text_sha256": audio_runner._text_sha256("independent reference"),
                "query_text": "independent reference",
            }
        ],
    }

    rows, reference_source = validate_source_manifest(manifest)
    assert rows[0]["id"] == "sample-0"
    assert reference_source == "LibriSpeech dataset transcript"

    invalid = {**manifest, "license": ""}
    with pytest.raises(ValueError, match="license"):
        validate_source_manifest(invalid)


def test_source_readback_must_confirm_revision_and_license() -> None:
    source = {
        "sha": "a" * 40,
        "cardData": {"license": ["cc-by-4.0"]},
    }

    validate_source_readback(source, revision="a" * 40, license_name="CC-BY-4.0")

    with pytest.raises(ValueError, match="license"):
        validate_source_readback(
            {**source, "cardData": {"license": ["unknown"]}},
            revision="a" * 40,
            license_name="CC-BY-4.0",
        )


def test_source_manifest_rejects_non_string_text_fields() -> None:
    manifest = {
        "schema_version": "audio-source-manifest/v1",
        "dataset_id": "openslr/librispeech_asr",
        "dataset_revision": "a" * 40,
        "config": "clean",
        "split": "test",
        "license": "CC-BY-4.0",
        "reference_source": "independent transcript",
        "rows": [
            {
                "row_index": 0,
                "id": "sample-0",
                "reference_text_sha256": audio_runner._text_sha256("reference"),
                "query_text": "query",
            }
        ],
    }

    for field in ("license", "reference_source"):
        invalid = {**manifest, field: 123}
        with pytest.raises(ValueError, match=field):
            validate_source_manifest(invalid)

    for field in ("id", "reference_text_sha256", "query_text"):
        invalid_row = {**manifest["rows"][0], field: 123}
        invalid = {**manifest, "rows": [invalid_row]}
        with pytest.raises(ValueError, match=field):
            validate_source_manifest(invalid)


@pytest.mark.parametrize("unsafe_id", ["../escape", "/absolute", r"C:\escape", r"..\escape"])
def test_source_manifest_rejects_ids_that_could_escape_output_directory(unsafe_id: str) -> None:
    manifest = {
        "schema_version": "audio-source-manifest/v1",
        "dataset_id": "openslr/librispeech_asr",
        "dataset_revision": "a" * 40,
        "config": "clean",
        "split": "test",
        "license": "CC-BY-4.0",
        "rows": [
            {
                "row_index": 0,
                "id": unsafe_id,
                "reference_text_sha256": audio_runner._text_sha256("reference"),
                "query_text": "query",
            }
        ],
    }

    with pytest.raises(ValueError, match="row id"):
        validate_source_manifest(manifest)


def test_first_rows_request_is_pinned_to_manifest_revision(monkeypatch) -> None:
    revision = "b" * 40
    manifest = {
        "schema_version": "audio-source-manifest/v1",
        "dataset_id": "openslr/librispeech_asr",
        "dataset_revision": revision,
        "config": "clean",
        "split": "test",
        "license": "CC-BY-4.0",
        "rows": [
            {
                "row_index": 3,
                "id": "sample-3",
                "reference_text_sha256": audio_runner._text_sha256("reference"),
                "query_text": "query",
            }
        ],
    }
    requests: list[str] = []

    def fake_request(url: str, **_kwargs):
        requests.append(url)
        if url.startswith(audio_runner.SOURCE_API):
            return {"sha": revision, "cardData": {"license": ["cc-by-4.0"]}}
        return {
            "rows": [
                {
                    "row": {
                        "id": "sample-3",
                        "text": "reference",
                    }
                }
            ]
        }

    monkeypatch.setattr(audio_runner, "_request_json", fake_request)
    audio_runner._fetch_rows(manifest, manifest["rows"])

    query = urllib.parse.parse_qs(urllib.parse.urlsplit(requests[1]).query)
    assert query["revision"] == [revision]


def test_request_json_records_request_and_response_hashes(monkeypatch) -> None:
    response_bytes = json.dumps({"ok": True}).encode("utf-8")

    class FakeResponse(io.BytesIO):
        status = 200

        def __enter__(self):
            return self

        def __exit__(self, *_args):
            self.close()

    monkeypatch.setattr(
        urllib.request,
        "urlopen",
        lambda request, timeout: FakeResponse(response_bytes),
    )
    records: list[dict[str, object]] = []
    url = "https://example.test/api?revision=" + ("c" * 40)

    payload = audio_runner._request_json(url, request_log=records)

    assert payload == {"ok": True}
    assert records == [
        {
            "method": "GET",
            "url": url,
            "request_sha256": hashlib.sha256(("GET\n" + url + "\n").encode("utf-8")).hexdigest(),
            "response_sha256": hashlib.sha256(response_bytes).hexdigest(),
            "response_bytes": len(response_bytes),
            "status": 200,
        }
    ]


def test_audio_qrels_bind_queries_to_time_containing_chunk() -> None:
    rows = [
        {
            "id": "sample-0",
            "query_text": "first phrase",
            "start_ms": 0,
            "end_ms": 18_000,
        },
        {
            "id": "sample-1",
            "query_text": "second phrase",
            "start_ms": 19_000,
            "end_ms": 35_000,
        },
    ]
    chunks = [AudioChunk("librispeech-test", "first phrase second phrase", 0, 35_000)]

    qrels = build_audio_qrels(rows, chunks)

    assert [item["query_id"] for item in qrels] == ["sample-0", "sample-1"]
    assert all(item["expected_chunk_id"] == "librispeech-test:audio:0-35000" for item in qrels)
    assert all(item["review_status"] == "DESIGNED_PUBLIC_DEV" for item in qrels)
    assert qrels[0]["start_ms"] == 0
    assert qrels[1]["end_ms"] == 35_000


def test_audio_qrels_allow_trailing_source_silence_via_unique_max_overlap() -> None:
    rows = [
        {
            "id": "sample-with-trailing-silence",
            "query_text": "spoken phrase",
            "start_ms": 30_000,
            "end_ms": 42_000,
        }
    ]
    chunks = [
        AudioChunk("librispeech-test", "earlier", 0, 29_000),
        AudioChunk("librispeech-test", "spoken phrase", 28_000, 39_500),
    ]

    qrels = build_audio_qrels(rows, chunks)

    assert qrels[0]["expected_chunk_id"] == "librispeech-test:audio:28000-39500"
    assert qrels[0]["overlap_ms"] == 9_500


def test_concatenate_wavs_returns_stable_source_ranges(tmp_path) -> None:
    paths = []
    for index, frame_count in enumerate((16_000, 8_000)):
        path = tmp_path / f"{index}.wav"
        with wave.open(str(path), "wb") as handle:
            handle.setnchannels(1)
            handle.setsampwidth(2)
            handle.setframerate(16_000)
            handle.writeframes(b"\0\0" * frame_count)
        paths.append(path)

    output = tmp_path / "combined.wav"
    ranges = concatenate_wavs(paths, output)

    assert ranges == [(0, 1_000), (1_000, 1_500)]
    with wave.open(str(output), "rb") as handle:
        assert handle.getnframes() == 24_000


def test_retrieval_scores_report_top_one_and_reciprocal_rank() -> None:
    rows = [
        {"expected_chunk_id": "c1", "returned_chunk_ids": ["c1", "c2"]},
        {"expected_chunk_id": "c2", "returned_chunk_ids": ["c3", "c2"]},
        {"expected_chunk_id": "c4", "returned_chunk_ids": []},
    ]

    metrics = score_retrieval_rows(rows)

    assert metrics == {"recall_at_1": 1 / 3, "recall_at_10": 2 / 3, "mrr_at_10": 0.5}
