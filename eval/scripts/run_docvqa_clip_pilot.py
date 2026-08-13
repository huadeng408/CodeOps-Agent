"""Run a pinned public DocVQA subset through the CLIP visual pilot.

The source contains upstream human annotations. This runner preserves their
stable identifiers and license metadata but does not convert them into project
human-review status. It never creates or switches an Elasticsearch alias.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import math
import os
import platform
import re
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any

from orchestrator.rag.visual.encoder import CLIPVisualEncoder

DATASET_ID = "vidore/docvqa_test_subsampled"
DATASET_REVISION = "49bf8f13e13c41dd8cdb0cae5314e31c1da1e0d6"
DATASET_LICENSE = "MIT"
MODEL_ID = "openai/clip-vit-base-patch32"
MODEL_REVISION = "3d74acf9a28c67741b2f4f2ea7635f0aaf6f0268"


def rank_ocr_text(qrels: list[dict[str, str]], pages: list[dict[str, str]]) -> list[dict[str, Any]]:
    """Rank OCR page text with a deterministic BM25 baseline.

    This baseline is intentionally local and lexical: it makes no claim to be
    a production text retriever, but supplies a reproducible comparison arm
    for the pinned public visual receipt.
    """
    tokenize = lambda value: re.findall(r"[\\w]+", value.lower(), flags=re.UNICODE)
    tokenized = [(page, tokenize(page["text"])) for page in pages]
    document_frequency: dict[str, int] = {}
    for _, terms in tokenized:
        for term in set(terms):
            document_frequency[term] = document_frequency.get(term, 0) + 1
    average_length = sum(len(terms) for _, terms in tokenized) / len(tokenized) if tokenized else 0.0
    ranked: list[dict[str, Any]] = []
    for qrel in qrels:
        query_terms = tokenize(qrel["query"])
        scored: list[tuple[float, dict[str, str]]] = []
        for page, terms in tokenized:
            term_frequency: dict[str, int] = {}
            for term in terms:
                term_frequency[term] = term_frequency.get(term, 0) + 1
            score = 0.0
            for term in query_terms:
                frequency = term_frequency.get(term, 0)
                if not frequency:
                    continue
                inverse_frequency = math.log(1 + (len(tokenized) - document_frequency.get(term, 0) + 0.5) / (document_frequency.get(term, 0) + 0.5))
                denominator = frequency + 1.2 * (1 - 0.75 + 0.75 * len(terms) / max(average_length, 1.0))
                score += inverse_frequency * frequency * 2.2 / denominator
            scored.append((score, page))
        for score, page in sorted(scored, key=lambda value: (-value[0], value[1]["page_id"])):
            ranked.append({
                "query_id": qrel["query_id"],
                "document_id": page["document_id"],
                "page_id": page["page_id"],
                "score": score,
            })
    return ranked


def visual_pilot_index_name(date_stamp: str, receipt_suffix: str = "") -> str:
    if not date_stamp.isdigit() or len(date_stamp) != 8:
        raise ValueError("visual pilot date must be YYYYMMDD")
    if receipt_suffix and (not receipt_suffix.replace("_", "").isalnum() or receipt_suffix != receipt_suffix.lower()):
        raise ValueError("visual pilot receipt suffix must be lowercase alphanumeric or underscores")
    suffix = f"_{receipt_suffix}" if receipt_suffix else ""
    return f"knowledge_page_visual_pilot_clip_{MODEL_REVISION[:8]}_{date_stamp}{suffix}"


def visual_index_mapping() -> dict[str, Any]:
    return {"mappings": {"properties": {
        "document_id": {"type": "keyword"}, "page_id": {"type": "keyword"},
        "page_ref": {"type": "keyword"}, "asset_sha256": {"type": "keyword"},
        "model": {"type": "keyword"}, "model_revision": {"type": "keyword"},
        "visual_vector": {"type": "dense_vector", "dims": 512, "index": True, "similarity": "cosine"},
    }}}


def _es_request(base_url: str, method: str, path: str, payload: Any | None = None) -> dict[str, Any]:
    data = json.dumps(payload).encode("utf-8") if payload is not None else None
    request = urllib.request.Request(base_url.rstrip("/") + path, data=data, method=method)
    request.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            return json.loads(response.read().decode("utf-8") or "{}")
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode("utf-8", errors="replace")[:512]
        raise RuntimeError(f"visual pilot Elasticsearch {method} {path} failed: HTTP {exc.code} {detail}") from exc


def index_and_rank_visual(es_url: str, index: str, images: list[dict[str, Any]], page_vectors: dict[str, list[float]], qrels: list[dict[str, str]], encoder: CLIPVisualEncoder) -> list[dict[str, Any]]:
    _es_request(es_url, "PUT", f"/{index}", visual_index_mapping())
    bulk_lines: list[str] = []
    for item in images:
        body = {**item, "visual_vector": page_vectors[item["page_id"]]}
        bulk_lines.extend([json.dumps({"index": {"_index": index, "_id": item["page_id"]}}), json.dumps(body)])
    request = urllib.request.Request(es_url.rstrip("/") + "/_bulk", data=("\n".join(bulk_lines) + "\n").encode("utf-8"), method="POST")
    request.add_header("Content-Type", "application/x-ndjson")
    with urllib.request.urlopen(request, timeout=60) as response:
        bulk = json.loads(response.read().decode("utf-8"))
    if bulk.get("errors"):
        raise RuntimeError("visual pilot Elasticsearch bulk indexing returned errors")
    _es_request(es_url, "POST", f"/{index}/_refresh")
    ranked: list[dict[str, Any]] = []
    for qrel in qrels:
        query_vector = encoder.encode_query(qrel["query"])
        result = _es_request(es_url, "POST", f"/{index}/_search", {"knn": {"field": "visual_vector", "query_vector": query_vector, "k": len(images), "num_candidates": len(images)}, "_source": ["document_id", "page_id"]})
        for hit in result.get("hits", {}).get("hits", []):
            source = hit["_source"]
            ranked.append({"query_id": qrel["query_id"], "document_id": source["document_id"], "page_id": source["page_id"], "score": float(hit["_score"])})
    return ranked


def select_public_subset(rows: list[dict[str, Any]], limit: int) -> list[dict[str, Any]]:
    if limit <= 0:
        raise ValueError("limit must be positive")
    valid = [row for row in rows if str(row.get("questionId", "")).strip() and str(row.get("query", "")).strip()]
    return sorted(valid, key=lambda row: str(row["questionId"]))[:limit]


def build_qrels(rows: list[dict[str, Any]]) -> list[dict[str, str]]:
    result: list[dict[str, str]] = []
    for row in rows:
        question_id = str(row["questionId"])
        document_id = f"docvqa:{row['docId']}"
        page = str(row["page"])
        result.append({
            "query_id": f"docvqa:{question_id}",
            "query": str(row["query"]),
            "document_id": document_id,
            "page_id": f"{document_id}:{page}",
            "source": DATASET_ID,
        })
    return result


def _sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def _write_json(path: Path, payload: Any) -> None:
    path.write_text(json.dumps(payload, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")


def _write_jsonl(path: Path, records: list[dict[str, Any]]) -> None:
    path.write_text("".join(json.dumps(record, ensure_ascii=False) + "\n" for record in records), encoding="utf-8")


def run(output_dir: Path, limit: int, device: str, es_url: str = "", date_stamp: str = "", receipt_suffix: str = "") -> dict[str, Any]:
    try:
        from datasets import load_dataset
        import torch
    except ImportError as exc:  # pragma: no cover - environment dependent
        raise RuntimeError("public visual pilot requires datasets and torch") from exc

    output_dir.mkdir(parents=True, exist_ok=False)
    started = time.time()
    dataset = load_dataset(DATASET_ID, revision=DATASET_REVISION, split="test")
    rows = select_public_subset([dict(dataset[index]) for index in range(len(dataset))], limit)
    if len(rows) != limit:
        raise RuntimeError(f"official dataset has only {len(rows)} valid rows, need {limit}")

    encoder = CLIPVisualEncoder(MODEL_ID, MODEL_REVISION, device)
    qrels = build_qrels(rows)
    images: list[dict[str, Any]] = []
    page_vectors: dict[str, list[float]] = {}
    image_payloads: list[bytes] = []
    for row, qrel in zip(rows, qrels, strict=True):
        image = row["image"].convert("RGB")
        asset_path = output_dir / "pages" / f"{qrel['query_id'].replace(':', '_')}.png"
        asset_path.parent.mkdir(exist_ok=True)
        image.save(asset_path, format="PNG")
        image_bytes = asset_path.read_bytes()
        image_payloads.append(image_bytes)
        images.append({
            "document_id": qrel["document_id"],
            "page_id": qrel["page_id"],
            "page_ref": str(asset_path.relative_to(output_dir)).replace("\\", "/"),
            "asset_sha256": hashlib.sha256(image_bytes).hexdigest(),
            **encoder.metadata,
        })

    for offset in range(0, len(images), 8):
        batch = images[offset:offset + 8]
        vectors = encoder.encode_pages(image_payloads[offset:offset + 8])
        for item, vector in zip(batch, vectors, strict=True):
            page_vectors[item["page_id"]] = vector

    ranked: list[dict[str, Any]] = []
    query_vectors = encoder.encode_queries([qrel["query"] for qrel in qrels])
    for qrel, query_vector in zip(qrels, query_vectors, strict=True):
        scores = []
        for item in images:
            score = sum(left * right for left, right in zip(query_vector, page_vectors[item["page_id"]], strict=True))
            scores.append((score, item))
        for score, item in sorted(scores, key=lambda pair: pair[0], reverse=True):
            ranked.append({
                "query_id": qrel["query_id"],
                "document_id": item["document_id"],
                "page_id": item["page_id"],
                "score": score,
            })

    pilot_index = ""
    if es_url:
        pilot_index = visual_pilot_index_name(date_stamp, receipt_suffix)
        ranked = index_and_rank_visual(es_url, pilot_index, images, page_vectors, qrels, encoder)

    _write_jsonl(output_dir / "qrels.public.jsonl", qrels)
    _write_jsonl(output_dir / "visual-ranked.jsonl", ranked)
    _write_jsonl(output_dir / "page-artifacts.jsonl", images)
    manifest = {
        "kind": "public_visual_pilot",
        "dataset": {"id": DATASET_ID, "revision": DATASET_REVISION, "license_spdx": DATASET_LICENSE, "upstream_annotations": "human-labeled"},
        "selection": {"method": "questionId lexical sort", "count": limit},
        "model": encoder.metadata,
        "runtime": {"python": sys.version.split()[0], "platform": platform.platform(), "cuda_available": torch.cuda.is_available()},
        "physical_visual_index": pilot_index,
        "alias_created": False,
        "alias_switched": False,
        "started_unix": started,
        "finished_unix": time.time(),
    }
    _write_json(output_dir / "manifest.json", manifest)
    checksums = {path.name: _sha256(path) for path in sorted(output_dir.glob("*.json*"))}
    _write_json(output_dir / "checksums.json", checksums)
    return manifest


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Pinned public DocVQA CLIP visual pilot")
    parser.add_argument("--out", required=True, type=Path)
    parser.add_argument("--limit", type=int, default=120)
    parser.add_argument("--device", choices=("cuda", "cpu"), default="cuda")
    parser.add_argument("--es-url", default="", help="optional Elasticsearch URL for a new isolated pilot index")
    parser.add_argument("--date", default=time.strftime("%Y%m%d"), help="YYYYMMDD suffix for a new visual index")
    parser.add_argument("--receipt-suffix", default="", help="lowercase unique suffix for an additional same-day receipt")
    args = parser.parse_args(argv)
    if args.out.exists():
        raise SystemExit(f"output directory already exists: {args.out}")
    manifest = run(args.out, args.limit, args.device, args.es_url, args.date, args.receipt_suffix)
    print(json.dumps(manifest, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
