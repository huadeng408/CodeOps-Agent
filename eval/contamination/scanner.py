"""Benchmark contamination scanner (plan Task 9.1).

Four-layer scan of benchmark queries/answers/tests against production
corpus chunks:

1. exact hash — sha256 of the raw text;
2. normalized n-gram overlap — Jaccard over 8-char n-grams of normalized
   (lowercase, punctuation-stripped, whitespace-collapsed) text;
3. MinHash similarity — 128-hash crc32-based signature with LSH banding as
   a candidate pre-filter, flagged on the signature agreement estimate;
4. embedding nearest-neighbor — cosine of an injectable embedding function;
   ``embedding_fn=None`` skips the layer and records the reason.

The scanner only isolates and reports; it never deletes or mutates data.
Any high-similarity item that has not been reviewed must block a release:
``blocking`` is True whenever ``exact_matches`` or ``high_similarity`` is
non-empty.

Stdlib only (hashlib / re / zlib); deterministic given the same inputs.
"""

from __future__ import annotations

import hashlib
import math
import re
import zlib
from dataclasses import dataclass, field
from typing import Callable, Optional

# 默认签名长度：128 个哈希位，配合 32 个 band、每 band 4 行，
# 在 Jaccard 0.8 处候选召回率趋近 1。
DEFAULT_NUM_HASHES = 128
DEFAULT_NGRAM = 8
DEFAULT_MINHASH_BANDS = 32
DEFAULT_THRESHOLD = 0.8

_WS_RE = re.compile(r"\s+")
_PUNCT_RE = re.compile(r"[^\w\s]+")


def normalize(text: str) -> str:
    """Lowercase, strip punctuation, collapse whitespace (regex-based).

    Chinese characters survive because ``\\w`` in Unicode mode matches
    them; punctuation like ``,.:;!?()[]`` is removed.
    """
    lowered = text.lower()
    stripped = _PUNCT_RE.sub("", lowered)
    return _WS_RE.sub(" ", stripped).strip()


def ngram_set(text: str, n: int) -> set[str]:
    """Character n-grams of the normalized text.

    Texts shorter than ``n`` keep the whole normalized text as a single
    n-gram so identical short queries still match; empty text yields an
    empty set.
    """
    if n <= 0:
        raise ValueError("n must be positive")
    norm = normalize(text)
    if not norm:
        return set()
    if len(norm) < n:
        return {norm}
    return {norm[i : i + n] for i in range(len(norm) - n + 1)}


def jaccard(a: set, b: set) -> float:
    """Jaccard similarity between two sets; empty sets score 0.0."""
    if not a or not b:
        return 0.0
    return len(a & b) / len(a | b)


def _shingles(text: str, size: int) -> list[str]:
    """Normalized character shingles; [] for whitespace-only text."""
    norm = normalize(text)
    if not norm:
        return []
    if len(norm) < size:
        return [norm]
    return [norm[i : i + size] for i in range(len(norm) - size + 1)]


def minhash_signature(text: str, num_hashes: int = DEFAULT_NUM_HASHES, shingle_size: int = DEFAULT_NGRAM) -> list[int]:
    """Deterministic MinHash signature via zlib.crc32 (stdlib only).

    Each of the ``num_hashes`` positions is the minimum of a per-shingle
    crc32 keyed by the hash index, so the same input always yields the
    same signature. Whitespace-only text yields an empty list.
    """
    shingles = _shingles(text, shingle_size)
    if not shingles:
        return []
    sig: list[int] = []
    for i in range(num_hashes):
        best = 0xFFFFFFFF
        for sh in shingles:
            digest = zlib.crc32(f"{i}:{sh}".encode("utf-8")) & 0xFFFFFFFF
            if digest < best:
                best = digest
        sig.append(best)
    return sig


def _signature_agreement(a: list[int], b: list[int]) -> float:
    """MinHash jaccard estimate: fraction of agreeing positions."""
    if not a or not b or len(a) != len(b):
        return 0.0
    return sum(1 for x, y in zip(a, b) if x == y) / len(a)


def _band_keys(sig: list[int], num_bands: int) -> list[tuple[int, ...]]:
    """Split a signature into row-bands (hashable tuple per band)."""
    if not sig:
        return []
    rows = max(1, len(sig) // num_bands)
    return [tuple(sig[b * rows : (b + 1) * rows]) for b in range(len(sig) // rows)]


def _cosine(a: list[float], b: list[float]) -> float:
    """Cosine similarity; zero-norm or dimension-mismatched vectors score 0."""
    if len(a) != len(b) or not a:
        return 0.0
    dot = sum(x * y for x, y in zip(a, b))
    na = math.sqrt(sum(x * x for x in a))
    nb = math.sqrt(sum(y * y for y in b))
    if na == 0.0 or nb == 0.0:
        return 0.0
    return dot / (na * nb)


@dataclass
class ContaminationReport:
    """Isolation-only scan result. Never deletes data; ``blocking`` gates release."""

    exact_matches: list[tuple[int, int]] = field(default_factory=list)  # (bench_idx, chunk_idx)
    high_similarity: list[tuple[int, int, float, str]] = field(default_factory=list)  # (bench, chunk, score, layer)
    blocking: bool = False
    layer_notes: dict[str, str] = field(default_factory=dict)


def scan(
    chunks: list[str],
    benchmark_items: list[str],
    embedding_fn: Optional[Callable[[str], list[float]]] = None,
    ngram: int = DEFAULT_NGRAM,
    minhash_bands: int = DEFAULT_MINHASH_BANDS,
    threshold: float = DEFAULT_THRESHOLD,
) -> ContaminationReport:
    """Run the four-layer contamination scan.

    ``chunks`` are production corpus chunks, ``benchmark_items`` are
    benchmark queries/answers/tests. ``embedding_fn`` takes one text and
    returns a fixed-size vector; when None the embedding layer is skipped
    and recorded in ``layer_notes``. Only reports — nothing is written or
    deleted. Returns a :class:`ContaminationReport` whose ``blocking`` is
    True whenever any unreviewed overlap was found.
    """
    if ngram <= 0:
        raise ValueError("ngram must be positive")
    if minhash_bands <= 0:
        raise ValueError("minhash_bands must be positive")
    if not 0.0 <= threshold <= 1.0:
        raise ValueError("threshold must be in [0, 1]")

    report = ContaminationReport()

    # ---- 预处理 chunks（保留原始下标，空白文本跳过）----
    chunk_ids: list[int] = []
    chunk_hashes: dict[str, int] = {}  # sha256 -> 首个 chunk 原始下标
    chunk_ngrams: list[set[str]] = []
    chunk_sigs: list[list[int]] = []
    chunk_embs: list[list[float]] = []
    chunk_emb_ids: list[int] = []
    for i, chunk in enumerate(chunks):
        if not normalize(chunk):
            continue
        chunk_ids.append(i)
        chunk_hashes.setdefault(hashlib.sha256(chunk.encode("utf-8")).hexdigest(), i)
        chunk_ngrams.append(ngram_set(chunk, ngram))
        chunk_sigs.append(minhash_signature(chunk, shingle_size=ngram))
        if embedding_fn is not None:
            vec = embedding_fn(chunk)
            if vec:
                chunk_embs.append(vec)
                chunk_emb_ids.append(i)

    # MinHash LSH：band -> 候选 chunk（按 chunk 在 chunk_sigs 中的位置）
    buckets: dict[tuple[int, ...], list[int]] = {}
    for ci, sig in enumerate(chunk_sigs):
        for band in _band_keys(sig, minhash_bands):
            buckets.setdefault(band, []).append(ci)

    # ---- 逐 item 四层扫描 ----
    ngram_hits = 0
    minhash_hits = 0
    emb_hits = 0
    for j, item in enumerate(benchmark_items):
        if not normalize(item):
            continue

        # 层 1：exact hash
        digest = hashlib.sha256(item.encode("utf-8")).hexdigest()
        if digest in chunk_hashes:
            report.exact_matches.append((j, chunk_hashes[digest]))

        # 层 2：normalized n-gram overlap
        item_ngrams = ngram_set(item, ngram)
        if item_ngrams:
            for ci, chunk_ng in enumerate(chunk_ngrams):
                score = jaccard(item_ngrams, chunk_ng)
                if score >= threshold:
                    report.high_similarity.append((j, chunk_ids[ci], score, "ngram"))
                    ngram_hits += 1

        # 层 3：MinHash similarity（banding 只做候选过滤，分数用全签名估计）
        item_sig = minhash_signature(item, shingle_size=ngram)
        if item_sig:
            candidates: set[int] = set()
            for band in _band_keys(item_sig, minhash_bands):
                for ci in buckets.get(band, ()):
                    candidates.add(ci)
            for ci in candidates:
                score = _signature_agreement(item_sig, chunk_sigs[ci])
                if score >= threshold:
                    report.high_similarity.append((j, chunk_ids[ci], score, "minhash"))
                    minhash_hits += 1

        # 层 4：embedding nearest-neighbor
        if embedding_fn is not None:
            item_vec = embedding_fn(item)
            if item_vec:
                best_ci, best_score = -1, 0.0
                for ci, chunk_vec in enumerate(chunk_embs):
                    s = _cosine(item_vec, chunk_vec)
                    if s > best_score:
                        best_score, best_ci = s, ci
                if best_ci >= 0 and best_score >= threshold:
                    report.high_similarity.append((j, chunk_emb_ids[best_ci], best_score, "embedding"))
                    emb_hits += 1

    # ---- 层 notes：说明扫描范围或跳过原因 ----
    report.layer_notes["exact"] = f"{len(report.exact_matches)} exact match(es) over {len(chunk_hashes)} unique chunk hashes"
    report.layer_notes["ngram"] = f"{ngram_hits} pair(s) above threshold {threshold} (n={ngram})"
    report.layer_notes["minhash"] = f"{minhash_hits} pair(s) above threshold {threshold} (bands={minhash_bands})"
    if embedding_fn is None:
        report.layer_notes["embedding"] = "skipped: embedding_fn not provided"
    else:
        report.layer_notes["embedding"] = f"{emb_hits} pair(s) above threshold {threshold}"

    # ---- 发布门：任何未审查的高相似 / 精确匹配都阻止发布 ----
    report.blocking = bool(report.exact_matches) or bool(report.high_similarity)
    return report
