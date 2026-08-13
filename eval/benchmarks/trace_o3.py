"""Fixed one-instance TechDocs O3 development-receipt benchmark.

This module is intentionally narrow.  It establishes that a current-HEAD
agent/tool/RAG/scorer trace can be observed and audited for one pinned public
query.  It is never a release score, hidden holdout, or aggregate benchmark.
"""

from __future__ import annotations

import hashlib
import json
from pathlib import Path
from typing import Any

from eval.adapter import EvalInstance, EvalResult
from eval.harness.runner import SCORER_RAW_OUTPUT_KEY

TRACE_PROFILE = "o3"
TRACE_CAPABILITIES = ("rag", "rerank")
INSTANCE_ID = "trace-o3/go-q001"
QUERY_ID = "go-q001"
CORPUS_GENERATION = "techdocs-2026-07-30-v1"
PHYSICAL_INDEX = "knowledge_base_v2_bge_m3"
QUERIES_SHA256 = "15d549e61cbfcceb183dc087aeb1a749230c9d8b6c12acc1f229a3730d17a454"
QRELS_SHA256 = "bf472829c1a334a7eb624f5d6dc9c09002d562573f1b6e0d60e0e6f6b95329ad"

_ROOT = Path(__file__).parents[2]
_DEFAULT_QUERIES = _ROOT / "data" / "eval" / "techdocs" / "queries.text.jsonl"
_DEFAULT_QRELS = _ROOT / "data" / "eval" / "techdocs" / "qrels.text.jsonl"


def _sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def preflight(
    *,
    queries_path: Path = _DEFAULT_QUERIES,
    qrels_path: Path = _DEFAULT_QRELS,
) -> dict[str, Any]:
    """Check immutable local inputs without starting services or writing data."""
    problems: list[str] = []
    if not queries_path.is_file():
        problems.append("queries file missing")
    elif _sha256(queries_path) != QUERIES_SHA256:
        problems.append("queries_sha256 mismatch")
    if not qrels_path.is_file():
        problems.append("qrels file missing")
    elif _sha256(qrels_path) != QRELS_SHA256:
        problems.append("qrels_sha256 mismatch")
    return {
        "ok": not problems,
        "queries_sha256": _sha256(queries_path) if queries_path.is_file() else "",
        "qrels_sha256": _sha256(qrels_path) if qrels_path.is_file() else "",
        "corpus_generation": CORPUS_GENERATION,
        "physical_index": PHYSICAL_INDEX,
        "problems": problems,
    }


def _query_text() -> str:
    for raw_line in _DEFAULT_QUERIES.read_text(encoding="utf-8").splitlines():
        row = json.loads(raw_line)
        if row.get("query_id") == QUERY_ID:
            return str(row["query"])
    raise ValueError(f"pinned query {QUERY_ID!r} is missing")


def load_instances(limit: int | None = None, instance_ids: list[str] | None = None) -> list[EvalInstance]:
    """Return exactly the fixed, public O3 query without loading qrels."""
    if instance_ids is not None and instance_ids != [INSTANCE_ID]:
        raise ValueError("trace_o3 accepts only its pinned instance ID")
    if limit is not None and limit < 1:
        return []
    return [
        EvalInstance(
            instance_id=INSTANCE_ID,
            task_description=(
                "Answer the following technical question. Use SearchKnowledge before "
                "answering and rely on the retrieved material rather than prior memory.\n\n"
                + _query_text()
            ),
            metadata={"query_id": QUERY_ID, "benchmark_scope": "non_release_dev_smoke"},
        )
    ]


def _relevant_document_ids() -> set[str]:
    """Read qrels only inside the scorer boundary, never in the agent task."""
    ids: set[str] = set()
    for raw_line in _DEFAULT_QRELS.read_text(encoding="utf-8").splitlines():
        row = json.loads(raw_line)
        if row.get("query_id") == QUERY_ID and float(row.get("relevance", 0)) > 0:
            ids.add(str(row["document_id"]))
    if not ids:
        raise ValueError(f"pinned qrels have no positive document for {QUERY_ID}")
    return ids


def score(result: EvalResult, instance: EvalInstance, _workspace: Path) -> dict[str, Any]:
    """Score only the safe IDs from a real SearchKnowledge response.

    The LLM answer text is deliberately not inspected. This makes a tool-less
    fluent answer unscorable rather than allowing it to manufacture RAG credit.
    """
    if instance.instance_id != INSTANCE_ID or result.instance_id != INSTANCE_ID:
        raise ValueError("trace_o3 scorer received an unpinned instance")
    hits = result.evidence.get("retrieval_hits", [])
    if not isinstance(hits, list):
        raise ValueError("trace_o3 evidence retrieval_hits must be a list")
    relevant = _relevant_document_ids()
    safe_hits: list[dict[str, Any]] = []
    for hit in hits:
        if not isinstance(hit, dict):
            continue
        document_id = str(hit.get("document_id", "")).strip()
        if not document_id:
            continue
        safe_hits.append({
            "rank": int(hit.get("rank", 0)),
            "document_id": document_id,
            "chunk_id": hit.get("chunk_id"),
            "score": float(hit.get("score", 0.0)),
        })
    matched = next((hit for hit in safe_hits if hit["document_id"] in relevant), None)
    payload = {
        "scorer": "techdocs-o3-receipt-v1",
        "query_id": QUERY_ID,
        "non_release_dev_smoke": True,
        "official_verdict": "retrieved_relevant_document" if matched else "no_relevant_document_retrieved",
        "retrieved_hit_count": len(safe_hits),
        "first_relevant_rank": matched["rank"] if matched else None,
    }
    return {
        "official_verdict": payload["official_verdict"],
        "non_release_dev_smoke": True,
        SCORER_RAW_OUTPUT_KEY: {"o3-techdocs-score.json": json.dumps(payload, indent=2) + "\n"},
    }
