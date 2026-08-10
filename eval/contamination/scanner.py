"""Benchmark contamination scanner (plan Task 9.1; E4 policy v1).

Four-layer scan of benchmark queries against production corpus chunks. The
layer set, metrics, thresholds, SLA and honesty rules are frozen in
``data/eval/contamination/contamination-policy.v1.json`` (byte-pinned by its
``.sha256`` sidecar); this module implements that policy.

1. ``exact``       -- sha256 of the raw UTF-8 text.
2. ``containment`` -- directional ``|q_ngrams & c_ngrams| / |q_ngrams|`` over
   n-char n-grams of normalized text. Directional on purpose: a benchmark
   query copied verbatim into a *longer* corpus document scores 1.0. That is
   exactly the case symmetric Jaccard is mathematically blind to, because
   ``Jaccard <= |q|/|c|`` when q is contained in c, so reaching 0.8 would
   require ``|c| <= 1.25 * |q|``. MinHash estimates Jaccard and therefore
   inherits the same blindness.
3. ``minhash``     -- Jaccard estimated from a 128-position MinHash signature,
   LSH-banded for candidate generation. Kept as a cheap near-duplicate net;
   it does NOT detect containment.
4. ``embedding``   -- cosine of an injectable embedding function.
   ``embedding_fn=None`` skips the layer, which makes the report INCOMPLETE.

Honesty rule (policy ``skipped_layer_rule``): if any layer did not run, the
verdict is ``INCOMPLETE`` and the scan must never be reported as clean.
Verdict precedence is ``BLOCKING`` > ``INCOMPLETE`` > ``CLEAN``: a real hit is
more actionable than missing coverage.

The scanner only isolates and reports; it never deletes or mutates data.
Degenerate fragments (fewer than ``min_ngrams`` n-grams, including chunks that
normalize to the empty string) are counted and reported, never dropped
silently and never removed from the index.

Memory: the pass over ``chunks`` is streaming. Per-chunk n-gram sets and
signatures are used and discarded, so only query-side state is retained
(measured Python-level peak 21 MB over the 24,877-chunk corpus). Retaining
per-chunk n-gram sets, as the pre-v1 implementation did, costs gigabytes.

Speed: numpy vectorises the MinHash permutations (measured 11.79 -> 0.41
ms/chunk, the difference between 291 s and 83 s over the same corpus). A
stdlib fallback computes bit-identical signatures when numpy is absent.
Deterministic given the same inputs.
"""

from __future__ import annotations

import hashlib
import math
import re
import zlib
from dataclasses import dataclass, field
from functools import lru_cache
from typing import Callable, Optional, Sequence

try:  # numpy is already a first-party dependency of the eval tree.
    import numpy as _np
except ImportError:  # pragma: no cover - only on a numpy-less install
    _np = None

# 默认签名长度：128 个哈希位，配合 32 个 band、每 band 4 行，
# 在 Jaccard 0.8 处候选召回率趋近 1。
DEFAULT_NUM_HASHES = 128
DEFAULT_NGRAM = 8
DEFAULT_MINHASH_BANDS = 32
DEFAULT_THRESHOLD = 0.8
# policy degenerate_fragment.min_ngrams / sla.progress_report_interval_chunks
DEFAULT_MIN_NGRAMS = 16
DEFAULT_EMBEDDING_BATCH = 64
DEFAULT_PROGRESS_INTERVAL = 2000

LAYER_EXACT = "exact"
LAYER_CONTAINMENT = "containment"
LAYER_MINHASH = "minhash"
LAYER_EMBEDDING = "embedding"
ALL_LAYERS: tuple[str, ...] = (
    LAYER_EXACT,
    LAYER_CONTAINMENT,
    LAYER_MINHASH,
    LAYER_EMBEDDING,
)

VERDICT_CLEAN = "CLEAN"
VERDICT_BLOCKING = "BLOCKING"
VERDICT_INCOMPLETE = "INCOMPLETE"

# Mersenne prime. Shingle hashes are reduced mod _PRIME (< 2^31) so that
# a * h + b stays below 2^63 and never overflows uint64/int64 in numpy.
_PRIME = (1 << 31) - 1

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
    """Jaccard similarity between two sets; empty sets score 0.0.

    Symmetric, and therefore blind to containment: when ``a`` is a subset of
    ``b`` this is capped at ``|a| / |b|``. Use :func:`containment` to ask
    whether a benchmark query sits inside a corpus document.
    """
    if not a or not b:
        return 0.0
    return len(a & b) / len(a | b)


def containment(query_ngrams: set, chunk_ngrams: set) -> float:
    """Directional containment: share of *query* n-grams present in the chunk.

    The denominator is the query side only (policy layer 2), so the score is
    independent of how large the hosting document is. Deliberately asymmetric:
    ``containment(q, c) != containment(c, q)``.

    A ``min(|q|, |c|)`` denominator was measured and rejected -- it scored 1.0
    for any tiny corpus fragment whose n-grams happen to be a subset of the
    query, producing 43 false positives over the real corpus and zero true
    positives (policy ``rejected_metric``).
    """
    if not query_ngrams or not chunk_ngrams:
        return 0.0
    return len(query_ngrams & chunk_ngrams) / len(query_ngrams)


def _shingles(text: str, size: int) -> list[str]:
    """Normalized character shingles; [] for whitespace-only text."""
    norm = normalize(text)
    if not norm:
        return []
    if len(norm) < size:
        return [norm]
    return [norm[i : i + size] for i in range(len(norm) - size + 1)]


@lru_cache(maxsize=8)
def _permutation_params(num_hashes: int) -> tuple[tuple[int, ...], tuple[int, ...]]:
    """Deterministic ``(a, b)`` coefficients for ``h -> (a*h + b) % _PRIME``.

    Derived from sha256 of a fixed label, so the hash family is stable across
    Python versions, platforms and numpy releases -- a signature computed today
    is comparable with one computed a year from now.
    """
    a_vals: list[int] = []
    b_vals: list[int] = []
    for i in range(num_hashes):
        da = hashlib.sha256(f"minhash-a:{i}".encode("utf-8")).digest()
        db = hashlib.sha256(f"minhash-b:{i}".encode("utf-8")).digest()
        a_vals.append(int.from_bytes(da[:8], "big") % (_PRIME - 1) + 1)  # a != 0
        b_vals.append(int.from_bytes(db[:8], "big") % _PRIME)
    return tuple(a_vals), tuple(b_vals)


@lru_cache(maxsize=8)
def _permutation_arrays(num_hashes: int):
    """Column vectors of the permutation coefficients for the numpy path."""
    a_vals, b_vals = _permutation_params(num_hashes)
    a = _np.asarray(a_vals, dtype=_np.uint64).reshape(-1, 1)
    b = _np.asarray(b_vals, dtype=_np.uint64).reshape(-1, 1)
    return a, b


def _base_hashes(shingles: list[str]) -> list[int]:
    """One crc32 per distinct shingle, reduced into ``[0, _PRIME)``.

    Hashing each shingle once and permuting is what makes the signature cheap:
    the pre-v1 implementation paid ``num_hashes * len(shingles)`` crc32 calls
    plus an f-string per call. MinHash is defined over the shingle *set*, so
    de-duplicating here changes no result.
    """
    return [zlib.crc32(sh.encode("utf-8")) % _PRIME for sh in set(shingles)]


def _permute_min(base: list[int], num_hashes: int) -> list[int]:
    """Minimum of each permutation over the base hashes.

    The numpy and stdlib branches implement the same arithmetic and return
    bit-identical signatures; numpy only removes the Python-level loop.
    """
    if _np is not None:
        a, b = _permutation_arrays(num_hashes)
        h = _np.asarray(base, dtype=_np.uint64).reshape(1, -1)
        vals = (a * h + b) % _np.uint64(_PRIME)
        return vals.min(axis=1).tolist()  # .tolist() -> Python ints, not uint64
    a_vals, b_vals = _permutation_params(num_hashes)
    return [min((av * h + bv) % _PRIME for h in base) for av, bv in zip(a_vals, b_vals)]


def minhash_signature(
    text: str,
    num_hashes: int = DEFAULT_NUM_HASHES,
    shingle_size: int = DEFAULT_NGRAM,
) -> list[int]:
    """Deterministic MinHash signature over normalized character shingles.

    Each of the ``num_hashes`` positions is the minimum of one permutation of
    the shingle hashes, so the fraction of agreeing positions estimates the
    Jaccard similarity of the shingle sets. Whitespace-only text yields an
    empty list. Values are Python ints.
    """
    shingles = _shingles(text, shingle_size)
    if not shingles:
        return []
    return _permute_min(_base_hashes(shingles), num_hashes)


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
    """Isolation-only scan result. Never deletes data.

    ``blocking`` gates release on findings; ``verdict`` additionally gates it
    on *coverage*, so a run that skipped a layer can never present itself as
    clean (policy ``skipped_layer_rule``).
    """

    exact_matches: list[tuple[int, int]] = field(default_factory=list)  # (bench_idx, chunk_idx)
    high_similarity: list[tuple[int, int, float, str]] = field(default_factory=list)  # (bench, chunk, score, layer)
    blocking: bool = False
    layer_notes: dict[str, str] = field(default_factory=dict)
    # 层 id -> 跳过原因；非空即代表本次扫描覆盖不全。
    skipped_layers: dict[str, str] = field(default_factory=dict)
    layers_completed: list[str] = field(default_factory=list)
    # 退化碎片：只计数上报，绝不删除、绝不静默丢弃。
    degenerate_fragments: int = 0
    chunks_scanned: int = 0
    queries_scanned: int = 0

    @property
    def incomplete(self) -> bool:
        """True when at least one layer did not run."""
        return bool(self.skipped_layers)

    @property
    def verdict(self) -> str:
        """``BLOCKING`` > ``INCOMPLETE`` > ``CLEAN``.

        ``CLEAN`` requires all four layers completed AND zero exact matches AND
        zero threshold hits. Zero hits from three layers is not clean when the
        fourth never ran.
        """
        if self.exact_matches or self.high_similarity:
            return VERDICT_BLOCKING
        if self.skipped_layers:
            return VERDICT_INCOMPLETE
        # 正向要求四层齐全，而不是「没记录跳过就算干净」：
        # 后者是 fail-open——任何忘记登记 skip 的路径都会静默升级成 CLEAN。
        if not all(layer in self.layers_completed for layer in ALL_LAYERS):
            return VERDICT_INCOMPLETE
        return VERDICT_CLEAN

    def layer_hits(self, layer: str) -> int:
        """Number of above-threshold pairs attributed to ``layer``."""
        if layer == LAYER_EXACT:
            return len(self.exact_matches)
        return sum(1 for entry in self.high_similarity if entry[3] == layer)


def _embed_all(
    texts: list[str],
    embedding_fn: Optional[Callable[[str], list[float]]],
    embedding_batch_fn: Optional[Callable[[list[str]], list[list[float]]]],
    batch_size: int,
) -> list[list[float]]:
    """Embed ``texts`` in order, batching when a batch function is available.

    Batching is load-bearing for the SLA on a real run: one HTTP round-trip per
    chunk over 24,877 chunks cannot fit the 600 s budget.
    """
    if embedding_batch_fn is not None:
        out: list[list[float]] = []
        for start in range(0, len(texts), max(1, batch_size)):
            batch = texts[start : start + max(1, batch_size)]
            vecs = embedding_batch_fn(batch)
            if len(vecs) != len(batch):
                raise ValueError(
                    f"embedding_batch_fn returned {len(vecs)} vectors for {len(batch)} texts"
                )
            out.extend(vecs)
        return out
    if embedding_fn is None:
        return [[] for _ in texts]
    return [embedding_fn(text) for text in texts]


class _EmbeddingMatcher:
    """Tracks, per benchmark item, the single best-scoring chunk seen so far.

    Mirrors the pre-v1 semantics (one hit per item, the first maximum wins)
    while streaming: chunk vectors are scored and discarded, never accumulated
    into a corpus-sized matrix.
    """

    def __init__(self, item_vecs: list[list[float]], threshold: float) -> None:
        self.threshold = threshold
        self.item_vecs = item_vecs
        self.best_score = [0.0] * len(item_vecs)
        self.best_chunk = [-1] * len(item_vecs)
        self._matrix = None
        self._dim = 0
        if _np is not None:
            dims = {len(v) for v in item_vecs if v}
            if len(dims) == 1:
                self._dim = dims.pop()
                rows = _np.zeros((len(item_vecs), self._dim), dtype=_np.float64)
                for i, vec in enumerate(item_vecs):
                    if len(vec) != self._dim:
                        continue
                    arr = _np.asarray(vec, dtype=_np.float64)
                    norm = _np.linalg.norm(arr)
                    if norm > 0.0:
                        rows[i] = arr / norm
                self._matrix = rows

    def observe(self, chunk_idx: int, vec: Sequence[float]) -> None:
        # 用 len() 而不是 `not vec` 判空：numpy 行的真值是歧义的，`not vec` 会抛
        # ValueError（多元素）或触发 DeprecationWarning（空数组）。而 float32 矩阵
        # 正是层 4 的预期载体——24877 x 1024 用 Python list 约 600 MB，用 float32
        # 才是 policy peak_rss_rationale 假定的约 102 MB。
        if vec is None or len(vec) == 0:
            return
        if self._matrix is not None and len(vec) == self._dim:
            arr = _np.asarray(vec, dtype=_np.float64)
            norm = _np.linalg.norm(arr)
            if norm == 0.0:
                return
            scores = self._matrix @ (arr / norm)
            for i in range(len(self.item_vecs)):
                score = float(scores[i])
                if score > self.best_score[i]:
                    self.best_score[i] = score
                    self.best_chunk[i] = chunk_idx
            return
        for i, item_vec in enumerate(self.item_vecs):
            if not item_vec:
                continue
            score = _cosine(item_vec, vec)
            if score > self.best_score[i]:
                self.best_score[i] = score
                self.best_chunk[i] = chunk_idx

    def hits(self) -> list[tuple[int, int, float, str]]:
        out: list[tuple[int, int, float, str]] = []
        for i, (score, chunk_idx) in enumerate(zip(self.best_score, self.best_chunk)):
            if chunk_idx >= 0 and score >= self.threshold:
                out.append((i, chunk_idx, float(score), LAYER_EMBEDDING))
        return out


def scan(
    chunks: Sequence[str],
    benchmark_items: Sequence[str],
    embedding_fn: Optional[Callable[[str], list[float]]] = None,
    ngram: int = DEFAULT_NGRAM,
    minhash_bands: int = DEFAULT_MINHASH_BANDS,
    threshold: float = DEFAULT_THRESHOLD,
    *,
    min_ngrams: int = DEFAULT_MIN_NGRAMS,
    embedding_batch_fn: Optional[Callable[[list[str]], list[list[float]]]] = None,
    embedding_batch_size: int = DEFAULT_EMBEDDING_BATCH,
    chunk_vectors: Optional[Sequence[Sequence[float]]] = None,
    progress_fn: Optional[Callable[[int, int], None]] = None,
    progress_interval: int = DEFAULT_PROGRESS_INTERVAL,
) -> ContaminationReport:
    """Run the four-layer contamination scan.

    ``chunks`` are production corpus chunks, ``benchmark_items`` are benchmark
    queries/answers/tests. ``embedding_fn`` takes one text and returns a
    fixed-size vector; ``embedding_batch_fn`` takes a list and returns one
    vector per text, and takes precedence when given. When neither is
    provided the embedding layer is skipped, recorded in ``skipped_layers``,
    and the verdict becomes ``INCOMPLETE`` -- never ``CLEAN``.

    ``chunk_vectors`` supplies precomputed corpus vectors, positionally aligned
    with ``chunks`` (index i is the vector for ``chunks[i]``). When given, the
    corpus side of layer 4 does no embedding work at all and these vectors are
    used directly; the *query* side still needs ``embedding_fn`` or
    ``embedding_batch_fn``, because corpus vectors alone have nothing to be
    compared against. A length mismatch raises ``ValueError`` rather than
    padding or truncating: padding would turn "not checked" into "checked and
    clean", and truncating would silently drop coverage. Accepts a numpy
    float32 matrix, which is the memory-sane carrier at corpus scale.

    ``progress_fn(chunks_done, chunks_total)`` is called every
    ``progress_interval`` chunks so a long run is observable and a killed run
    can still be reported. Only reports -- nothing is written or deleted.
    """
    if ngram <= 0:
        raise ValueError("ngram must be positive")
    if minhash_bands <= 0:
        raise ValueError("minhash_bands must be positive")
    if not 0.0 <= threshold <= 1.0:
        raise ValueError("threshold must be in [0, 1]")
    # 数量不符必须抛错，绝不补齐或截断：补齐会把「没检查」伪装成「检查过且干净」，
    # 错位配对则会给出与语料无关的相似度——那比不跑这一层更坏，因为它看起来像证据。
    if chunk_vectors is not None and len(chunk_vectors) != len(chunks):
        raise ValueError(
            f"chunk_vectors has {len(chunk_vectors)} vector(s) for {len(chunks)} chunk(s); "
            "they must align positionally"
        )

    report = ContaminationReport()
    # chunk 侧向量已给定时，语料侧不需要任何 embedding 后端；但查询侧仍然需要，
    # 否则没有可比对的向量，这一层等于没跑。
    has_embedding = (
        embedding_fn is not None or embedding_batch_fn is not None
    )

    # ---- 查询侧预计算（只保留查询侧状态，chunk 侧流式处理后即丢弃）----
    item_indices: list[int] = []
    item_ngrams: list[set[str]] = []
    item_sigs: list[list[int]] = []
    item_texts: list[str] = []
    hash_to_items: dict[str, list[int]] = {}
    for j, item in enumerate(benchmark_items):
        if not normalize(item):
            continue
        item_indices.append(j)
        item_ngrams.append(ngram_set(item, ngram))
        item_sigs.append(minhash_signature(item, shingle_size=ngram))
        item_texts.append(item)
        digest = hashlib.sha256(item.encode("utf-8")).hexdigest()
        hash_to_items.setdefault(digest, []).append(len(item_indices) - 1)
    report.queries_scanned = len(item_indices)

    # 层 2 倒排：n-gram -> 含该 n-gram 的查询位置。查询侧唯一 n-gram 数量远小于
    # 语料侧，因此一次流式遍历即可算出全部 containment，无需 |q| x |c| 笛卡尔积。
    inverted: dict[str, list[int]] = {}
    for qi, ngrams in enumerate(item_ngrams):
        for gram in ngrams:
            inverted.setdefault(gram, []).append(qi)

    # 层 3 LSH：band -> 候选查询位置（候选生成放在查询侧，语料签名用完即弃）。
    buckets: dict[tuple[int, ...], list[int]] = {}
    for qi, sig in enumerate(item_sigs):
        for band in _band_keys(sig, minhash_bands):
            buckets.setdefault(band, []).append(qi)

    # 第 4 层的失败原因。非 None 即代表该层没有跑完，必须记为跳过。
    # 按 policy failure_semantics 的 EMBEDDING_SERVICE_UNREACHABLE(non_clean)：
    # 服务出问题时扫描继续、层 1-3 结果照常上报，判决降级为 INCOMPLETE，
    # 而不是让异常逃出去——异常会让进程退 1，而 1 在 exit_codes 里是 BLOCKING，
    # 等于把「没查完」误报成「查到污染」。
    embedding_failure: Optional[str] = None

    item_vecs: list[list[float]] = []
    if has_embedding:
        try:
            item_vecs = _embed_all(
                item_texts, embedding_fn, embedding_batch_fn, embedding_batch_size
            )
        except Exception as exc:  # noqa: BLE001 - 任何后端异常都只降级，不改判决方向
            embedding_failure = f"skipped: embedding failed on the query side: {exc}"
    matcher = (
        _EmbeddingMatcher(item_vecs, threshold)
        if has_embedding and embedding_failure is None
        else None
    )

    exact_seen: set[int] = set()
    containment_hits = 0
    minhash_hits = 0
    unique_chunk_hashes: set[str] = set()
    emb_buffer: list[tuple[int, str]] = []
    total = len(chunks)

    def flush_embeddings() -> None:
        nonlocal embedding_failure
        if matcher is None or embedding_failure is not None or not emb_buffer:
            return
        texts = [text for _, text in emb_buffer]
        try:
            vecs = _embed_all(texts, embedding_fn, embedding_batch_fn, embedding_batch_size)
        except Exception as exc:  # noqa: BLE001 - 服务中途挂掉只降级，层 1-3 结果保留
            # 只停止继续送请求，不销毁 matcher：挂掉之前已经观测到的命中是
            # 真实证据，丢掉它等于把「已经查到的污染」一起抹掉。覆盖面仍按
            # 跳过登记，所以判决不会因为保留这些命中而变成 CLEAN。
            embedding_failure = f"skipped: embedding failed mid-scan: {exc}"
            emb_buffer.clear()
            return
        for (chunk_idx, _), vec in zip(emb_buffer, vecs):
            matcher.observe(chunk_idx, vec)
        emb_buffer.clear()

    # ---- 语料侧单趟流式扫描 ----
    for ci, chunk in enumerate(chunks):
        chunk_ngrams = ngram_set(chunk, ngram)
        # 退化碎片：低于 min_ngrams（含规范化后为空）都计数上报。
        if len(chunk_ngrams) < min_ngrams:
            report.degenerate_fragments += 1
        if not chunk_ngrams:
            # 规范化为空的 chunk 不参与打分，但上面已计数，跳过是可见的。
            if progress_fn is not None and progress_interval > 0 and (ci + 1) % progress_interval == 0:
                progress_fn(ci + 1, total)
            continue

        report.chunks_scanned += 1

        # 层 1：exact hash
        digest = hashlib.sha256(chunk.encode("utf-8")).hexdigest()
        unique_chunk_hashes.add(digest)
        for qi in hash_to_items.get(digest, ()):
            if qi not in exact_seen:
                exact_seen.add(qi)
                report.exact_matches.append((item_indices[qi], ci))

        # 层 2：directional containment（倒排计数，一次遍历得到所有查询的交集大小）
        if inverted:
            overlap: dict[int, int] = {}
            for gram in chunk_ngrams:
                for qi in inverted.get(gram, ()):
                    overlap[qi] = overlap.get(qi, 0) + 1
            for qi, shared in overlap.items():
                score = shared / len(item_ngrams[qi])
                if score >= threshold:
                    report.high_similarity.append(
                        (item_indices[qi], ci, float(score), LAYER_CONTAINMENT)
                    )
                    containment_hits += 1

        # 层 3：MinHash（banding 只做候选过滤，分数用全签名估计）
        if buckets:
            chunk_sig = minhash_signature(chunk, shingle_size=ngram)
            if chunk_sig:
                candidates: set[int] = set()
                for band in _band_keys(chunk_sig, minhash_bands):
                    candidates.update(buckets.get(band, ()))
                for qi in candidates:
                    score = _signature_agreement(item_sigs[qi], chunk_sig)
                    if score >= threshold:
                        report.high_similarity.append(
                            (item_indices[qi], ci, float(score), LAYER_MINHASH)
                        )
                        minhash_hits += 1

        # 层 4：embedding nearest-neighbor
        # chunk_vectors 给定时直接用库内向量，不发任何 chunk 侧请求——这正是 policy
        # ann_note/peak_rss_rationale 设想的路线（持有 24877 x 1024 矩阵做一次 matmul）。
        # 否则回落到批量 embedding：一旦记录了失败原因就不再送请求，对着已经挂掉的
        # 服务重试每个 batch 只会把同一个异常重复抛一遍，既拖满 SLA 又不改变判决。
        if matcher is not None and embedding_failure is None:
            if chunk_vectors is not None:
                matcher.observe(ci, chunk_vectors[ci])
            else:
                emb_buffer.append((ci, chunk))
                if len(emb_buffer) >= max(1, embedding_batch_size):
                    flush_embeddings()

        if progress_fn is not None and progress_interval > 0 and (ci + 1) % progress_interval == 0:
            progress_fn(ci + 1, total)

    flush_embeddings()
    emb_hits: list[tuple[int, int, float, str]] = []
    if matcher is not None:
        emb_hits = [
            (item_indices[qi], chunk_idx, score, layer)
            for qi, chunk_idx, score, layer in matcher.hits()
        ]
        report.high_similarity.extend(emb_hits)

    if progress_fn is not None and total and (progress_fn is not None):
        progress_fn(total, total)

    # ---- 层 notes / 覆盖度：说明扫描范围或跳过原因 ----
    report.layer_notes[LAYER_EXACT] = (
        f"{len(report.exact_matches)} exact match(es) over "
        f"{len(unique_chunk_hashes)} unique chunk hashes"
    )
    report.layer_notes[LAYER_CONTAINMENT] = (
        f"{containment_hits} pair(s) at containment_q >= {threshold} (n={ngram})"
    )
    report.layer_notes[LAYER_MINHASH] = (
        f"{minhash_hits} pair(s) above threshold {threshold} (bands={minhash_bands})"
    )
    report.layers_completed = [LAYER_EXACT, LAYER_CONTAINMENT, LAYER_MINHASH]
    if has_embedding and embedding_failure is None:
        report.layer_notes[LAYER_EMBEDDING] = f"{len(emb_hits)} pair(s) above threshold {threshold}"
        report.layers_completed.append(LAYER_EMBEDDING)
    else:
        # 「没配 embedding」和「配了但跑挂了」都是没跑完，都必须登记为跳过；
        # 后者额外把失败原因写进 note，否则事后无人能判断这次 INCOMPLETE 的来由。
        reason = embedding_failure or "skipped: embedding_fn not provided"
        if embedding_failure is not None and emb_hits:
            reason = f"{reason} ({len(emb_hits)} partial hit(s) kept from before the failure)"
        report.layer_notes[LAYER_EMBEDDING] = reason
        report.skipped_layers[LAYER_EMBEDDING] = reason

    if report.degenerate_fragments:
        report.layer_notes["degenerate_fragment"] = (
            f"{report.degenerate_fragments} chunk(s) below {min_ngrams} n-grams "
            "(counted and reported; never removed)"
        )

    # ---- 发布门：任何未审查的高相似 / 精确匹配都阻止发布 ----
    # 覆盖不全由 verdict 单独把关，见 ContaminationReport.verdict。
    report.blocking = bool(report.exact_matches) or bool(report.high_similarity)
    return report
