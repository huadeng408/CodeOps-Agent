"""Layer-2 directional containment tests for the E4 contamination contract.

Regression for D1, the defect this layer exists to fix: the pre-v1 scanner
could not detect a benchmark query copied verbatim into a longer corpus
document. Layers 2 and 3 both scored Jaccard, and when ``q`` is fully inside
``c`` Jaccard is bounded by ``|q| / |c|`` -- reaching 0.8 would require the
chunk to be at most 1.25x the query. On the live corpus (median chunk 360
n-grams against median query 45) the ceiling was 0.138, so the most important
contamination shape was mathematically undetectable.

Directional containment (``|q AND c| / |q|``) is the metric that detects it.

These tests also pin the shape of the *rejected* metric -- overlap coefficient
with a ``min(|q|, |c|)`` denominator -- which produced 43 false positives on
the live corpus, all of them degenerate fragments of 1 to 14 n-grams rather
than contamination. Re-introducing that denominator must not silently pass.

Offline: no ES, no embedding service, no production scan.
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from eval.contamination.scanner import containment, jaccard, ngram_set, scan

REPO_ROOT = Path(__file__).resolve().parents[2]
QUERIES_PATH = REPO_ROOT / "data" / "eval" / "techdocs" / "queries.text.jsonl"

NGRAM = 8
THRESHOLD = 0.8

# The blindness ceiling recorded in the frozen policy: q fully inside c caps
# Jaccard at |q|/|c|, so 0.8 needs c <= 1.25 * q.
MAX_CHUNK_RATIO_FOR_JACCARD = 1.25


def _first_real_query() -> str:
    """First query from the hash-pinned benchmark queries file."""
    for line in QUERIES_PATH.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line:
            continue
        record = json.loads(line)
        query = record.get("query")
        if isinstance(query, str) and query:
            return query
    raise AssertionError(f"no usable query found in {QUERIES_PATH}")


def _host_chunk(query: str, factor: int = 10) -> str:
    """A realistic corpus chunk roughly ``factor`` times the query, holding it verbatim.

    The surrounding prose is unrelated deployment documentation. Containment is
    1.0 regardless of what the filler says, because every query n-gram is
    present; the filler only drives |c| up so Jaccard cannot fire.
    """
    filler = (
        "Deployment topology notes cover replica placement, rolling restart "
        "windows, storage class selection, and backup retention schedules. "
    )
    half = max(1, (len(query) * factor) // 2)
    pad = filler * (half // len(filler) + 1)
    return f"{pad[:half]} {query} {pad[:half]}"


# --------------------------------------------------------------------------- #
# containment helper
# --------------------------------------------------------------------------- #


def test_containment_is_full_when_query_is_a_subset_of_the_chunk() -> None:
    q = {"a", "b", "c"}
    c = {"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
    assert containment(q, c) == 1.0


def test_containment_is_directional_not_symmetric() -> None:
    """The whole point of D1's fix: swapping the arguments must change the score.

    A symmetric metric (Jaccard) or a min() denominator cannot express
    "the query is inside the document".
    """
    q = {"a", "b", "c"}
    c = {"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
    assert containment(q, c) == 1.0
    assert containment(c, q) == pytest.approx(0.3)
    assert containment(q, c) != containment(c, q)


def test_containment_partial_overlap_uses_query_side_denominator() -> None:
    q = {"a", "b", "c", "d"}
    c = {"a", "b", "zz"}
    # 2 of the query's 4 n-grams are present -> 0.5, regardless of |c|.
    assert containment(q, c) == 0.5


def test_containment_empty_sets_score_zero() -> None:
    assert containment(set(), {"a"}) == 0.0
    assert containment({"a"}, set()) == 0.0
    assert containment(set(), set()) == 0.0


def test_containment_ignores_chunk_size_entirely() -> None:
    """Same query inside chunks of wildly different sizes scores the same 1.0."""
    q = {"a", "b"}
    small = {"a", "b", "c"}
    huge = {"a", "b"} | {f"pad{i}" for i in range(5000)}
    assert containment(q, small) == 1.0
    assert containment(q, huge) == 1.0


# --------------------------------------------------------------------------- #
# D1 regression: a query copied into a longer document must block
# --------------------------------------------------------------------------- #


def test_d1_regression_verbatim_query_in_longer_chunk_blocks() -> None:
    query = "How do I configure a rolling update strategy for a StatefulSet?"
    chunk = _host_chunk(query, factor=10)
    assert query in chunk

    report = scan([chunk], [query], ngram=NGRAM, threshold=THRESHOLD)

    layers = {layer for _, _, _, layer in report.high_similarity}
    assert "containment" in layers, (
        "a benchmark query copied verbatim into a longer corpus chunk must be "
        "flagged by the containment layer"
    )
    assert report.blocking is True


def test_d1_regression_uses_a_real_pinned_benchmark_query() -> None:
    """Same regression, but with a real query from the hash-pinned queries file."""
    query = _first_real_query()
    chunk = _host_chunk(query, factor=10)

    report = scan([chunk], [query], ngram=NGRAM, threshold=THRESHOLD)

    containment_hits = [
        (j, ci, score) for j, ci, score, layer in report.high_similarity if layer == "containment"
    ]
    assert containment_hits == [(0, 0, 1.0)]
    assert report.blocking is True


def test_d1_the_old_jaccard_metric_provably_misses_the_same_pair() -> None:
    """Proves the fix is load-bearing, not cosmetic.

    The exact pair the containment layer catches must be one Jaccard scores
    far below threshold -- otherwise the old implementation would have caught
    it and D1 would not be a real defect.
    """
    query = _first_real_query()
    chunk = _host_chunk(query, factor=10)

    q_ngrams = ngram_set(query, NGRAM)
    c_ngrams = ngram_set(chunk, NGRAM)

    assert containment(q_ngrams, c_ngrams) == 1.0
    assert jaccard(q_ngrams, c_ngrams) < THRESHOLD
    # And the ceiling is structural, not incidental to this one pair.
    assert jaccard(q_ngrams, c_ngrams) <= len(q_ngrams) / len(c_ngrams) + 1e-9
    assert len(c_ngrams) > MAX_CHUNK_RATIO_FOR_JACCARD * len(q_ngrams)


@pytest.mark.parametrize("factor", [2, 5, 10, 25])
def test_d1_holds_across_chunk_sizes_where_jaccard_degrades(factor: int) -> None:
    """Containment stays 1.0 as the host document grows; Jaccard decays toward 0."""
    query = "What does the reconcile loop do when a finalizer is still present?"
    chunk = _host_chunk(query, factor=factor)

    q_ngrams = ngram_set(query, NGRAM)
    c_ngrams = ngram_set(chunk, NGRAM)

    assert containment(q_ngrams, c_ngrams) == 1.0
    assert jaccard(q_ngrams, c_ngrams) < THRESHOLD

    report = scan([chunk], [query], ngram=NGRAM, threshold=THRESHOLD)
    assert report.blocking is True


def test_layer_label_is_containment_not_ngram() -> None:
    """The frozen policy names layer 2 'containment' and counts containment_hits.

    Keeping the old 'ngram' label would keep the report misleading about which
    metric produced the hit.
    """
    query = "How is leader election implemented in the controller manager?"
    report = scan([_host_chunk(query, factor=8)], [query], ngram=NGRAM, threshold=THRESHOLD)
    layers = {layer for _, _, _, layer in report.high_similarity}
    assert "containment" in layers
    assert "ngram" not in layers


# --------------------------------------------------------------------------- #
# D6: the rejected min() denominator and its 43 false positives
# --------------------------------------------------------------------------- #


def test_degenerate_fragment_inside_a_query_is_not_flagged() -> None:
    """The exact shape of the 43 rejected-metric false positives.

    A 1-to-14 n-gram fragment whose n-grams are a subset of the query scored
    1.0 under the min() denominator. Under directional containment it scores
    |c|/|q|, which is far below threshold, so it must not be flagged.
    """
    query = _first_real_query()
    q_ngrams = ngram_set(query, NGRAM)
    # A short substring of the query: its n-gram set is a strict subset.
    fragment = query[:15]
    c_ngrams = ngram_set(fragment, NGRAM)

    assert 0 < len(c_ngrams) <= 14
    assert c_ngrams <= q_ngrams
    # This is what the rejected metric computed, and why it fired.
    rejected_overlap = len(q_ngrams & c_ngrams) / min(len(q_ngrams), len(c_ngrams))
    assert rejected_overlap == 1.0
    # The frozen metric does not fire.
    assert containment(q_ngrams, c_ngrams) < THRESHOLD

    report = scan([fragment], [query], ngram=NGRAM, threshold=THRESHOLD)
    containment_hits = [e for e in report.high_similarity if e[3] == "containment"]
    assert containment_hits == []


@pytest.mark.parametrize("prefix_len", [9, 12, 15, 20, 21])
def test_no_containment_hit_across_the_rejected_metrics_size_range(prefix_len: int) -> None:
    """Sweeps the measured |c| range of the 43 false positives (1..14 n-grams)."""
    query = _first_real_query()
    fragment = query[:prefix_len]
    c_ngrams = ngram_set(fragment, NGRAM)
    assert len(c_ngrams) <= 14

    report = scan([fragment], [query], ngram=NGRAM, threshold=THRESHOLD)
    assert [e for e in report.high_similarity if e[3] == "containment"] == []


def test_degenerate_fragments_are_counted_not_silently_dropped() -> None:
    """Policy: report_only, never_silently_dropped.

    A fragment excluded from scoring must still appear in the count, so the
    exclusion is visible in the report rather than invisible.
    """
    query = _first_real_query()
    chunks = [query[:12], query[:15], "a" * 400]
    report = scan(chunks, [query], ngram=NGRAM, threshold=THRESHOLD)
    assert report.degenerate_fragments >= 2


def test_empty_normalizing_chunks_are_counted_as_degenerate() -> None:
    """605 live chunks are under 8 n-grams and some normalize to ''.

    The scanner already skips empty-normalizing chunks from scoring; the policy
    additionally requires them to be counted so the skip is visible.
    """
    report = scan(["   \n\t  ", "", "real chunk text here"], ["some query"], ngram=NGRAM)
    assert report.degenerate_fragments >= 2
