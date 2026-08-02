"""Visual pilot bake-off entrypoint (plan Task 5.2).

Loads per-path ranked results + qrels and writes a JSON report with
nDCG@10 / Recall@5 / MRR@10 / bbox-hit per path. Pure offline scoring —
no model download, no alias creation.
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

from eval.benchmarks.vidore import compare_paths, load_qrels
from eval.retrieval.multimodal_metrics import Hit


def _load_ranked(path: str | None) -> dict[str, list[Hit]]:
    """Load ranked hits from JSON lines; None/empty => disabled path."""
    ranked: dict[str, list[Hit]] = {}
    if not path or not Path(path).exists():
        return ranked
    for line in Path(path).read_text(encoding="utf-8").splitlines():
        if not line.strip():
            continue
        record = json.loads(line)
        ranked.setdefault(record["query_id"], []).append(
            Hit(
                document_id=record["document_id"],
                page_id=record["page_id"],
                element_id=record.get("element_id", ""),
                bbox=record.get("bbox"),
                score=float(record.get("score", 0.0)),
            )
        )
    return ranked


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Visual pilot bake-off")
    parser.add_argument("--qrels", required=True, help="qrels JSONL path")
    parser.add_argument("--text", required=True, help="text-only ranked JSONL")
    parser.add_argument("--visual", default="", help="page-visual ranked JSONL (optional)")
    parser.add_argument("--late", default="", help="late-interaction ranked JSONL (optional)")
    parser.add_argument("--out", required=True, help="output JSON report path")
    args = parser.parse_args(argv)

    qrels = load_qrels(args.qrels)
    text = _load_ranked(args.text)
    visual = _load_ranked(args.visual)
    late = _load_ranked(args.late)

    results = compare_paths(text, visual, late, qrels)
    report = {
        "qrels": str(Path(args.qrels).resolve()),
        "query_count": len({q.query_id for q in qrels}),
        "paths": [r.to_dict() for r in results],
        "gpu_required": False,  # scoring is offline; encoding happens upstream
        "alias_created": False,
    }
    out = Path(args.out)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(report, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    print(json.dumps(report, indent=2, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    sys.exit(main())
