"""Contamination scanner tests (plan Task 9.1).

TDD: the four-layer scan (exact hash / normalized n-gram / MinHash /
embedding nearest-neighbor) isolates and reports benchmark-corpus overlap
without ever deleting data; high-similarity unreviewed items must block
release.
"""

from __future__ import annotations

from eval.contamination.scanner import (
    ContaminationReport,
    jaccard,
    minhash_signature,
    ngram_set,
    normalize,
    scan,
)


def _agreement(a: list[int], b: list[int]) -> float:
    """Fraction of identical positions between two MinHash signatures."""
    if not a or not b or len(a) != len(b):
        return 0.0
    return sum(1 for x, y in zip(a, b) if x == y) / len(a)


def test_exact_hash_match_detected() -> None:
    chunk = "def foo(): return 42"
    report = scan([chunk], [chunk])
    assert report.exact_matches == [(0, 0)]
    # 精确匹配是最强的高相似信号，同样阻止发布。
    assert report.blocking is True


def test_exact_match_uses_original_indices() -> None:
    chunks = ["unrelated chunk", "def foo(): return 42"]
    items = ["def foo(): return 42", "another query"]
    report = scan(chunks, items)
    assert report.exact_matches == [(0, 1)]


def test_normalized_ngram_overlap_flagged() -> None:
    # 大小写与标点不同，规范化后 n-gram 完全重叠，ngram 层必须标记。
    chunk = "The quick brown fox jumps over the lazy dog."
    item = "the quick, brown fox jumps over the lazy dog!"
    report = scan([chunk], [item])
    layers = {layer for _, _, _, layer in report.high_similarity}
    assert "ngram" in layers
    for _, _, score, layer in report.high_similarity:
        if layer == "ngram":
            assert score >= 0.8


def test_minhash_signature_properties() -> None:
    text = "The quick brown fox jumps over the lazy dog."
    sig = minhash_signature(text)
    assert len(sig) == 128
    assert all(isinstance(v, int) for v in sig)
    # 确定性：同一输入必须产生相同签名。
    assert sig == minhash_signature(text)
    near = minhash_signature("the quick brown fox jumps over the lazy dog x")
    far = minhash_signature("completely unrelated text about quantum physics")
    assert _agreement(sig, near) > _agreement(sig, far)


def test_minhash_similarity_flags_near_duplicate() -> None:
    chunk = "def factorial(n): return 1 if n <= 1 else n * factorial(n - 1)"
    item = chunk + " x"
    report = scan([chunk], [item])
    layers = {layer for _, _, _, layer in report.high_similarity}
    assert "minhash" in layers
    for _, _, score, layer in report.high_similarity:
        if layer == "minhash":
            assert score >= 0.8


def test_embedding_layer_with_injected_fn() -> None:
    def emb(text: str) -> list[float]:
        if "alpha" in text:
            return [1.0, 0.0]
        if "gamma" in text:
            return [0.0, 1.0]
        return [0.0, 0.0]

    chunks = ["alpha document", "gamma document"]
    items = ["alpha query", "beta query"]
    report = scan(chunks, items, embedding_fn=emb)
    assert (0, 0, 1.0, "embedding") in report.high_similarity
    assert "skip" not in report.layer_notes.get("embedding", "").lower()


def test_embedding_layer_skipped_without_fn() -> None:
    report = scan(["alpha document"], ["alpha query"])
    note = report.layer_notes.get("embedding", "")
    assert note and "skip" in note.lower()
    assert all(layer != "embedding" for _, _, _, layer in report.high_similarity)


def test_embedding_layer_index_alignment_with_empty_vectors() -> None:
    """Regression: embedding_fn returning empty for one chunk must not shift
    the chunk index reported in high_similarity (was chunk_embs vs
    chunk_ids divergence)."""

    def emb(text: str) -> list[float]:
        if "empty" in text:
            return []  # empty vector: skipped from embedding layer
        if "alpha" in text:
            return [1.0, 0.0]
        if "gamma" in text:
            return [0.0, 1.0]
        return [0.0, 0.0]

    chunks = ["empty chunk", "alpha document", "gamma document"]
    items = ["gamma query"]
    report = scan(chunks, items, embedding_fn=emb)
    # The reported chunk index must be 2 (gamma), not 1 (alpha) — an index
    # shift from the skipped empty-vector chunk would misreport alpha.
    embedding_hits = [(j, ci) for j, ci, s, layer in report.high_similarity if layer == "embedding"]
    assert embedding_hits == [(0, 2)]


def test_blocking_verdict_true_when_high_similarity() -> None:
    chunk = "The quick brown fox jumps over the lazy dog."
    item = "the quick brown fox jumps over the lazy dog and"
    report = scan([chunk], [item])
    assert report.high_similarity
    assert report.blocking is True


def test_no_false_positive_on_distinct_text() -> None:
    chunks = [
        "Golang http server with graceful shutdown and health endpoints.",
        "Postgres connection pooling tuned for 10k concurrent queries.",
    ]
    items = [
        "Python data loader that streams parquet files from S3.",
        "MinIO bucket lifecycle policy for expired object cleanup.",
    ]
    report = scan(chunks, items)
    assert report.exact_matches == []
    assert report.high_similarity == []
    assert report.blocking is False


def test_report_fields_shape() -> None:
    report = scan([], [])
    assert report.exact_matches == []
    assert report.high_similarity == []
    assert report.blocking is False
    assert isinstance(report.layer_notes, dict)
    # 四个层都需要在 notes 里说明扫描范围或跳过原因。
    for layer in ("exact", "ngram", "minhash", "embedding"):
        assert layer in report.layer_notes

    report = scan(["chunk one"], ["item one", "chunk one"])
    for pair in report.exact_matches:
        assert len(pair) == 2 and all(isinstance(v, int) for v in pair)
    for entry in report.high_similarity:
        assert len(entry) == 4
        assert isinstance(entry[0], int) and isinstance(entry[1], int)
        assert isinstance(entry[2], float) and isinstance(entry[3], str)
    assert isinstance(report, ContaminationReport)


def test_whitespace_only_inputs_are_ignored() -> None:
    report = scan(["   \n\t ", "real chunk"], ["", "real chunk"])
    assert report.exact_matches == [(1, 1)]
    assert report.blocking is True


def test_normalize_helper() -> None:
    assert normalize("  The QUICK, Brown fox!!  \n\t jumps  ") == "the quick brown fox jumps"
    assert normalize("") == ""


def test_ngram_set_helper() -> None:
    assert ngram_set("abcabc", 3) == {"abc", "bca", "cab"}
    # 短于 n 时整体保留为单个 n-gram。
    assert ngram_set("ab", 3) == {"ab"}
    assert ngram_set("", 3) == set()


def test_jaccard_helper() -> None:
    assert jaccard({1, 2}, {2, 3}) == 1 / 3
    assert jaccard({1, 2}, {3, 4}) == 0.0
    assert jaccard({"a"}, {"a"}) == 1.0
    assert jaccard(set(), set()) == 0.0
    assert jaccard({1}, set()) == 0.0
