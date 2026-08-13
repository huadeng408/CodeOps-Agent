"""Create a local EasyOCR + BM25 baseline for a frozen DocVQA visual receipt.

DocVQA provides page images, not PDFs. This script therefore does not invoke
MinerU or Tika; production PDF ingestion remains MinerU plus explicit OCR.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import platform
import sys
import time
from pathlib import Path
from typing import Any

from eval.scripts.run_docvqa_clip_pilot import rank_ocr_text


def _read_jsonl(path: Path) -> list[dict[str, Any]]:
    return [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines() if line.strip()]


def _write_json(path: Path, payload: Any) -> None:
    path.write_text(json.dumps(payload, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")


def _write_jsonl(path: Path, records: list[dict[str, Any]]) -> None:
    path.write_text("".join(json.dumps(record, ensure_ascii=False) + "\n" for record in records), encoding="utf-8")


def run(receipt: Path, output_dir: Path, device: str) -> dict[str, Any]:
    try:
        import easyocr
        import torch
    except ImportError as exc:  # pragma: no cover - environment dependent
        raise RuntimeError("OCR baseline requires easyocr and torch") from exc
    if output_dir.exists():
        raise RuntimeError(f"output directory already exists: {output_dir}")
    if device == "cuda" and not torch.cuda.is_available():
        raise RuntimeError("OCR baseline requested cuda but CUDA is unavailable")
    pages = _read_jsonl(receipt / "page-artifacts.jsonl")
    qrels = _read_jsonl(receipt / "qrels.public.jsonl")
    if not pages or not qrels:
        raise RuntimeError("frozen visual receipt must contain pages and qrels")
    output_dir.mkdir(parents=True)
    reader = easyocr.Reader(["en"], gpu=device == "cuda", verbose=False)
    started = time.time()
    ocr_pages: list[dict[str, str]] = []
    for page in pages:
        image_path = receipt / page["page_ref"]
        if not image_path.is_file():
            raise RuntimeError(f"receipt page artifact is missing: {image_path}")
        lines = reader.readtext(str(image_path), detail=0, paragraph=True)
        text = "\n".join(str(line).strip() for line in lines if str(line).strip())
        ocr_pages.append({
            "document_id": page["document_id"],
            "page_id": page["page_id"],
            "page_ref": page["page_ref"],
            "text": text,
        })
    ranked = rank_ocr_text(qrels, ocr_pages)
    _write_jsonl(output_dir / "ocr-pages.jsonl", ocr_pages)
    _write_jsonl(output_dir / "text-ranked.jsonl", ranked)
    manifest = {
        "kind": "public_docvqa_ocr_text_baseline",
        "input_receipt": str(receipt.resolve()),
        "input_manifest_sha256": hashlib.sha256((receipt / "manifest.json").read_bytes()).hexdigest(),
        "engine": {"name": "easyocr", "version": easyocr.__version__, "languages": ["en"], "device": device},
        "ranking": {"algorithm": "bm25", "k1": 1.2, "b": 0.75, "tokenization": "unicode-word-regex-lowercase"},
        "selection_count": len(qrels),
        "runtime": {"python": sys.version.split()[0], "platform": platform.platform(), "cuda_available": torch.cuda.is_available()},
        "started_unix": started,
        "finished_unix": time.time(),
    }
    _write_json(output_dir / "manifest.json", manifest)
    checksums = {path.name: hashlib.sha256(path.read_bytes()).hexdigest() for path in sorted(output_dir.glob("*.json*"))}
    _write_json(output_dir / "checksums.json", checksums)
    return manifest


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Pinned DocVQA EasyOCR + BM25 baseline")
    parser.add_argument("--receipt", required=True, type=Path)
    parser.add_argument("--out", required=True, type=Path)
    parser.add_argument("--device", choices=("cuda", "cpu"), default="cuda")
    args = parser.parse_args(argv)
    print(json.dumps(run(args.receipt, args.out, args.device), ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
