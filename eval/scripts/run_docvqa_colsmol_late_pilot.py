"""Run pinned ColSmol late interaction on a frozen public visual receipt.

This runner intentionally has no Elasticsearch side effects.  It consumes the
same fixed page images and upstream-human-labelled qrels as the CLIP receipt,
then writes an independently checksummed ranking receipt for offline bake-off.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import platform
from pathlib import Path
import sys
import time
from typing import Any

ADAPTER_MODEL_ID = "vidore/colSmol-256M"
ADAPTER_REVISION = "a59110fdf114638b8018e6c9a018907e12f14855"
BASE_MODEL_ID = "vidore/ColSmolVLM-Instruct-256M-base"
BASE_REVISION = "99ca96f1f6b95b3a69e6abef74a2416cb738fed0"


def validate_source_receipt(receipt: Path) -> dict[str, Any]:
    """Validate the immutable public page receipt before loading any model."""
    manifest_path = receipt / "manifest.json"
    if not manifest_path.is_file():
        raise ValueError("source receipt manifest.json is required")
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    if manifest.get("kind") != "public_visual_pilot":
        raise ValueError("source receipt must be a public_visual_pilot")
    if not (receipt / "qrels.public.jsonl").is_file():
        raise ValueError("source receipt qrels.public.jsonl is required")
    if not (receipt / "page-artifacts.jsonl").is_file():
        raise ValueError("source receipt page-artifacts.jsonl is required")
    if not (receipt / "pages").is_dir():
        raise ValueError("source receipt pages directory is required")
    return manifest


def select_text_candidates(ranked: list[dict[str, Any]], top_n: int) -> dict[str, list[str]]:
    """Select bounded late-interaction candidates from frozen text rankings."""
    if top_n <= 0:
        raise ValueError("top_n must be positive")
    grouped: dict[str, list[dict[str, Any]]] = {}
    for record in ranked:
        query_id = str(record["query_id"])
        grouped.setdefault(query_id, []).append(record)
    return {
        query_id: [str(item["page_id"]) for item in sorted(items, key=lambda item: (-float(item["score"]), str(item["page_id"])))[:top_n]]
        for query_id, items in grouped.items()
    }


def _read_jsonl(path: Path) -> list[dict[str, Any]]:
    return [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines() if line.strip()]


def _write_json(path: Path, payload: Any) -> None:
    path.write_text(json.dumps(payload, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")


def _write_jsonl(path: Path, records: list[dict[str, Any]]) -> None:
    path.write_text("".join(json.dumps(record, ensure_ascii=False) + "\n" for record in records), encoding="utf-8")


def run(receipt: Path, text_receipt: Path, output_dir: Path, device: str, top_n: int = 20) -> dict[str, Any]:
    """Encode each frozen page once, then MaxSim-rerank frozen BM25 candidates."""
    if output_dir.exists():
        raise RuntimeError(f"output directory already exists: {output_dir}")
    source_manifest = validate_source_receipt(receipt)
    text_manifest_path = text_receipt / "manifest.json"
    text_ranked_path = text_receipt / "text-ranked.jsonl"
    if not text_manifest_path.is_file() or not text_ranked_path.is_file():
        raise ValueError("text receipt manifest.json and text-ranked.jsonl are required")
    text_manifest = json.loads(text_manifest_path.read_text(encoding="utf-8"))
    expected_source_digest = hashlib.sha256((receipt / "manifest.json").read_bytes()).hexdigest()
    if text_manifest.get("kind") != "public_docvqa_ocr_text_baseline" or text_manifest.get("input_manifest_sha256") != expected_source_digest:
        raise ValueError("text receipt must be the paired OCR/BM25 baseline for this frozen visual receipt")
    try:
        import torch
        from PIL import Image
        from colpali_engine.models import ColIdefics3, ColIdefics3Processor
        from peft import PeftModel
    except ImportError as exc:  # pragma: no cover - environment dependent
        raise RuntimeError("late-interaction pilot requires torch, Pillow, colpali-engine, and peft") from exc
    if device == "cuda" and not torch.cuda.is_available():
        raise RuntimeError("late-interaction pilot requested cuda but CUDA is unavailable")

    pages = _read_jsonl(receipt / "page-artifacts.jsonl")
    qrels = _read_jsonl(receipt / "qrels.public.jsonl")
    candidates = select_text_candidates(_read_jsonl(text_ranked_path), top_n)
    page_by_id = {str(page["page_id"]): page for page in pages}
    missing = [page_id for page_ids in candidates.values() for page_id in page_ids if page_id not in page_by_id]
    if missing:
        raise ValueError(f"text candidates refer to page(s) absent from frozen receipt: {missing[:3]}")

    output_dir.mkdir(parents=True)
    started = time.time()
    torch.cuda.reset_peak_memory_stats()
    base_model = ColIdefics3.from_pretrained(
        BASE_MODEL_ID,
        revision=BASE_REVISION,
        torch_dtype=torch.bfloat16,
        device_map=f"{device}:0" if device == "cuda" else "cpu",
        attn_implementation="eager",
    )
    model = PeftModel.from_pretrained(base_model, ADAPTER_MODEL_ID, revision=ADAPTER_REVISION).eval()
    model_device = next(model.parameters()).device
    processor = ColIdefics3Processor.from_pretrained(BASE_MODEL_ID, revision=BASE_REVISION)
    page_embeddings: dict[str, Any] = {}
    with torch.inference_mode():
        for page in pages:
            image_path = receipt / str(page["page_ref"])
            if not image_path.is_file():
                raise RuntimeError(f"receipt page artifact is missing: {image_path}")
            image = Image.open(image_path).convert("RGB")
            inputs = processor.process_images([image]).to(model_device)
            page_embeddings[str(page["page_id"])] = model(**inputs)[0].cpu()

        ranked: list[dict[str, Any]] = []
        for qrel in qrels:
            query_id = str(qrel["query_id"])
            candidate_ids = candidates.get(query_id)
            if not candidate_ids:
                raise RuntimeError(f"paired text receipt has no candidates for {query_id}")
            query_inputs = processor.process_queries([str(qrel["query"])]).to(model_device)
            query_embedding = model(**query_inputs)[0].cpu()
            scores = processor.score_multi_vector([query_embedding], [page_embeddings[page_id] for page_id in candidate_ids], device=model_device)[0].tolist()
            for score, page_id in sorted(zip(scores, candidate_ids, strict=True), key=lambda value: (-float(value[0]), value[1])):
                page = page_by_id[page_id]
                ranked.append({"query_id": query_id, "document_id": page["document_id"], "page_id": page_id, "score": float(score)})

    _write_jsonl(output_dir / "late-ranked.jsonl", ranked)
    manifest = {
        "kind": "public_docvqa_colsmol_late_interaction",
        "input_receipt": str(receipt.resolve()),
        "input_manifest_sha256": expected_source_digest,
        "candidate_receipt": str(text_receipt.resolve()),
        "candidate_manifest_sha256": hashlib.sha256(text_manifest_path.read_bytes()).hexdigest(),
        "candidate_source": "paired_easyocr_bm25_text_ranked",
        "top_n": top_n,
        "model": {"adapter": ADAPTER_MODEL_ID, "adapter_revision": ADAPTER_REVISION, "base": BASE_MODEL_ID, "base_revision": BASE_REVISION, "interaction": "MaxSim", "embedding_dimensions": 128},
        "selection_count": len(qrels),
        "runtime": {"python": sys.version.split()[0], "platform": platform.platform(), "device": str(model_device), "cuda_available": torch.cuda.is_available(), "peak_vram_mb": round(torch.cuda.max_memory_allocated() / 1024 / 1024, 1) if device == "cuda" else 0.0},
        "started_unix": started,
        "finished_unix": time.time(),
        "alias_created": False,
        "alias_switched": False,
        "source_dataset": source_manifest.get("dataset", {}),
    }
    _write_json(output_dir / "manifest.json", manifest)
    checksums = {path.name: hashlib.sha256(path.read_bytes()).hexdigest() for path in sorted(output_dir.glob("*.json*"))}
    _write_json(output_dir / "checksums.json", checksums)
    return manifest


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Pinned DocVQA ColSmol late-interaction pilot")
    parser.add_argument("--receipt", required=True, type=Path)
    parser.add_argument("--text-receipt", required=True, type=Path)
    parser.add_argument("--out", required=True, type=Path)
    parser.add_argument("--device", choices=("cuda", "cpu"), default="cuda")
    parser.add_argument("--top-n", type=int, default=20)
    args = parser.parse_args(argv)
    print(json.dumps(run(args.receipt, args.text_receipt, args.out, args.device, args.top_n), ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
