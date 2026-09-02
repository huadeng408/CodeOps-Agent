"""Corpus contamination scan driver (post-import preflight gate).

Drives the four-layer contamination scanner (``eval/contamination/scanner.py``)
against the production corpus the importer just indexed:

1. scrolls every chunk out of the ES index (default
   ``knowledge_base_v2_bge_m3``), reading ``text_content`` with
   ``embedding_text`` as the fallback field, and — unless
   ``--chunk-vector-field ''`` disables it — the stored ``vector`` field layer 4
   compares against;
2. loads the benchmark queries (``data/eval/techdocs/queries.text.jsonl``,
   one ``{"query_id","query"}`` record per line);
3. builds the embedding_fn for the local OpenAI-compatible embedding service;
   when the service is unreachable the fn degrades to ``None`` and the scanner
   skips the embedding layer (recording the reason in ``layer_notes``). Only the
   180 queries go through it by default — the corpus side reuses the vectors the
   index already holds;
4. scans and writes a JSONL review report — one line per unreviewed hit:

   {"id": "<chunk_id>", "source": "<benchmark item summary>",
    "similarity": 0.95, "reviewed": false}

Exit codes, frozen in data/eval/contamination/contamination-policy.v1.json:
0 = CLEAN (all four layers completed, no hits); 1 = BLOCKING (unreviewed
exact / above-threshold hits found); 2 = parameter / IO error (unreadable
queries file, ES unreachable, report write failure, missing or tampered
policy); 3 = INCOMPLETE (at least one layer was skipped or did not finish).

Exit code 3 is new in policy v1. A scan that did not complete every layer must
never print "clean" and must never exit 0, even with zero hits — zero hits from
three layers says nothing about the fourth. Any CI job or wrapper calling this
script must treat 3 as a failure rather than an unknown success. The driver
only reports — it never deletes or mutates corpus data.

Usage:
    python scripts/corpus/scan_contamination.py \\
        [--es http://127.0.0.1:9200] [--index knowledge_base_v2_bge_m3] \\
        [--queries data/eval/techdocs/queries.text.jsonl] \\
        [--embedding-url http://127.0.0.1:8009/embeddings] \\
        [--policy data/eval/contamination/contamination-policy.v1.json] \\
        [--out results/contamination/report.jsonl] \\
        [--manifest results/contamination/contamination-manifest.v1.json] \\
        [--chunk-vector-field vector]

The manifest records layer 4's provenance (``embedding_vector_source``,
``embedding_vector_field``, ``embedding_model_version``,
``embedding_text_field``) next to ``layer_text_field``, because
``embedding_hits: 0`` alone cannot tell a later reader whether layer 4 covered
the same string layers 1-3 hashed: the stored vectors were computed from
``embedding_text``, while layers 1-3 hash the ``text_content``-first chain.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import sys
import time
from datetime import datetime, timezone
from pathlib import Path
from typing import Callable, NamedTuple, Optional, Sequence

import requests

try:  # 与 eval/contamination/scanner.py 保持同一约定：软导入，缺了也能跑。
    import numpy as _np
except ImportError:  # pragma: no cover - only on a numpy-less install
    _np = None

# 本文件 docstring 里的退出码表是给 CI wrapper 的契约，而 wrapper 是用
# `python scripts/corpus/scan_contamination.py` 直接调的。裸调用时
# sys.path[0] 是 scripts/corpus/，`import eval` 会失败——门禁在 import 阶段
# 就死了，连退出码表里的任何一个码都给不出来。pytest 下能跑只是因为
# pyproject 配了 pythonpath=["."]，那是测试环境的便利，不是入口的能力。
_REPO_ROOT = Path(__file__).resolve().parents[2]
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from eval.contamination.scanner import (  # noqa: E402 - 必须在 sys.path 引导之后
    DEFAULT_EMBEDDING_BATCH,
    LAYER_CONTAINMENT,
    LAYER_EMBEDDING,
    LAYER_EXACT,
    LAYER_MINHASH,
    VERDICT_BLOCKING,
    VERDICT_CLEAN,
    VERDICT_INCOMPLETE,
    ContaminationReport,
    scan,
)

DEFAULT_ES_URL = "http://127.0.0.1:9200"
DEFAULT_INDEX = "knowledge_base_v2_bge_m3"
DEFAULT_QUERIES_PATH = Path("data/eval/techdocs/queries.text.jsonl")
DEFAULT_EMBEDDING_URL = "http://127.0.0.1:8009/embeddings"
DEFAULT_EMBED_MODEL = "BAAI/bge-m3"
DEFAULT_OUT_PATH = Path("results/contamination/report.jsonl")
# 冻结策略与其 sha256 sidecar：结果只在其所属 policy 下可解释。
# _REPO_ROOT 在上面的 sys.path 引导处已定义，此处直接复用。
DEFAULT_POLICY_PATH = _REPO_ROOT / "data" / "eval" / "contamination" / "contamination-policy.v1.json"
DEFAULT_MANIFEST_PATH = Path("results/contamination/contamination-manifest.v1.json")
SUPPORTED_POLICY_VERSIONS = ("v1",)

# chunk 文本字段优先级：text_content 优先，embedding_text 兜底。
TEXT_FIELDS = ("text_content", "embedding_text")
# 报告 source 字段里 benchmark item 摘要的最大长度。
SOURCE_SUMMARY_LIMIT = 120

# 层 4 默认复用索引里已有的向量字段（dense_vector，1024 维 cosine）。
# 传 "" 显式关闭，回到「向 embedding 服务重新编码整个语料」的路线。
DEFAULT_CHUNK_VECTOR_FIELD = "vector"
# 向量所属模型的记账字段：跨模型的余弦值没有可比性，必须单一。
MODEL_VERSION_FIELD = "model_version"
# 索引里的向量是对 embedding_text 编码得到的，而层 1-3 哈希的是 TEXT_FIELDS
# 优先级链（text_content 优先）。两者不是同一个字符串，这个语义差要落进
# manifest 由人判断，不能靠「跑通了」掩盖。
STORED_VECTOR_TEXT_FIELD = "embedding_text"
VECTOR_SOURCE_STORED = "es_stored_vectors"
VECTOR_SOURCE_SERVICE = "embedding_service"


class ChunkRecord(NamedTuple):
    """One chunk plus the stored vector layer 4 will compare against.

    Positional unpacking is part of the contract: layer 4 pairs ``chunks[i]``
    with ``chunk_vectors[i]`` by index, so the order this record is produced in
    is the order the scanner relies on.
    """

    doc_id: str
    text: str
    vector: list[float]
    model_version: str


# --------------------------------------------------------------------------- #
# ES chunk 拉取（scroll 分页）
# --------------------------------------------------------------------------- #


def chunk_text(source: dict) -> str:
    """First non-empty TEXT_FIELDS value in an ES ``_source``; '' when absent."""
    for field_name in TEXT_FIELDS:
        value = source.get(field_name)
        if isinstance(value, str) and value:
            return value
    return ""


def fetch_chunks(
    es_url: str,
    index: str,
    *,
    batch_size: int = 1000,
    scroll_ttl: str = "2m",
    http_timeout: int = 60,
    vector_field: Optional[str] = None,
) -> list[tuple[str, str]] | list[ChunkRecord]:
    """Scroll every chunk out of ``index``; return (doc_id, text) pairs.

    Uses the classic scroll API: an initial ``GET /<index>/_search?scroll=``
    page, then ``POST /_search/scroll`` continuations chaining the previous
    ``_scroll_id`` until a page comes back empty. Documents without any
    usable text field are skipped. Raises ``requests.RequestException`` on
    transport/HTTP errors (``main`` maps that to the exit-2 path).

    With ``vector_field`` set, the stored dense_vector and its
    ``model_version`` join ``_source`` and each element becomes a
    :class:`ChunkRecord` instead of a pair — layer 4 then compares against the
    vectors the index already holds rather than re-encoding the corpus.
    ``ValueError`` (mapped to exit 2 as well) is raised when a chunk that has
    text has no usable vector, or when the corpus mixes ``model_version``
    values:

    * dropping a vector-less chunk would leave layer 4 not covering it while
      the layer still reports itself completed — "not checked" written down as
      "checked and clean";
    * cosine similarity across two different embedding spaces neither proves
      contamination nor proves cleanliness, so one threshold cannot judge both.
    """
    base = es_url.rstrip("/")
    source_fields = list(TEXT_FIELDS)
    if vector_field:
        source_fields += [vector_field, MODEL_VERSION_FIELD]
    body = {
        "size": batch_size,
        "query": {"match_all": {}},
        "_source": source_fields,
    }
    resp = requests.get(
        f"{base}/{index}/_search",
        params={"scroll": scroll_ttl},
        json=body,
        timeout=http_timeout,
    )
    resp.raise_for_status()
    payload = resp.json()

    pairs: list[tuple[str, str]] = []
    records: list[ChunkRecord] = []
    model_versions: set[str] = set()
    while True:
        hits = (payload.get("hits") or {}).get("hits") or []
        for hit in hits:
            source = hit.get("_source") or {}
            text = chunk_text(source)
            if not text:
                continue
            doc_id = str(hit.get("_id", ""))
            if not vector_field:
                pairs.append((doc_id, text))
                continue
            raw_vector = source.get(vector_field)
            if not isinstance(raw_vector, list) or not raw_vector:
                raise ValueError(
                    f"chunk {doc_id!r} has text but no usable vector in field "
                    f"{vector_field!r}; refusing to drop it silently because layer 4 "
                    "would then skip it while still reporting itself completed"
                )
            model_version = str(source.get(MODEL_VERSION_FIELD) or "")
            model_versions.add(model_version)
            records.append(
                ChunkRecord(doc_id, text, [float(value) for value in raw_vector], model_version)
            )
        scroll_id = payload.get("_scroll_id")
        if not hits or not scroll_id:
            break
        resp = requests.post(
            f"{base}/_search/scroll",
            json={"scroll": scroll_ttl, "scroll_id": scroll_id},
            timeout=http_timeout,
        )
        resp.raise_for_status()
        payload = resp.json()

    if not vector_field:
        return pairs
    if len(model_versions) > 1:
        raise ValueError(
            f"index {index!r} mixes {len(model_versions)} embedding model versions "
            f"({sorted(model_versions)}); cosine similarity is only comparable inside "
            "one embedding space, so a single threshold cannot judge them together"
        )
    return records


# --------------------------------------------------------------------------- #
# Benchmark queries
# --------------------------------------------------------------------------- #


def load_queries(path: Path) -> list[str]:
    """Read benchmark queries from JSONL (``{"query_id","query"}`` per line).

    Blank lines and records without a non-empty ``query`` field are skipped.
    Raises OSError when the file is unreadable and json.JSONDecodeError on a
    malformed line (``main`` maps both to the exit-2 path).
    """
    queries: list[str] = []
    for line in path.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line:
            continue
        record = json.loads(line)
        query = record.get("query") if isinstance(record, dict) else None
        if isinstance(query, str) and query:
            queries.append(query)
    return queries


# --------------------------------------------------------------------------- #
# Embedding service
# --------------------------------------------------------------------------- #


def build_embedding_fn(
    embedding_url: str,
    *,
    model: str = DEFAULT_EMBED_MODEL,
    timeout: int = 30,
) -> Optional[Callable[[str], list[float]]]:
    """Build the OpenAI-compatible embedding callable; None when unreachable.

    Probes the service once with a ping request before the scan. The returned
    callable POSTs ``{"model", "input": [text]}`` and returns
    ``data[0].embedding`` (``[]`` when the response carries no vector, which
    the scanner treats as "no embedding for this text"). Any request/HTTP
    error during the probe degrades to None so the scanner skips the embedding
    layer and records the skip in ``layer_notes``.
    """

    def embed(text: str) -> list[float]:
        resp = requests.post(
            embedding_url, json={"model": model, "input": [text]}, timeout=timeout
        )
        resp.raise_for_status()
        payload = resp.json()
        data = payload.get("data") if isinstance(payload, dict) else None
        if not isinstance(data, list) or not data:
            return []
        embedding = (data[0] or {}).get("embedding")
        return list(embedding) if isinstance(embedding, list) else []

    try:
        embed("ping")
    except (requests.RequestException, ValueError) as exc:
        print(
            f"embedding service unreachable at {embedding_url}: {exc}; "
            "the embedding layer will be skipped",
            file=sys.stderr,
        )
        return None
    return embed


def build_embedding_batch_fn(
    embedding_url: str,
    *,
    model: str = DEFAULT_EMBED_MODEL,
    timeout: int = 30,
) -> Optional[Callable[[list[str]], list[list[float]]]]:
    """Build the batched embedding callable; None when the service is unreachable.

    Layer 4 on the real corpus embeds 24,877 chunks plus 180 queries. One HTTP
    round-trip per text cannot fit the frozen 600 s SLA, so batching is
    load-bearing rather than an optimisation.

    Two failure modes are treated as hard errors instead of being smoothed over,
    because both would corrupt the verdict rather than merely slow it down:

    * a response that carries fewer vectors than texts -- silently padding with
      ``[]`` would let those chunks skip layer 4 while the layer still reports
      itself completed, turning "not checked" into "checked and clean";
    * ``data[]`` out of order -- the OpenAI contract permits it, and pairing
      chunk A's text with chunk B's vector yields similarity numbers unrelated
      to the corpus.
    """

    def embed_batch(texts: list[str]) -> list[list[float]]:
        if not texts:
            return []
        resp = requests.post(
            embedding_url, json={"model": model, "input": list(texts)}, timeout=timeout
        )
        resp.raise_for_status()
        payload = resp.json()
        data = payload.get("data") if isinstance(payload, dict) else None
        if not isinstance(data, list):
            raise ValueError(
                f"embedding service returned no data array for {len(texts)} text(s)"
            )

        slots: list[Optional[list[float]]] = [None] * len(texts)
        for position, item in enumerate(data):
            if not isinstance(item, dict):
                continue
            raw_index = item.get("index")
            index = raw_index if isinstance(raw_index, int) else position
            if not 0 <= index < len(slots):
                raise ValueError(
                    f"embedding service returned index {index} outside 0..{len(slots) - 1}"
                )
            embedding = item.get("embedding")
            if not isinstance(embedding, list):
                continue
            slots[index] = [float(value) for value in embedding]

        missing = [i for i, vector in enumerate(slots) if vector is None]
        if missing:
            raise ValueError(
                f"embedding service returned {len(texts) - len(missing)} vectors "
                f"for {len(texts)} texts (missing indices: {missing[:5]})"
            )
        return [vector for vector in slots if vector is not None]

    try:
        embed_batch(["ping"])
    except (requests.RequestException, ValueError) as exc:
        print(
            f"embedding service unreachable at {embedding_url}: {exc}; "
            "the embedding layer will be skipped",
            file=sys.stderr,
        )
        return None
    return embed_batch


# --------------------------------------------------------------------------- #
# Report (preflight-review JSONL)
# --------------------------------------------------------------------------- #


def summarize(text: str, limit: int = SOURCE_SUMMARY_LIMIT) -> str:
    """One-line, length-bounded summary of a benchmark item (report source)."""
    compact = " ".join(text.split())
    if len(compact) <= limit:
        return compact
    return compact[:limit]


def report_to_jsonl_rows(
    report: ContaminationReport,
    chunk_ids: list[str],
    benchmark_items: list[str],
) -> list[dict]:
    """Convert a scan report into preflight-review JSONL rows.

    One row per (benchmark item, chunk) pair: exact matches score 1.0 and win
    over any high-similarity score for the same pair; otherwise the highest
    layer score is kept. ``id`` is the ES chunk id, falling back to
    ``"<bench_idx>:<chunk_idx>"`` when the chunk id is unknown. Rows are
    ordered by (bench_idx, chunk_idx) for deterministic output.
    """
    best: dict[tuple[int, int], float] = {}
    for bench_idx, chunk_idx in report.exact_matches:
        best[(bench_idx, chunk_idx)] = 1.0
    for bench_idx, chunk_idx, score, _layer in report.high_similarity:
        key = (bench_idx, chunk_idx)
        if key not in best or score > best[key]:
            best[key] = score

    rows: list[dict] = []
    for bench_idx, chunk_idx in sorted(best):
        chunk_id = chunk_ids[chunk_idx] if 0 <= chunk_idx < len(chunk_ids) else ""
        item = benchmark_items[bench_idx] if 0 <= bench_idx < len(benchmark_items) else ""
        rows.append(
            {
                "id": chunk_id or f"{bench_idx}:{chunk_idx}",
                "source": summarize(item),
                "similarity": best[(bench_idx, chunk_idx)],
                "reviewed": False,
            }
        )
    return rows


def write_jsonl_report(path: Path, rows: list[dict]) -> None:
    """Write one JSON object per line (UTF-8, CJK kept unescaped)."""
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", encoding="utf-8") as handle:
        for row in rows:
            handle.write(json.dumps(row, ensure_ascii=False) + "\n")


# --------------------------------------------------------------------------- #
# 冻结策略绑定 + manifest（一次扫描到底跑了什么的落盘凭证）
# --------------------------------------------------------------------------- #


class PolicyError(RuntimeError):
    """POLICY_MISSING / POLICY_HASH_MISMATCH / POLICY_VERSION_UNKNOWN / MANIFEST_*。"""


def load_policy(path: Path) -> tuple[dict, str]:
    """Load the frozen policy and verify it against its sha256 sidecar.

    Returns ``(policy_document, policy_sha256)``. Raises :class:`PolicyError` on
    POLICY_MISSING (absent / unreadable / not JSON), POLICY_HASH_MISMATCH (bytes
    disagree with the sidecar digest) or POLICY_VERSION_UNKNOWN. There is no
    default-threshold fallback: an unpinned scan yields a result nobody can
    interpret later, so the run must refuse to start instead.
    """
    try:
        raw = path.read_bytes()
    except OSError as exc:
        raise PolicyError(f"POLICY_MISSING: {path}: {exc}") from exc

    digest = hashlib.sha256(raw).hexdigest()
    sidecar = Path(f"{path}.sha256")
    try:
        recorded = sidecar.read_text(encoding="utf-8").split()[0]
    except (OSError, IndexError) as exc:
        raise PolicyError(f"POLICY_MISSING: unreadable sidecar for {path}: {exc}") from exc
    if recorded != digest:
        raise PolicyError(
            f"POLICY_HASH_MISMATCH: {path} hashes to {digest}, sidecar records {recorded}"
        )

    try:
        document = json.loads(raw.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise PolicyError(f"POLICY_MISSING: {path} is not readable JSON: {exc}") from exc

    version = document.get("policy_version")
    if version not in SUPPORTED_POLICY_VERSIONS:
        raise PolicyError(
            f"POLICY_VERSION_UNKNOWN: {version!r}; supported {list(SUPPORTED_POLICY_VERSIONS)}"
        )
    return document, digest


def build_manifest(
    report: ContaminationReport,
    *,
    policy: dict,
    policy_sha256: str,
    es_index: str,
    verdict: str,
    wall_clock_seconds: float,
    sla_exceeded: bool = False,
    vector_source: str = VECTOR_SOURCE_SERVICE,
    vector_field: str = "",
    embedding_model_version: str = "",
    embedding_text_field: str = "",
) -> dict:
    """Assemble the manifest required by ``policy['manifest']['required_fields']``.

    Pins the policy the run executed under (MANIFEST_STALE guard) and records
    which layers finished versus were skipped, so a terminated or degraded run
    can never be mistaken for a completed one. Raises :class:`PolicyError`
    (MANIFEST_MISSING_FIELD) rather than writing an under-specified manifest.

    The four ``embedding_*`` / ``layer_text_field`` entries record layer 4's
    provenance: where its chunk vectors came from, which ES field held them,
    which model produced them, and which text each side actually covered. They
    are additions on top of the policy's ``required_fields`` list, not
    substitutes for it — a reader who only sees ``embedding_hits: 0`` cannot
    otherwise tell whether the vectors were recomputed from the same string
    layers 1-3 hashed.
    """
    manifest = {
        "policy_id": policy.get("policy_id", ""),
        "policy_version": policy.get("policy_version", ""),
        "policy_sha256": policy_sha256,
        "generated_utc": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "es_index": es_index,
        "chunks_scanned": report.chunks_scanned,
        "queries_scanned": report.queries_scanned,
        "layers_completed": sorted(report.layers_completed),
        "layers_skipped": dict(report.skipped_layers),
        "verdict": verdict,
        "wall_clock_seconds": round(float(wall_clock_seconds), 3),
        "exact_hits": report.layer_hits(LAYER_EXACT),
        "containment_hits": report.layer_hits(LAYER_CONTAINMENT),
        "minhash_hits": report.layer_hits(LAYER_MINHASH),
        "embedding_hits": report.layer_hits(LAYER_EMBEDDING),
        "degenerate_fragments": report.degenerate_fragments,
        # SLA 审计：超时按 failure_semantics 记为 non_clean，证据留在 manifest 里。
        "sla_exceeded": bool(sla_exceeded),
        "sla_max_wall_clock_seconds": ((policy.get("sla") or {}).get("max_wall_clock_seconds")),
        # 层 4 溯源：向量从哪来、哪个字段、哪个模型、覆盖的是哪段文本。
        "embedding_vector_source": vector_source,
        "embedding_vector_field": vector_field,
        "embedding_model_version": embedding_model_version,
        "embedding_text_field": embedding_text_field,
        # 层 1-3 扫的文本字段优先级链，与上面一行对照着看才知道语义差在哪。
        "layer_text_field": ">".join(TEXT_FIELDS),
    }
    required = (policy.get("manifest") or {}).get("required_fields") or []
    missing = [name for name in required if name not in manifest]
    if missing:
        raise PolicyError(f"MANIFEST_MISSING_FIELD: {missing}")
    return manifest


def write_manifest(path: Path, manifest: dict) -> None:
    """Write the manifest as pretty-printed UTF-8 JSON with a trailing newline."""
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


# --------------------------------------------------------------------------- #
# CLI
# --------------------------------------------------------------------------- #


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Scan the imported corpus for benchmark contamination (preflight gate)."
    )
    parser.add_argument("--es", default=DEFAULT_ES_URL, help="Elasticsearch base URL")
    parser.add_argument("--index", default=DEFAULT_INDEX, help="corpus index to scan")
    parser.add_argument(
        "--queries",
        type=Path,
        default=DEFAULT_QUERIES_PATH,
        help="benchmark queries JSONL ({'query_id','query'} per line)",
    )
    parser.add_argument(
        "--embedding-url",
        default=DEFAULT_EMBEDDING_URL,
        help="OpenAI-compatible embedding endpoint",
    )
    parser.add_argument(
        "--policy",
        type=Path,
        default=DEFAULT_POLICY_PATH,
        help="frozen contamination policy JSON (verified against its .sha256 sidecar)",
    )
    parser.add_argument(
        "--out", type=Path, default=DEFAULT_OUT_PATH, help="JSONL review report output path"
    )
    parser.add_argument(
        "--manifest",
        type=Path,
        default=DEFAULT_MANIFEST_PATH,
        help="run manifest output path (what actually ran, per policy manifest.required_fields)",
    )
    parser.add_argument("--threshold", type=float, default=0.8, help="similarity threshold for all layers")
    parser.add_argument("--ngram", type=int, default=8, help="character n-gram size")
    parser.add_argument("--minhash-bands", type=int, default=32, help="MinHash LSH bands")
    parser.add_argument("--batch-size", type=int, default=1000, help="ES page size for the scroll")
    parser.add_argument("--http-timeout", type=int, default=60, help="per-request HTTP timeout (ES + embedding)")
    parser.add_argument(
        "--chunk-vector-field",
        default=DEFAULT_CHUNK_VECTOR_FIELD,
        help=(
            "ES dense_vector field holding the chunk vectors layer 4 compares against "
            f"(default {DEFAULT_CHUNK_VECTOR_FIELD!r}); pass '' to re-encode the whole "
            "corpus through the embedding service instead"
        ),
    )
    return parser.parse_args(argv)


def build_chunk_vector_matrix(vectors: list[list[float]]):
    """Stack stored chunk vectors for layer 4; float32 when numpy is available.

    The frozen policy's ``peak_rss_rationale`` budgets "a 24877 x 1024 float32
    matrix (about 102 MB)" — the same data as Python lists is roughly 600 MB, so
    the dtype is what keeps the run inside the budget rather than a micro
    optimisation. Without numpy the lists are returned unchanged: still correct,
    still under the 2048 MB ceiling, just heavier.

    Raises ``ValueError`` when the vectors disagree on dimension. A ragged stack
    would either fail deep inside the matmul or silently produce cosines that
    describe nothing.
    """
    if not vectors:
        return vectors
    widths = {len(vector) for vector in vectors}
    if len(widths) > 1:
        raise ValueError(
            f"stored chunk vectors disagree on dimension ({sorted(widths)}); "
            "cosine similarity across mixed dimensions is meaningless"
        )
    if _np is None:  # pragma: no cover - only on a numpy-less install
        return vectors
    return _np.asarray(vectors, dtype=_np.float32)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    started = time.monotonic()

    # 先绑定冻结策略：没有 policy 的扫描结果事后无人能解释，宁可不跑。
    try:
        policy, policy_sha256 = load_policy(args.policy)
    except PolicyError as exc:
        print(str(exc), file=sys.stderr)
        return 2
    sla = policy.get("sla") or {}
    max_wall_clock = sla.get("max_wall_clock_seconds")
    progress_interval = int(sla.get("progress_report_interval_chunks") or DEFAULT_PROGRESS_INTERVAL)
    min_ngrams = int((policy.get("degenerate_fragment") or {}).get("min_ngrams") or DEFAULT_MIN_NGRAMS)
    # 第 4 层的 batch_size 由策略决定（layers[].id == 4），driver 不得自定。
    embedding_batch_size = DEFAULT_EMBEDDING_BATCH
    for layer in policy.get("layers") or []:
        if isinstance(layer, dict) and layer.get("name") == LAYER_EMBEDDING:
            embedding_batch_size = int(layer.get("batch_size") or DEFAULT_EMBEDDING_BATCH)
            break

    try:
        benchmark_items = load_queries(args.queries)
    except (OSError, json.JSONDecodeError) as exc:
        print(f"queries file error: {exc}", file=sys.stderr)
        return 2
    if not benchmark_items:
        print(f"no benchmark queries found in {args.queries}", file=sys.stderr)
        return 2

    # 层 4 的向量来源。默认复用索引里已有的 dense_vector：首次全量真实执行中，
    # 重新编码整个语料把服务顶到单批 183.57s（客户端 60s 读超时），
    # 层 4 因此没跑完；而库内向量与冻结 revision 同源，实测
    # cos(stored, fresh[embedding_text]) = 1.000000，冻结策略的 ann_note 与
    # peak_rss_rationale 写的本来就是「持有这批向量做一次 matmul」。
    vector_field = (args.chunk_vector_field or "").strip()
    try:
        chunks = fetch_chunks(
            args.es,
            args.index,
            batch_size=args.batch_size,
            http_timeout=args.http_timeout,
            vector_field=vector_field or None,
        )
    except requests.RequestException as exc:
        print(f"ES scan failed for index {args.index}: {exc}", file=sys.stderr)
        return 2
    except ValueError as exc:
        # 取数故障必须落在 rc 2。未捕获异常让解释器退 1，而 1 在冻结策略里是
        # BLOCKING = 「发现污染」——一个读不到向量的故障绝不能伪装成污染判决。
        print(f"chunk vectors unusable in index {args.index}: {exc}", file=sys.stderr)
        return 2

    # ChunkRecord 与二元组都按位置解包，所以取 id/text 不需要分支。
    chunk_ids = [record[0] for record in chunks]
    chunk_texts = [record[1] for record in chunks]

    chunk_vectors = None
    embedding_model_version = ""
    embedding_text_field = ">".join(TEXT_FIELDS)
    vector_source = VECTOR_SOURCE_SERVICE
    if vector_field:
        try:
            # 形状要验证，不能从「我传了 vector_field」推断「收到的一定是
            # ChunkRecord」。真不符时 record.vector 抛 AttributeError 会一路逃到
            # 解释器 → 退出码 1，而 1 在冻结策略里是 BLOCKING = 「发现污染」。
            # 取数形状故障绝不能伪装成污染判决，所以显式验形并落到 rc 2。
            shapeless = next(
                (record for record in chunks if not isinstance(record, ChunkRecord)), None
            )
            if shapeless is not None:
                raise ValueError(
                    f"requested chunk vectors from field {vector_field!r} but fetch_chunks "
                    f"returned {type(shapeless).__name__} without a vector; layer 4 would "
                    "have nothing to compare against"
                )
            chunk_vectors = build_chunk_vector_matrix([record.vector for record in chunks])
        except ValueError as exc:
            print(f"chunk vectors unusable in index {args.index}: {exc}", file=sys.stderr)
            return 2
        vector_source = VECTOR_SOURCE_STORED
        # 库内向量算的是 embedding_text，层 1-3 哈希的是 TEXT_FIELDS 优先级链。
        embedding_text_field = STORED_VECTOR_TEXT_FIELD
        # fetch_chunks 已对混合 model_version 抛错；这里只是不替一个越界的
        # 假实现挑代表值——真混了就留空，让读报告的人看见它没被确定下来。
        model_versions = sorted({record.model_version for record in chunks})
        embedding_model_version = model_versions[0] if len(model_versions) == 1 else ""

    # 第 4 层只走批量这一个入口。单条 HTTP 往返乘以 24,877 个 chunk 装不进
    # 600s SLA，而 batch_size=1 的批量调用与单条调用等价，所以单条入口在
    # driver 里是纯冗余。更要紧的是：两个独立的网络入口意味着只 patch 其中
    # 一个的测试会静默打到真实服务上（本轮就发生过），判决面因此不可信。
    # build_embedding_fn 仍作为公开 helper 保留给其他调用方，但 main 不再用它。
    embedding_batch_fn = build_embedding_batch_fn(args.embedding_url, timeout=args.http_timeout)
    embedding_fn = None

    def report_progress(done: int, total: int) -> None:
        # 进度走 stderr：stdout 是判定面，不能被进度噪声污染。
        print(f"  scanned {done}/{total} chunks in {time.monotonic() - started:.1f}s", file=sys.stderr)

    report = scan(
        chunk_texts,
        benchmark_items,
        embedding_fn,
        ngram=args.ngram,
        minhash_bands=args.minhash_bands,
        threshold=args.threshold,
        min_ngrams=min_ngrams,
        embedding_batch_fn=embedding_batch_fn,
        embedding_batch_size=embedding_batch_size,
        chunk_vectors=chunk_vectors,
        progress_fn=report_progress,
        progress_interval=progress_interval,
    )
    wall_clock = time.monotonic() - started

    # SLA 超时按 policy failure_semantics 处理为 non_clean：保留 manifest，不给部分通过。
    verdict = report.verdict
    sla_exceeded = isinstance(max_wall_clock, (int, float)) and wall_clock > float(max_wall_clock)
    if sla_exceeded and verdict == VERDICT_CLEAN:
        verdict = VERDICT_INCOMPLETE

    rows = report_to_jsonl_rows(report, chunk_ids, benchmark_items)
    try:
        write_jsonl_report(args.out, rows)
    except OSError as exc:
        print(f"report write failed: {exc}", file=sys.stderr)
        return 2

    try:
        manifest = build_manifest(
            report,
            policy=policy,
            policy_sha256=policy_sha256,
            es_index=args.index,
            verdict=verdict,
            wall_clock_seconds=round(wall_clock, 3),
            sla_exceeded=sla_exceeded,
            vector_source=vector_source,
            vector_field=vector_field,
            embedding_model_version=embedding_model_version,
            embedding_text_field=embedding_text_field,
        )
        write_manifest(args.manifest, manifest)
    except (PolicyError, OSError) as exc:
        print(f"manifest write failed: {exc}", file=sys.stderr)
        return 2

    print(f"scanned {report.chunks_scanned} chunk(s) against {report.queries_scanned} benchmark item(s)")
    for layer, note in report.layer_notes.items():
        print(f"  {layer}: {note}")
    print(
        f"exact={report.layer_hits(LAYER_EXACT)} "
        f"containment={report.layer_hits(LAYER_CONTAINMENT)} "
        f"minhash={report.layer_hits(LAYER_MINHASH)} "
        f"embedding={report.layer_hits(LAYER_EMBEDDING)} "
        f"degenerate_fragments={report.degenerate_fragments}"
    )
    print(f"wall_clock={wall_clock:.1f}s manifest={args.manifest}")
    if sla_exceeded:
        print(f"SLA_WALL_CLOCK_EXCEEDED: {wall_clock:.1f}s over the frozen {max_wall_clock}s budget")

    if verdict == VERDICT_BLOCKING:
        print(f"BLOCKING: unreviewed contamination found; review {args.out}")
        return 1
    if verdict == VERDICT_INCOMPLETE:
        # 覆盖不全时禁止出现 clean 字样：少跑一层就不知道那一层会发现什么。
        print("INCOMPLETE: the scan did not finish every layer, so its coverage cannot be vouched for")
        for layer, reason in sorted(report.skipped_layers.items()):
            print(f"  skipped layer {layer}: {reason}")
        print(f"review the incomplete manifest at {args.manifest}")
        return 3
    print("clean: no unreviewed contamination found")
    return 0


if __name__ == "__main__":
    sys.exit(main())
