"""Layer 4 must be able to reuse the corpus vectors the index already stores.

Measured failure this replaces (design map §29): the driver re-embedded all
24,877 chunks through the bge-m3 service. One server-side sub-batch took
183.57s against a 60s client read timeout, so layer 4 degraded to a recorded
skip and the verdict was INCOMPLETE. Wall clock was 107.609s, well inside the
600s SLA -- the SLA was never the problem, the implementation route was.

The route was also never what the frozen policy described:
  layers[3].metric      "cosine similarity of BAAI/bge-m3 vectors"
  layers[3].ann_note    "180 x 24877 x 1024 is a single BLAS matmul at this
                         scale; no ANN index is required and none is introduced."
  sla.peak_rss_rationale "layer 4 holds a 24877 x 1024 float32 matrix (about
                         102 MB)"
24877 x 1024 x 4 bytes is exactly ~102 MB, i.e. the policy assumed the corpus
vectors were already in hand. Reusing the stored vectors follows the policy;
re-encoding the corpus was the deviation.

Measured provenance backing the reuse (whole index, not a sample):
  model_version       one value, BAAI/bge-m3@5617a9f6...aefb181, x 24,877
  exists: vector      24,877 / 24,877
  cos(stored, fresh[embedding_text])  = 1.000000 on 5/5 sampled docs
  control cos(stored doc0, stored doc1) = 0.777860

All tests here are offline: no ES, no embedding service, no production scan.
"""

from __future__ import annotations

import pytest

from eval.contamination.scanner import (
    LAYER_EMBEDDING,
    VERDICT_BLOCKING,
    VERDICT_CLEAN,
    VERDICT_INCOMPLETE,
    _EmbeddingMatcher,
    scan,
)

# Lexically disjoint on purpose: no exact match, no shared 8-grams, no
# near-duplicate. Any layer-4 hit below therefore comes from the vectors
# alone, which is the whole point of the layer.
QUERY = "How do I parse a TOML configuration file in Python?"
CHUNKS = [
    "Goroutines are lightweight concurrent threads of execution in Go.",
    "Elasticsearch stores inverted indices for full text search queries.",
    "The borrow checker rejects aliasing mutable references at compile time.",
    "Kubernetes schedules pods onto nodes according to resource requests.",
]


def basis(index: int, dims: int = 8) -> list[float]:
    """One-hot unit vector: cosine is exactly 1.0 with itself, 0.0 otherwise."""
    vec = [0.0] * dims
    vec[index] = 1.0
    return vec


class RecordingEmbedder:
    """Deterministic embedder that records every text it was asked to embed.

    Recording the texts (not just a call count) is what lets a test prove the
    chunk side never went over the wire: asserting ``chunks_embedded == []`` is
    a positive check, whereas a call count only bounds the total.
    """

    def __init__(self, dims: int = 8) -> None:
        self._seen: dict[str, int] = {}
        self._dims = dims
        self.texts: list[str] = []

    def __call__(self, text: str) -> list[float]:
        self.texts.append(text)
        idx = self._seen.setdefault(text, len(self._seen))
        return basis(idx % self._dims, self._dims)

    def batch(self, texts: list[str]) -> list[list[float]]:
        return [self(text) for text in texts]


@pytest.fixture(autouse=True)
def _forbid_network(monkeypatch: pytest.MonkeyPatch) -> None:
    """Fail loudly rather than silently reaching a live service.

    §29 recorded a case where tests patched one embedding entry point while the
    driver had two, so the suite quietly hit the real bge-m3 server on :8009 and
    a correct assertion looked wrong. Making bare requests raise keeps that
    from recurring here.
    """
    import requests

    def _boom(*args: object, **kwargs: object) -> None:
        raise AssertionError("test attempted a real network call")

    monkeypatch.setattr(requests, "get", _boom)
    monkeypatch.setattr(requests, "post", _boom)


def test_stored_chunk_vectors_complete_layer4_with_no_chunk_side_embedding() -> None:
    """chunk_vectors satisfies layer 4 and the chunk texts never get embedded."""
    embedder = RecordingEmbedder()
    # Query is embedded first, so it takes basis(0). Give every chunk a vector
    # orthogonal to it, so layer 4 runs and legitimately finds nothing.
    chunk_vectors = [basis(1), basis(2), basis(3), basis(4)]

    report = scan(
        CHUNKS,
        [QUERY],
        embedding_batch_fn=embedder.batch,
        chunk_vectors=chunk_vectors,
        min_ngrams=1,
    )

    assert LAYER_EMBEDDING in report.layers_completed
    assert LAYER_EMBEDDING not in report.skipped_layers
    assert report.verdict == VERDICT_CLEAN
    # The decisive assertion: only the query went to the embedder.
    assert report.chunks_scanned == len(CHUNKS)
    assert embedder.texts == [QUERY]
    for chunk in CHUNKS:
        assert chunk not in embedder.texts


def test_chunk_vectors_align_positionally_with_chunks() -> None:
    """A hit must name the chunk whose vector matched, not an arbitrary index.

    Mispairing vectors with chunks yields similarities unrelated to the corpus,
    which is worse than not running the layer: it looks like evidence.
    """
    embedder = RecordingEmbedder()
    # Query takes basis(0); only chunk index 2 carries that same direction.
    chunk_vectors = [basis(1), basis(3), basis(0), basis(4)]

    report = scan(
        CHUNKS,
        [QUERY],
        embedding_batch_fn=embedder.batch,
        chunk_vectors=chunk_vectors,
        min_ngrams=1,
    )

    embedding_hits = [hit for hit in report.high_similarity if hit[3] == LAYER_EMBEDDING]
    assert len(embedding_hits) == 1
    item_index, chunk_index, score, _ = embedding_hits[0]
    assert chunk_index == 2
    assert item_index == 0
    assert score == pytest.approx(1.0)
    # A real hit is contamination: the verdict must block, not stay clean.
    assert report.verdict == VERDICT_BLOCKING


def test_chunk_vector_count_mismatch_raises_instead_of_padding() -> None:
    """Padding or truncating would turn "not checked" into "checked and clean"."""
    embedder = RecordingEmbedder()
    too_few = [basis(1), basis(2)]  # 2 vectors for 4 chunks

    with pytest.raises(ValueError) as excinfo:
        scan(
            CHUNKS,
            [QUERY],
            embedding_batch_fn=embedder.batch,
            chunk_vectors=too_few,
            min_ngrams=1,
        )

    message = str(excinfo.value)
    assert "2" in message and "4" in message


def test_chunk_vectors_without_query_embedder_still_skips_layer4() -> None:
    """Corpus vectors alone cannot run layer 4 -- there is nothing to compare to.

    Without query vectors the layer did not run, so it must be recorded as a
    skip and the verdict must be INCOMPLETE, never CLEAN.
    """
    chunk_vectors = [basis(1), basis(2), basis(3), basis(4)]

    report = scan(CHUNKS, [QUERY], chunk_vectors=chunk_vectors, min_ngrams=1)

    assert LAYER_EMBEDDING not in report.layers_completed
    assert LAYER_EMBEDDING in report.skipped_layers
    assert report.verdict == VERDICT_INCOMPLETE


def test_chunk_vectors_take_precedence_over_the_batch_fn_for_chunks() -> None:
    """When both are supplied, stored vectors win and no chunk is re-embedded."""
    embedder = RecordingEmbedder()
    chunk_vectors = [basis(1), basis(2), basis(3), basis(4)]

    report = scan(
        CHUNKS,
        [QUERY],
        embedding_batch_fn=embedder.batch,
        chunk_vectors=chunk_vectors,
        embedding_batch_size=2,
        min_ngrams=1,
    )

    assert LAYER_EMBEDDING in report.layers_completed
    assert embedder.texts == [QUERY]


def test_numpy_chunk_vectors_do_not_raise_ambiguous_truth() -> None:
    """A float32 matrix is the memory-sane carrier; it must not trip the guard.

    Holding 24,877 x 1024 as Python lists costs roughly 600 MB; as a float32
    numpy array it is the ~102 MB the policy's peak_rss_rationale assumes. That
    makes numpy rows the expected input, and ``if not vec`` raises
    "truth value of an array with more than one element is ambiguous" on them.
    """
    numpy = pytest.importorskip("numpy")
    embedder = RecordingEmbedder()
    chunk_vectors = numpy.asarray(
        [basis(1), basis(2), basis(0), basis(4)], dtype=numpy.float32
    )

    report = scan(
        CHUNKS,
        [QUERY],
        embedding_batch_fn=embedder.batch,
        chunk_vectors=chunk_vectors,
        min_ngrams=1,
    )

    assert LAYER_EMBEDDING in report.layers_completed
    embedding_hits = [hit for hit in report.high_similarity if hit[3] == LAYER_EMBEDDING]
    assert len(embedding_hits) == 1
    assert embedding_hits[0][1] == 2


def test_matcher_observe_accepts_a_numpy_row() -> None:
    """Unit-level lock on the same guard, independent of scan()."""
    numpy = pytest.importorskip("numpy")
    matcher = _EmbeddingMatcher([basis(0), basis(1)], threshold=0.8)

    matcher.observe(7, numpy.asarray(basis(0), dtype=numpy.float32))

    hits = matcher.hits()
    assert [(item, chunk) for item, chunk, _, _ in hits] == [(0, 7)]


def test_matcher_observe_ignores_an_empty_numpy_row() -> None:
    """An empty vector is 'no observation', not an error and not a match."""
    numpy = pytest.importorskip("numpy")
    matcher = _EmbeddingMatcher([basis(0), basis(1)], threshold=0.8)

    matcher.observe(7, numpy.asarray([], dtype=numpy.float32))

    assert matcher.hits() == []
