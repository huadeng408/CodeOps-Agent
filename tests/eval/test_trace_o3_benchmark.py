from __future__ import annotations

from pathlib import Path
import json

from eval.adapter import EvalInstance, EvalResult
from eval.benchmarks import trace_o3
from eval.harness import HarnessRun, RunArtifacts


def test_load_instances_exposes_only_the_fixed_query_to_the_agent() -> None:
    instances = trace_o3.load_instances()

    assert [instance.instance_id for instance in instances] == ["trace-o3/go-q001"]
    task = instances[0].task_description
    assert "SearchKnowledge" in task
    assert "document_id" not in task
    assert "section_path" not in task
    assert "relevance" not in task


def test_official_scorer_uses_only_safe_tool_evidence(tmp_path: Path) -> None:
    instance = EvalInstance(instance_id="trace-o3/go-q001", task_description="not scored")
    result = EvalResult(
        instance_id=instance.instance_id,
        answer="The model answer is deliberately ignored by this scorer.",
        evidence={
            "retrieval_hits": [
                {
                    "rank": 1,
                    "document_id": "go@5d29d80b6c9960c3fc39e14e3e8b9a0f52041fed:doc/go_spec.html",
                    "chunk_id": 17,
                    "score": 0.9,
                }
            ]
        },
    )

    scored = trace_o3.score(result, instance, tmp_path)

    assert scored["official_verdict"] == "retrieved_relevant_document"
    assert scored["non_release_dev_smoke"] is True
    assert "The model answer" not in str(scored)
    raw = scored["scorer_raw_output"]["o3-techdocs-score.json"]
    assert "go-q001" in raw
    assert "The model answer" not in raw


def test_preflight_rejects_changed_pinned_inputs(tmp_path: Path) -> None:
    queries = tmp_path / "queries.jsonl"
    qrels = tmp_path / "qrels.jsonl"
    queries.write_text('{"query_id":"go-q001","query":"different"}\n', encoding="utf-8")
    qrels.write_text('{"query_id":"go-q001","document_id":"doc","relevance":1}\n', encoding="utf-8")

    report = trace_o3.preflight(queries_path=queries, qrels_path=qrels)

    assert report["ok"] is False
    assert "queries_sha256 mismatch" in report["problems"]
    assert "qrels_sha256 mismatch" in report["problems"]


def test_harness_persists_only_safe_o3_retrieval_evidence(tmp_path: Path) -> None:
    class Adapter:
        def solve_instance(self, instance, _working_dir, **_kwargs):
            return EvalResult(
                instance_id=instance.instance_id,
                answer="ignored by scorer",
                evidence={
                    "retrieval_hits": [
                        {
                            "rank": 1,
                            "document_id": "go@5d29d80b6c9960c3fc39e14e3e8b9a0f52041fed:doc/go_spec.html",
                            "chunk_id": 17,
                            "score": 0.9,
                        }
                    ]
                },
            )

    harness = HarnessRun(
        run_id="o3-evidence",
        artifacts=RunArtifacts("o3-evidence", tmp_path),
        adapter=Adapter(),
        scorer=trace_o3.score,
        config={
            "git_sha": "a" * 40,
            "model": "test-model",
            "trace_profile": "o3",
            "trace_capabilities": ("rag", "rerank"),
            "corpus_generation": trace_o3.CORPUS_GENERATION,
            "qrels_hash": trace_o3.QRELS_SHA256,
            "index_name": trace_o3.PHYSICAL_INDEX,
            "phoenix_url": "http://phoenix",
            "phoenix_project": "default",
            "trace_start_time": "2026-08-14T00:00:00Z",
        },
    )
    harness.run(trace_o3.load_instances())

    row = json.loads((harness.artifacts.root / "predictions.jsonl").read_text(encoding="utf-8"))
    assert row["evidence"]["retrieval_hits"][0]["document_id"].startswith("go@")
    assert "ignored by scorer" not in json.dumps(row["evidence"])
