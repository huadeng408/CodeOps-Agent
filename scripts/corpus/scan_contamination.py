"""Corpus contamination scan driver (post-import preflight gate).

Drives the four-layer contamination scanner (``eval/contamination/scanner.py``)
against the production corpus the importer just indexed:

1. scrolls every chunk out of the ES index (default
   ``knowledge_base_v2_bge_m3``), reading ``text_content`` with
   ``embedding_text`` as the fallback field;
2. loads the benchmark queries (``data/eval/techdocs/queries.text.jsonl``,
   one ``{"query_id","query"}`` record per line);
3. builds the embedding_fn for the local OpenAI-compatible embedding service;
   when the service is unreachable the fn degrades to ``None`` and the scanner
   skips the embedding layer (recording the reason in ``layer_notes``);
4. scans and writes a JSONL review report — one line per unreviewed hit:

   {"id": "<chunk_id>", "source": "<benchmark item summary>",
    "similarity": 0.95, "reviewed": false}

Exit codes: 0 = clean (no unreviewed overlap); 1 = blocking (unreviewed
exact / high-similarity hits found); 2 = parameter / IO error (unreadable
queries file, ES unreachable, report write failure). The driver only reports
— it never deletes or mutates corpus data.

Usage:
    python scripts/corpus/scan_contamination.py \\
        [--es http://127.0.0.1:9200] [--index knowledge_base_v2_bge_m3] \\
        [--queries data/eval/techdocs/queries.text.jsonl] \\
        [--embedding-url http://127.0.0.1:8009/embeddings] \\
        [--out results/contamination/report.jsonl]
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Callable, Optional

import requests

from eval.contamination.scanner import ContaminationReport, scan

DEFAULT_ES_URL = "http://127.0.0.1:9200"
DEFAULT_INDEX = "knowledge_base_v2_bge_m3"
DEFAULT_QUERIES_PATH = Path("data/eval/techdocs/queries.text.jsonl")
DEFAULT_EMBEDDING_URL = "http://127.0.0.1:8009/embeddings"
DEFAULT_EMBED_MODEL = "BAAI/bge-m3"
DEFAULT_OUT_PATH = Path("results/contamination/report.jsonl")

# chunk 文本字段优先级：text_content 优先，embedding_text 兜底。
TEXT_FIELDS = ("text_content", "embedding_text")
# 报告 source 字段里 benchmark item 摘要的最大长度。
SOURCE_SUMMARY_LIMIT = 120


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
) -> list[tuple[str, str]]:
    """Scroll every chunk out of ``index``; return (doc_id, text) pairs.

    Uses the classic scroll API: an initial ``GET /<index>/_search?scroll=``
    page, then ``POST /_search/scroll`` continuations chaining the previous
    ``_scroll_id`` until a page comes back empty. Documents without any
    usable text field are skipped. Raises ``requests.RequestException`` on
    transport/HTTP errors (``main`` maps that to the exit-2 path).
    """
    base = es_url.rstrip("/")
    body = {
        "size": batch_size,
        "query": {"match_all": {}},
        "_source": list(TEXT_FIELDS),
    }
    resp = requests.get(
        f"{base}/{index}/_search",
        params={"scroll": scroll_ttl},
        json=body,
        timeout=http_timeout,
    )
    resp.raise_for_status()
    payload = resp.json()

    chunks: list[tuple[str, str]] = []
    while True:
        hits = (payload.get("hits") or {}).get("hits") or []
        for hit in hits:
            text = chunk_text(hit.get("_source") or {})
            if text:
                chunks.append((str(hit.get("_id", "")), text))
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
    return chunks


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
        "--out", type=Path, default=DEFAULT_OUT_PATH, help="JSONL review report output path"
    )
    parser.add_argument("--threshold", type=float, default=0.8, help="similarity threshold for all layers")
    parser.add_argument("--ngram", type=int, default=8, help="character n-gram size")
    parser.add_argument("--minhash-bands", type=int, default=32, help="MinHash LSH bands")
    parser.add_argument("--batch-size", type=int, default=1000, help="ES page size for the scroll")
    parser.add_argument("--http-timeout", type=int, default=60, help="per-request HTTP timeout (ES + embedding)")
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)

    try:
        benchmark_items = load_queries(args.queries)
    except (OSError, json.JSONDecodeError) as exc:
        print(f"queries file error: {exc}", file=sys.stderr)
        return 2
    if not benchmark_items:
        print(f"no benchmark queries found in {args.queries}", file=sys.stderr)
        return 2

    try:
        chunks = fetch_chunks(
            args.es, args.index, batch_size=args.batch_size, http_timeout=args.http_timeout
        )
    except requests.RequestException as exc:
        print(f"ES scan failed for index {args.index}: {exc}", file=sys.stderr)
        return 2

    chunk_ids = [doc_id for doc_id, _ in chunks]
    chunk_texts = [text for _, text in chunks]

    embedding_fn = build_embedding_fn(args.embedding_url, timeout=args.http_timeout)

    report = scan(
        chunk_texts,
        benchmark_items,
        embedding_fn,
        ngram=args.ngram,
        minhash_bands=args.minhash_bands,
        threshold=args.threshold,
    )

    rows = report_to_jsonl_rows(report, chunk_ids, benchmark_items)
    try:
        write_jsonl_report(args.out, rows)
    except OSError as exc:
        print(f"report write failed: {exc}", file=sys.stderr)
        return 2

    print(f"scanned {len(chunk_texts)} chunk(s) against {len(benchmark_items)} benchmark item(s)")
    for layer, note in report.layer_notes.items():
        print(f"  {layer}: {note}")
    print(f"exact={len(report.exact_matches)} high_similarity={len(report.high_similarity)}")
    if report.blocking:
        print(f"BLOCKING: unreviewed contamination found; review {args.out}")
        return 1
    print("clean: no unreviewed contamination found")
    return 0


if __name__ == "__main__":
    sys.exit(main())
