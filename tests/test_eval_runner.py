"""Evaluation runner tests (plan Task 3).

Offline-only: fixtures are written to tmp_path, no network, no models, no
Elasticsearch or alias changes.
"""

from __future__ import annotations

import json
import re

from orchestrator.eval.runner import redact_text, run_eval


def _write_jsonl(path, records) -> None:
    lines = "".join(json.dumps(r, ensure_ascii=False) + "\n" for r in records)
    path.write_text(lines, encoding="utf-8")


def _run(tmp_path, qrels, hits, visual_disabled=True):
    qrels_path = tmp_path / "qrels.jsonl"
    predictions_path = tmp_path / "predictions.jsonl"
    output_path = tmp_path / "report.json"
    _write_jsonl(qrels_path, qrels)
    _write_jsonl(predictions_path, hits)
    summary = run_eval(
        qrels_path=qrels_path,
        predictions_path=predictions_path,
        corpus_generation="techdocs-2026-07-30-v1",
        index_alias="knowledge_base_current",
        output_path=output_path,
        visual_disabled=visual_disabled,
    )
    return summary, output_path


def test_perfect_predictions_score_1_0_overall(tmp_path) -> None:
    qrels = [
        {
            "query_id": "q1",
            "document_id": "doc-a",
            "section_path": ["API"],
            "relevance": 2,
            "language": "en",
            "query_type": "concept",
            "source_id": "go",
        },
        {
            "query_id": "q2",
            "document_id": "doc-b",
            "section_path": ["guide"],
            "relevance": 1,
            "language": "zh",
            "query_type": "command",
            "source_id": "go",
        },
    ]
    hits = [
        {"query_id": "q1", "document_id": "doc-a", "section_path": ["API"], "score": 0.9},
        {"query_id": "q2", "document_id": "doc-b", "section_path": ["guide"], "score": 0.8},
    ]

    summary, output_path = _run(tmp_path, qrels, hits)

    assert summary["query_count"] == 2
    assert summary["overall"]["recall@5"] == 1.0
    assert summary["overall"]["mrr@10"] == 1.0
    assert summary["overall"]["ndcg@10"] == 1.0
    assert summary["empty_results"] == 0
    assert summary["wrong_hits"] == 0
    assert output_path.exists()


def test_report_has_per_source_language_and_query_type_sections(tmp_path) -> None:
    qrels = [
        {
            "query_id": "q1",
            "document_id": "doc-a",
            "section_path": ["API"],
            "relevance": 1,
            "language": "en",
            "query_type": "concept",
            "source_id": "go",
            "query": "how do I call an exported symbol",
        },
        {
            "query_id": "q2",
            "document_id": "doc-b",
            "section_path": ["start"],
            "relevance": 1,
            "language": "zh",
            "query_type": "command",
            "source_id": "python",
            "query": "如何安装依赖",
        },
    ]
    hits = [
        {"query_id": "q1", "document_id": "doc-a", "section_path": ["API"], "score": 0.5},
        {"query_id": "q2", "document_id": "doc-b", "section_path": ["start"], "score": 0.4},
    ]

    summary, output_path = _run(tmp_path, qrels, hits)

    assert summary["per_source"]["go"]["queries"] == 1
    assert summary["per_source"]["python"]["queries"] == 1
    assert summary["per_language"]["en"]["queries"] == 1
    assert summary["per_language"]["zh"]["queries"] == 1
    assert summary["per_query_type"]["concept"]["queries"] == 1
    assert summary["per_query_type"]["command"]["queries"] == 1

    report = json.loads(output_path.read_text(encoding="utf-8"))
    assert report["per_source"]["go"]["recall@5"] == 1.0
    assert report["per_language"]["zh"]["mrr@10"] == 1.0
    assert report["per_query_type"]["command"]["ndcg@10"] == 1.0
    # raw query text must never be written into the report, only query ids
    report_text = output_path.read_text(encoding="utf-8")
    assert "how do I call an exported symbol" not in report_text
    assert "如何安装依赖" not in report_text


def test_empty_results_and_wrong_hits_are_recorded(tmp_path) -> None:
    qrels = [
        {"query_id": "q1", "document_id": "doc-a", "section_path": [], "relevance": 1},
        {"query_id": "q2", "document_id": "doc-b", "section_path": [], "relevance": 1},
    ]
    # q1 gets one wrong hit (unknown document), q2 gets no predictions at all
    hits = [
        {"query_id": "q1", "document_id": "doc-x", "section_path": [], "score": 1.0},
    ]

    summary, output_path = _run(tmp_path, qrels, hits)

    assert summary["empty_results"] == 1
    assert summary["wrong_hits"] == 1
    assert summary["overall"]["recall@5"] == 0.0
    assert summary["overall"]["mrr@10"] == 0.0
    assert summary["overall"]["ndcg@10"] == 0.0
    # both queries are the worst (ndcg 0.0), listed deterministically by id
    assert [w["query_id"] for w in summary["worst_queries"][:2]] == ["q1", "q2"]

    report = json.loads(output_path.read_text(encoding="utf-8"))
    assert report["overall"]["empty_results"] == 1
    assert report["overall"]["wrong_hits"] == 1


def test_visual_disabled_is_reported_truthfully(tmp_path) -> None:
    qrels = [
        {"query_id": "q1", "document_id": "doc-a", "section_path": [], "relevance": 1}
    ]
    hits = [{"query_id": "q1", "document_id": "doc-a", "section_path": [], "score": 1.0}]

    summary, output_path = _run(tmp_path, qrels, hits, visual_disabled=True)

    assert summary["visual_disabled"] is True
    report = json.loads(output_path.read_text(encoding="utf-8"))
    assert report["visual"] == {"status": "disabled"}
    # no metrics section pretending the visual path succeeded
    assert "recall" not in str(report["visual"])


def test_visual_enabled_is_reported_as_enabled(tmp_path) -> None:
    qrels = [
        {"query_id": "q1", "document_id": "doc-a", "section_path": [], "relevance": 1}
    ]
    hits = [{"query_id": "q1", "document_id": "doc-a", "section_path": [], "score": 1.0}]

    summary, output_path = _run(tmp_path, qrels, hits, visual_disabled=False)

    assert summary["visual_disabled"] is False
    report = json.loads(output_path.read_text(encoding="utf-8"))
    assert report["visual"]["status"] == "enabled"


def test_report_never_leaks_api_key_from_environment(tmp_path, monkeypatch) -> None:
    fake_key = "sk-test-fake-key-0123456789abcdef"
    monkeypatch.setenv("DEEPSEEK_API_KEY", fake_key)
    qrels = [
        {"query_id": "q1", "document_id": "doc-a", "section_path": [], "relevance": 1}
    ]
    hits = [{"query_id": "q1", "document_id": "doc-a", "section_path": [], "score": 1.0}]

    _, output_path = _run(tmp_path, qrels, hits)

    report_text = output_path.read_text(encoding="utf-8")
    assert fake_key not in report_text
    assert re.search(r"(?i)\bsk-[a-z0-9_-]{16,}\b", report_text) is None


def test_redact_text_scrubs_common_key_shapes() -> None:
    secrets = (
        "sk-" + "a" * 32,
        "sk-proj-" + "b" * 24,
        "AKIA" + "ABCDEFGHIJKLMNOP",
        "ghp_" + "c" * 40,
        "xoxb-" + "d" * 12,
    )
    for secret in secrets:
        redacted = redact_text(f"prefix {secret} suffix")
        assert secret not in redacted
        assert "[REDACTED]" in redacted


# ---------------------------------------------------------------------------
# Phase 2.1 — nDCG boundary, dedup, relevance=0 exclusion contract tests
# ---------------------------------------------------------------------------


def test_ndcg_never_exceeds_1_with_multi_chunk_same_document(tmp_path) -> None:
    """nDCG must be ≤ 1.0 when the same document appears as multiple chunks in top-10."""
    qrels = [
        {"query_id": "q1", "document_id": "doc-a", "section_path": ["API"],
         "relevance": 2.0, "language": "en", "query_type": "concept", "source_id": "go"},
    ]
    # doc-a appears 3 times as different chunks — all in top 10
    hits = [
        {"query_id": "q1", "document_id": "doc-a", "section_path": ["API"], "score": 0.9},
        {"query_id": "q1", "document_id": "doc-a", "section_path": ["Overview"], "score": 0.8},
        {"query_id": "q1", "document_id": "doc-a", "section_path": ["Details"], "score": 0.7},
    ]
    summary, _ = _run(tmp_path, qrels, hits)
    ndcg = summary["overall"]["ndcg@10"]
    assert 0.0 <= ndcg <= 1.0, f"nDCG must be in [0,1], got {ndcg}"


def test_perfect_ranking_ndcg_equals_1(tmp_path) -> None:
    """All relevant docs at rank 1 → nDCG=1.0 (not > 1)."""
    qrels = [
        {"query_id": "q1", "document_id": "doc-a", "section_path": [], "relevance": 2},
        {"query_id": "q1", "document_id": "doc-b", "section_path": [], "relevance": 1},
    ]
    hits = [
        {"query_id": "q1", "document_id": "doc-a", "section_path": [], "score": 0.9},
        {"query_id": "q1", "document_id": "doc-b", "section_path": [], "score": 0.8},
    ]
    summary, _ = _run(tmp_path, qrels, hits)
    # Both qrels have same query_id=q1, so they're grouped.
    # The _score_query function handles multi-qrel queries — both match, nDCG=1.0
    assert summary["overall"]["ndcg@10"] == 1.0


def test_imperfect_ranking_ndcg_below_1(tmp_path) -> None:
    """Relevant doc at rank 3 instead of rank 1 → nDCG < 1.0.

    The DCG formula discounts at position i by 1/log2(i) for i>=2.
    Since log2(2)=1.0 the discount at rank 2 is 1.0 — same as rank 1.
    The first real discount is at rank 3: 1/log2(3) ≈ 0.631.
    So we put doc-a at rank 3 to get a measurable nDCG drop."""
    qrels = [
        {"query_id": "q1", "document_id": "doc-a", "section_path": [], "relevance": 2},
    ]
    hits = [
        {"query_id": "q1", "document_id": "doc-x", "section_path": [], "score": 0.9},
        {"query_id": "q1", "document_id": "doc-y", "section_path": [], "score": 0.85},
        {"query_id": "q1", "document_id": "doc-a", "section_path": [], "score": 0.8},
    ]
    summary, _ = _run(tmp_path, qrels, hits)
    ndcg = summary["overall"]["ndcg@10"]
    assert ndcg < 1.0, f"nDCG should be < 1.0 for doc at rank 3, got {ndcg}"


def test_relevance_zero_qrels_excluded_from_scoring(tmp_path) -> None:
    """Qrels with relevance=0 are NOT relevant — don't count for Recall/DCG/IDCG."""
    qrels = [
        {"query_id": "q1", "document_id": "doc-a", "section_path": [], "relevance": 0.0,
         "language": "en", "query_type": "concept", "source_id": "go"},
    ]
    hits = [
        {"query_id": "q1", "document_id": "doc-a", "section_path": [], "score": 0.9},
    ]
    summary, _ = _run(tmp_path, qrels, hits)
    assert summary["overall"]["recall@5"] == 0.0, "relevance=0 should give 0 recall"
    assert summary["overall"]["ndcg@10"] == 0.0, "no relevant docs => nDCG=0"


def test_hybrid_dedup_by_document_id(tmp_path) -> None:
    """Duplicate document in hits counted once at first occurrence."""
    qrels = [
        {"query_id": "q1", "document_id": "doc-a", "section_path": [], "relevance": 2},
        {"query_id": "q1", "document_id": "doc-b", "section_path": [], "relevance": 1},
    ]
    # doc-a at rank 1 AND rank 2 (duplicate chunks), doc-b at rank 3
    hits = [
        {"query_id": "q1", "document_id": "doc-a", "section_path": ["A"], "score": 0.9},
        {"query_id": "q1", "document_id": "doc-a", "section_path": ["B"], "score": 0.8},
        {"query_id": "q1", "document_id": "doc-b", "section_path": [], "score": 0.7},
    ]
    summary, _ = _run(tmp_path, qrels, hits)
    ndcg = summary["overall"]["ndcg@10"]
    assert 0.0 <= ndcg <= 1.0, f"nDCG must be in [0,1] with dedup, got {ndcg}"
    # doc-a (rel=2) at rank 1, duplicate skipped, doc-b (rel=1) at rank 3
    # IDCG = dcg([2, 1]) = 2.0 + 1/1.0 = 3.0
    # After dedup top10 = [doc-a, doc-b]; actual = [2.0, 1.0]
    # DCG = 2.0 + 1/log2(2) = 2.0 + 1.0 = 3.0; nDCG = 1.0
    # Wait — with dedup, we have [doc-a, doc-b] perfectly ordered, so nDCG = 1.0
    # The duplicate at rank 2 is skipped; doc-b slides from rank 3 to rank 2 in deduped list
    assert ndcg == 1.0, f"deduped [doc-a, doc-b] = perfect order => nDCG=1.0, got {ndcg}"
