"""The trace contract's PASS must be reachable for the benchmarks we run.

§20.6.4 lists ``rag.retrieve`` / ``embedding`` among the required span kinds, but
solving a SWE-bench instance means reading and patching an already-checked-out
repository: no retrieval happens anywhere in that path.  Requiring those kinds of
every benchmark makes ``PASS`` unreachable for SWE-bench, and an unreachable
``PASS`` is the mirror image of the defect fixed in §31 item 4 -- a preflight that
cannot fail.  Both produce a verdict that carries no information: if the answer is
always the same, observing it tells you nothing about the run.

So required kinds are gated on capabilities the run actually exercises.  The
danger in that fix is obvious and is what most of this file tests: a capability
declaration must not become a free pass.  Three properties keep it honest.

1. Silence is strict.  ``capabilities=None`` (nothing declared) keeps every kind
   required, so forgetting to declare can only tighten the contract, never
   loosen it.
2. The declaration is auditable.  Whatever was declared, and which required
   kinds it waived, are written into the artifact, so a reader re-derives the
   verdict without trusting the reporter.
3. Contradictions are reported.  A run that declares RAG off and then emits RAG
   spans is describing itself incorrectly; that is a FAIL, not a shrug.

The declaration also lives in the benchmark module (``TRACE_CAPABILITIES``), next
to the code that either retrieves or does not -- not in whoever reports results.
"""

from __future__ import annotations

import ast
from pathlib import Path

import pytest

from eval.harness.trace_contract import (
    ALL_CAPABILITIES,
    CAPABILITY_RAG,
    CAPABILITY_RERANK,
    CapturedSpan,
    REQUIRED_SPAN_KINDS,
    evaluate_trace_contract,
    required_span_kinds,
    span_kinds,
)

VERDICT_PASS = "PASS"
VERDICT_FAIL = "FAIL"
VERDICT_INCOMPLETE = "INCOMPLETE"

RAG_KINDS = {"rag.retrieve", "embedding", "rerank"}
RUN_ID = "run-cap-1"


def _capture(
    *,
    include_rag: bool = False,
    extra: CapturedSpan | None = None,
) -> list[CapturedSpan]:
    """Build a capture containing every kind a run of this shape can emit."""
    spans: list[CapturedSpan] = []
    for kind in span_kinds():
        if not include_rag and kind.name in RAG_KINDS:
            continue
        attributes: dict[str, object] = {
            "eval.run_id": RUN_ID,
            "eval.instance_id": "astropy__astropy-12907",
        }
        for name in kind.required_attributes:
            attributes[name] = "pinned"
        spans.append(
            CapturedSpan(
                name=kind.name,
                trace_id="t0",
                span_id=f"s-{kind.name}",
                attributes=attributes,
                status="OK",
            )
        )
    if extra is not None:
        spans.append(extra)
    return spans


# ---------------------------------------------------------------------------
# The defect this fix addresses: PASS was unreachable for a non-RAG benchmark
# ---------------------------------------------------------------------------


def test_pass_is_reachable_for_a_benchmark_that_does_no_retrieval() -> None:
    report = evaluate_trace_contract(_capture(), run_id=RUN_ID, capabilities=())
    assert report["verdict"] == VERDICT_PASS, report["problems"]


def test_without_the_fix_shape_the_same_capture_is_incomplete() -> None:
    """Pins the defect: demanding RAG of a non-RAG run yields INCOMPLETE forever.

    This is the behaviour that made the trace verdict uninformative for
    SWE-bench, and it is still what happens when the run declares RAG on.
    """
    report = evaluate_trace_contract(
        _capture(), run_id=RUN_ID, capabilities=(CAPABILITY_RAG,)
    )
    assert report["verdict"] == VERDICT_INCOMPLETE
    assert set(report["missing_required_kinds"]) == {"rag.retrieve", "embedding"}


# ---------------------------------------------------------------------------
# Property 1: silence is strict
# ---------------------------------------------------------------------------


def test_declaring_nothing_keeps_every_kind_required() -> None:
    assert required_span_kinds(None) == REQUIRED_SPAN_KINDS


def test_declaring_nothing_does_not_waive_rag_for_a_non_rag_capture() -> None:
    report = evaluate_trace_contract(_capture(), run_id=RUN_ID)
    assert report["verdict"] == VERDICT_INCOMPLETE
    assert "rag.retrieve" in report["missing_required_kinds"]
    assert report["waived_required_kinds"] == []


def test_capability_declaration_can_only_shrink_the_required_set() -> None:
    """No declaration may add a requirement that full capabilities lacks.

    Otherwise a caller could invent a capability to make some other run look
    incomplete by comparison.
    """
    full = set(required_span_kinds(ALL_CAPABILITIES))
    for declared in ((), (CAPABILITY_RAG,), (CAPABILITY_RERANK,), ALL_CAPABILITIES):
        assert set(required_span_kinds(declared)) <= full


# ---------------------------------------------------------------------------
# Property 2: the declaration is auditable from the artifact alone
# ---------------------------------------------------------------------------


def test_artifact_records_what_was_declared_and_what_it_waived() -> None:
    report = evaluate_trace_contract(_capture(), run_id=RUN_ID, capabilities=())
    assert report["declared_capabilities"] == []
    assert set(report["waived_required_kinds"]) == {"rag.retrieve", "embedding"}


def test_verdict_is_rederivable_from_the_recorded_fields() -> None:
    """A reader must not have to trust the verdict; the fields must imply it."""
    report = evaluate_trace_contract(_capture(), run_id=RUN_ID, capabilities=())
    required = set(required_span_kinds(report["declared_capabilities"]))
    present = set(report["present_kinds"])
    assert required - present == set(report["missing_required_kinds"])
    assert not report["problems"]
    assert report["verdict"] == VERDICT_PASS


# ---------------------------------------------------------------------------
# Property 3: contradictions and nonsense are reported, not accepted
# ---------------------------------------------------------------------------


def test_declaring_rag_off_while_emitting_rag_spans_fails() -> None:
    sneaked = CapturedSpan(
        name="rag.retrieve",
        trace_id="t0",
        span_id="s-sneak",
        attributes={"eval.run_id": RUN_ID, "eval.instance_id": "i1"},
        status="OK",
    )
    report = evaluate_trace_contract(
        _capture(extra=sneaked), run_id=RUN_ID, capabilities=()
    )
    assert report["verdict"] == VERDICT_FAIL
    assert any("does not match the run" in p for p in report["problems"])


def test_unknown_capability_is_reported_rather_than_ignored() -> None:
    report = evaluate_trace_contract(
        _capture(), run_id=RUN_ID, capabilities=("rag", "teleportation")
    )
    assert report["verdict"] == VERDICT_FAIL
    assert any("unknown capabilities" in p for p in report["problems"])


@pytest.mark.parametrize("capabilities", [None, (), ALL_CAPABILITIES])
def test_an_empty_capture_never_passes_whatever_is_declared(capabilities) -> None:
    """The central anti-cheating property: no declaration buys a PASS.

    If declaring "this run does nothing" could satisfy the contract, the trace
    requirement would be satisfiable by running nothing at all.
    """
    report = evaluate_trace_contract([], run_id=RUN_ID, capabilities=capabilities)
    assert report["verdict"] != VERDICT_PASS
    assert report["span_count"] == 0


def test_waiving_rag_does_not_waive_the_scorer_or_agent_kinds() -> None:
    """Waivers stay scoped: only capability-gated kinds may be waived."""
    waivable = {k.name for k in span_kinds() if k.capability}
    report = evaluate_trace_contract(_capture(), run_id=RUN_ID, capabilities=())
    assert set(report["waived_required_kinds"]) <= waivable
    for essential in ("eval.run", "invoke_agent", "chat", "scorer.official"):
        assert essential not in report["waived_required_kinds"]


def test_secret_detection_still_applies_to_a_waived_run() -> None:
    """Loosening required kinds must not loosen anything else (§9.2)."""
    leaky = _capture()
    leaky[0].attributes["some.attr"] = "sk-" + ("dead" * 8)
    report = evaluate_trace_contract(leaky, run_id=RUN_ID, capabilities=())
    assert report["verdict"] == VERDICT_FAIL
    assert any("secret" in p.lower() for p in report["problems"])


# ---------------------------------------------------------------------------
# The declaration lives with the benchmark, not with the reporter
# ---------------------------------------------------------------------------


def test_swebench_declares_no_retrieval_capability() -> None:
    from eval.benchmarks import swebench

    assert tuple(swebench.TRACE_CAPABILITIES) == ()


def test_run_py_reads_the_declaration_from_the_benchmark_module() -> None:
    """AST-asserted: the wiring must read the benchmark's own declaration.

    A hardcoded ``capabilities=()`` in run.py would pass every behavioural test
    here while silently waiving RAG spans for the RAG benchmarks too -- the
    §24.2 precedent for preferring a static assertion over a behavioural one.
    """
    source = (Path(__file__).resolve().parents[2] / "eval" / "run.py").read_text(
        encoding="utf-8"
    )
    tree = ast.parse(source)
    found = False
    for node in ast.walk(tree):
        if not isinstance(node, ast.Call):
            continue
        if not (isinstance(node.func, ast.Name) and node.func.id == "getattr"):
            continue
        if len(node.args) < 2:
            continue
        target, attribute = node.args[0], node.args[1]
        if (
            isinstance(target, ast.Name)
            and target.id == "benchmark_mod"
            and isinstance(attribute, ast.Constant)
            and attribute.value == "TRACE_CAPABILITIES"
        ):
            found = True
    assert found, "run.py must read TRACE_CAPABILITIES off the benchmark module"


def test_runner_passes_capabilities_into_the_contract() -> None:
    """AST-asserted: the runner must forward the declaration, not drop it."""
    source = (
        Path(__file__).resolve().parents[2] / "eval" / "harness" / "runner.py"
    ).read_text(encoding="utf-8")
    tree = ast.parse(source)
    for node in ast.walk(tree):
        if (
            isinstance(node, ast.Call)
            and isinstance(node.func, ast.Name)
            and node.func.id == "evaluate_trace_contract"
        ):
            keywords = {kw.arg for kw in node.keywords}
            assert "capabilities" in keywords, (
                "runner must forward capabilities to evaluate_trace_contract"
            )
            return
    pytest.fail("no call to evaluate_trace_contract found in runner.py")
