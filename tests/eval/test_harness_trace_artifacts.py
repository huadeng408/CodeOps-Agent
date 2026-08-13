"""O2 wiring: a real HarnessRun writes the ``traces/`` subtree (§20.6.4/§20.7).

The H5 gate needs ONE artifact that simultaneously holds manifest, prediction,
official score, **trace** and checksums.  Everything but ``traces/`` was already
satisfied; these tests pin the missing item to the harness lifecycle rather than
to a script someone has to remember to run afterwards.

The adapter used here is a deterministic fake, which per §20.1 rule 6 proves
only that the *wiring* works.  What it must prove is that the spans are
produced by the harness executing its own lifecycle — no test writes a span
dict, and no test writes into ``traces/`` directly.
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from eval.adapter import EvalInstance, EvalResult
from eval.harness.artifacts import RunArtifacts
from eval.harness.runner import HarnessRun
from eval.harness.trace_contract import (
    SPAN_EVAL_INSTANCE,
    SPAN_EVAL_RUN,
    SPAN_SCORER_OFFICIAL,
    VERDICT_PASS,
)

pytest.importorskip("opentelemetry.sdk.trace")

TRACE_SUMMARY = "traces/trace-summary.json"
SPAN_ASSERTION = "traces/span-assertion.json"


class _FakeAdapter:
    def solve_instance(self, instance: EvalInstance, working_dir: str, **kwargs) -> EvalResult:
        return EvalResult(instance_id=instance.instance_id, model_patch="diff --git a b\n")


class _ExplodingAdapter:
    def solve_instance(self, instance: EvalInstance, working_dir: str, **kwargs) -> EvalResult:
        raise RuntimeError("agent exploded")


def _harness(tmp_path: Path, **kwargs) -> HarnessRun:
    artifacts = RunArtifacts("run-trace", tmp_path)
    config = {"git_sha": "abc1234", "model": "deepseek-v4-pro", "benchmark": "swebench"}
    config.update(kwargs.pop("config", {}))
    return HarnessRun(
        run_id="run-trace",
        artifacts=artifacts,
        adapter=kwargs.pop("adapter", _FakeAdapter()),
        config=config,
        **kwargs,
    )


def _read(root: Path, rel: str) -> dict:
    return json.loads((root / rel).read_text(encoding="utf-8"))


def test_run_writes_trace_subtree(tmp_path: Path) -> None:
    harness = _harness(tmp_path)
    harness.run([EvalInstance(instance_id="inst-1", task_description="t")])

    root = harness.artifacts.root
    assert (root / TRACE_SUMMARY).exists(), "traces/trace-summary.json missing"
    assert (root / SPAN_ASSERTION).exists(), "traces/span-assertion.json missing"


def test_trace_artifacts_are_pinned_by_checksums(tmp_path: Path) -> None:
    """§20.7: trace evidence outside ``checksums.sha256`` is unpinned, so it is
    not evidence.  The recursion already exists; this proves it fires."""
    harness = _harness(tmp_path)
    harness.run([EvalInstance(instance_id="inst-1", task_description="t")])

    root = harness.artifacts.root
    pinned = (root / "checksums.sha256").read_text(encoding="utf-8")
    assert TRACE_SUMMARY in pinned
    assert SPAN_ASSERTION in pinned
    assert harness.artifacts.verify_checksums() == []


def test_captured_spans_come_from_the_harness_lifecycle(tmp_path: Path) -> None:
    harness = _harness(tmp_path)
    harness.run([EvalInstance(instance_id="inst-1", task_description="t")])

    summary = _read(harness.artifacts.root, TRACE_SUMMARY)
    names = {span["name"] for span in summary["spans"]}
    assert SPAN_EVAL_RUN in names
    assert SPAN_EVAL_INSTANCE in names
    assert summary["span_count"] >= 2
    # Real OTel identifiers, not placeholders.
    assert all(len(span["trace_id"]) == 32 for span in summary["spans"])


def test_instance_span_is_a_child_of_the_run_span(tmp_path: Path) -> None:
    harness = _harness(tmp_path)
    harness.run([EvalInstance(instance_id="inst-1", task_description="t")])

    summary = _read(harness.artifacts.root, TRACE_SUMMARY)
    by_name = {span["name"]: span for span in summary["spans"]}
    run_span = by_name[SPAN_EVAL_RUN]
    instance_span = by_name[SPAN_EVAL_INSTANCE]
    assert instance_span["parent_span_id"] == run_span["span_id"]
    assert instance_span["trace_id"] == run_span["trace_id"]


def test_join_attributes_are_on_the_spans(tmp_path: Path) -> None:
    harness = _harness(tmp_path)
    harness.run([EvalInstance(instance_id="inst-1", task_description="t")])

    summary = _read(harness.artifacts.root, TRACE_SUMMARY)
    by_name = {span["name"]: span for span in summary["spans"]}
    assert by_name[SPAN_EVAL_RUN]["attributes"]["eval.run_id"] == "run-trace"
    assert by_name[SPAN_EVAL_RUN]["attributes"]["git.commit"] == "abc1234"
    assert by_name[SPAN_EVAL_INSTANCE]["attributes"]["eval.instance_id"] == "inst-1"


def test_scorer_official_span_is_created(tmp_path: Path) -> None:
    """§20.6.4 item 2: the official scorer adapter gets a ``scorer.official``
    span.  The harness owns the scorer callback, so it owns the span."""
    harness = _harness(tmp_path)
    harness.scorer = lambda result, instance, workspace: {"resolved": True}
    harness.run([EvalInstance(instance_id="inst-1", task_description="t")])

    summary = _read(harness.artifacts.root, TRACE_SUMMARY)
    names = {span["name"] for span in summary["spans"]}
    assert SPAN_SCORER_OFFICIAL in names


def test_scorer_failure_still_ends_the_span_with_error(tmp_path: Path) -> None:
    def _boom(result, instance, workspace):
        raise RuntimeError("official scorer unreachable")

    harness = _harness(tmp_path)
    harness.scorer = _boom
    harness.run([EvalInstance(instance_id="inst-1", task_description="t")])

    summary = _read(harness.artifacts.root, TRACE_SUMMARY)
    scorer_spans = [s for s in summary["spans"] if s["name"] == SPAN_SCORER_OFFICIAL]
    assert scorer_spans, "scorer span vanished on failure"
    assert scorer_spans[0]["status"] == "ERROR"


def test_agent_failure_still_ends_the_instance_span_with_error(tmp_path: Path) -> None:
    harness = _harness(tmp_path, adapter=_ExplodingAdapter())
    harness.run([EvalInstance(instance_id="inst-1", task_description="t")])

    summary = _read(harness.artifacts.root, TRACE_SUMMARY)
    by_name = {span["name"]: span for span in summary["spans"]}
    assert by_name[SPAN_EVAL_INSTANCE]["status"] == "ERROR"


def test_span_assertion_reports_incomplete_without_agent_and_rag_spans(tmp_path: Path) -> None:
    """The honest verdict for today's environment.

    Phoenix is down and the Go agent is not running, so ``invoke_agent`` /
    ``chat`` / ``rag.retrieve`` / ``embedding`` cannot exist.  The artifact
    must say INCOMPLETE and name them.  If this ever asserts PASS while those
    services are down, the artifact is lying.
    """
    harness = _harness(tmp_path)
    harness.scorer = lambda result, instance, workspace: {"resolved": True}
    harness.run([EvalInstance(instance_id="inst-1", task_description="t")])

    assertion = _read(harness.artifacts.root, SPAN_ASSERTION)
    assert assertion["verdict"] != VERDICT_PASS
    assert "invoke_agent" in assertion["missing_required_kinds"]
    assert "rag.retrieve" in assertion["missing_required_kinds"]
    assert assertion["contract_version"]


def test_span_assertion_records_run_id_for_the_join(tmp_path: Path) -> None:
    harness = _harness(tmp_path)
    harness.run([EvalInstance(instance_id="inst-1", task_description="t")])

    assertion = _read(harness.artifacts.root, SPAN_ASSERTION)
    assert assertion["run_id"] == "run-trace"
    assert assertion["primary_trace_id"]


def test_o3_trace_profile_is_explicitly_written_to_assertion(tmp_path: Path) -> None:
    class O3Adapter:
        def solve_instance(self, instance: EvalInstance, working_dir: str, **kwargs) -> EvalResult:
            return EvalResult(
                instance_id=instance.instance_id,
                answer="answer",
                evidence={
                    "retrieval_hits": [
                        {"rank": 1, "document_id": "doc", "chunk_id": 1, "score": 0.9}
                    ]
                },
            )

    harness = _harness(
        tmp_path,
        adapter=O3Adapter(),
        config={
            "trace_profile": "o3",
            "trace_capabilities": ("rag", "rerank"),
            "corpus_generation": "techdocs-2026-07-30-v1",
            "qrels_hash": "a" * 64,
            "index_name": "knowledge_base_v2_bge_m3",
        },
    )
    harness.run([EvalInstance(instance_id="inst-1", task_description="t")])

    assertion = _read(harness.artifacts.root, SPAN_ASSERTION)
    assert assertion["profile"] == "o3"


def test_o3_trace_profile_uses_phoenix_readback_as_contract_source(
    monkeypatch, tmp_path: Path
) -> None:
    captured: dict[str, object] = {}

    def fake_readback(url, project, start_time, run_id, expected_ids):  # noqa: ANN001
        captured.update(
            url=url,
            project=project,
            start_time=start_time,
            run_id=run_id,
            expected_ids=tuple(expected_ids),
        )
        return []

    monkeypatch.setattr("eval.harness.phoenix.read_run_spans", fake_readback)
    class O3Adapter:
        def solve_instance(self, instance: EvalInstance, working_dir: str, **kwargs) -> EvalResult:
            return EvalResult(
                instance_id=instance.instance_id,
                answer="answer",
                evidence={
                    "retrieval_hits": [
                        {"rank": 1, "document_id": "doc", "chunk_id": 1, "score": 0.9}
                    ]
                },
            )

    harness = _harness(
        tmp_path,
        adapter=O3Adapter(),
        config={
            "trace_profile": "o3",
            "trace_capabilities": ("rag", "rerank"),
            "corpus_generation": "techdocs-2026-07-30-v1",
            "qrels_hash": "a" * 64,
            "index_name": "knowledge_base_v2_bge_m3",
            "phoenix_url": "http://phoenix",
            "phoenix_project": "code-agent",
            "trace_start_time": "2026-08-13T00:00:00Z",
        },
    )
    harness.run([EvalInstance(instance_id="inst-1", task_description="t")])

    assertion = _read(harness.artifacts.root, SPAN_ASSERTION)
    assert captured == {
        "url": "http://phoenix",
        "project": "code-agent",
        "start_time": "2026-08-13T00:00:00Z",
        "run_id": "run-trace",
        "expected_ids": ("inst-1",),
    }
    assert assertion["span_count"] == 0
    assert assertion["verdict"] != VERDICT_PASS


def test_o3_installs_phoenix_exporter_before_run_span(monkeypatch, tmp_path: Path) -> None:
    seen: list[str] = []

    def fake_install(self, otlp_endpoint: str = ""):  # noqa: ANN001
        seen.append(otlp_endpoint)
        return False

    monkeypatch.setattr("eval.harness.runner.TraceCapture.install", fake_install)
    harness = _harness(
        tmp_path,
        config={
            "trace_profile": "o3",
            "phoenix_otlp_endpoint": "http://127.0.0.1:6006/v1/traces",
        },
    )
    harness.run([EvalInstance(instance_id="inst-1", task_description="t")])

    assert seen == ["http://127.0.0.1:6006/v1/traces"]


def test_o3_forces_export_before_phoenix_readback(monkeypatch, tmp_path: Path) -> None:
    """The root/instance/scorer spans must leave the batch processor before readback."""
    calls: list[str] = []

    class Provider:
        def force_flush(self) -> bool:
            calls.append("flush")
            return True

    class O3Adapter:
        def solve_instance(self, instance: EvalInstance, working_dir: str, **kwargs) -> EvalResult:
            return EvalResult(
                instance_id=instance.instance_id,
                answer="answer",
                evidence={"retrieval_hits": [{"rank": 1, "document_id": "doc", "chunk_id": 1, "score": 0.9}]},
            )

    def readback(*_args, **_kwargs):
        calls.append("readback")
        return []

    monkeypatch.setattr("opentelemetry.trace.get_tracer_provider", lambda: Provider())
    monkeypatch.setattr("eval.harness.phoenix.read_run_spans", readback)
    harness = _harness(
        tmp_path,
        adapter=O3Adapter(),
        config={
            "trace_profile": "o3",
            "trace_capabilities": ("rag", "rerank"),
            "corpus_generation": "techdocs-2026-07-30-v1",
            "qrels_hash": "a" * 64,
            "index_name": "knowledge_base_v2_bge_m3",
            "phoenix_url": "http://phoenix",
            "phoenix_project": "code-agent",
            "trace_start_time": "2026-08-13T00:00:00Z",
        },
    )
    harness.run([EvalInstance(instance_id="inst-1", task_description="t")])

    assert calls[0] == "flush"
    assert calls[1:] and set(calls[1:]) == {"readback"}


def test_resume_skipped_instances_get_a_span_with_skipped_status(tmp_path: Path) -> None:
    """§20.6.4 item 2 names 'skipped' explicitly, and §20.1 rule 7 keeps
    skipped instances in the denominator."""
    checkpoint = tmp_path / "ckpt.txt"
    checkpoint.write_text("inst-1\n", encoding="utf-8")
    harness = _harness(tmp_path, checkpoint_path=checkpoint)
    harness.run([EvalInstance(instance_id="inst-1", task_description="t")])

    summary = _read(harness.artifacts.root, TRACE_SUMMARY)
    instance_spans = [s for s in summary["spans"] if s["name"] == SPAN_EVAL_INSTANCE]
    assert instance_spans
    assert instance_spans[0]["attributes"]["eval.instance_status"] == "skipped-resume"


def test_trace_capture_failure_does_not_fail_the_run(tmp_path: Path) -> None:
    """§9.3: the business path must degrade normally when telemetry is off."""
    harness = _harness(tmp_path, config={"trace_capture": False})
    outcome = harness.run([EvalInstance(instance_id="inst-1", task_description="t")])

    assert outcome["summary"]["completed"] == 1
    root = harness.artifacts.root
    # Still writes the artifact, but declares it produced nothing.
    assertion = _read(root, SPAN_ASSERTION)
    assert assertion["verdict"] != VERDICT_PASS
    assert harness.artifacts.verify_checksums() == []
