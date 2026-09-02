"""Pin the manifest pin contract, and the ways it must refuse to be gamed.

Defect 9: ``_build_manifest`` required only ``git_sha``/``model``, so
``prompt_hash``, ``qrels_hash`` and ``physical_index`` could all be empty and
the manifest was written anyway — an unpinned run was byte-indistinguishable
from a pinned one.

Two failure modes are guarded against here, not one:

* the original defect (empty required pin silently accepted), and
* the fix's own temptation — declaring "we don't do RAG" as a free pass, or
  requiring a pin that nothing can populate so no run can ever succeed.
"""

from __future__ import annotations

import ast
from pathlib import Path

import pytest

from eval.harness.pin_contract import (
    CAPABILITY_RAG,
    CLASS_CAPABILITY,
    CLASS_DEGRADABLE,
    CLASS_REQUIRED,
    STATUS_CAPABILITIES_UNDECLARED,
    STATUS_MODEL_IDENTITY_UNVERIFIED,
    evaluate_pins,
    pin_rules,
    required_pin_keys,
)

REPO_ROOT = Path(__file__).resolve().parents[2]

FULLY_PINNED = {
    "git_sha": "abc1234",
    "model": "deepseek-v4-pro",
    "prompt_hash": "f" * 64,
    "model_revision": "deepseek-v4-pro@0123456",
    "corpus_generation": "gen-7",
    "qrels_hash": "a" * 64,
    "physical_index": "knowledge_base_v2_bge_m3",
}


# ---------------------------------------------------------------------------
# the original defect
# ---------------------------------------------------------------------------


@pytest.mark.parametrize("key", ["git_sha", "model", "prompt_hash"])
def test_empty_required_pin_is_reported_missing(key: str) -> None:
    values = dict(FULLY_PINNED)
    values[key] = ""
    report = evaluate_pins(values, capabilities=[CAPABILITY_RAG])
    assert key in report["missing"]


@pytest.mark.parametrize("blank", ["", "   ", "\t", "\n"])
def test_whitespace_only_pin_is_not_a_pin(blank: str) -> None:
    """``" "`` must not satisfy a pin: it is emptiness with extra steps."""
    values = dict(FULLY_PINNED)
    values["prompt_hash"] = blank
    assert "prompt_hash" in evaluate_pins(values)["missing"]


def test_none_valued_pin_is_reported_missing() -> None:
    values = dict(FULLY_PINNED)
    values["git_sha"] = None
    assert "git_sha" in evaluate_pins(values)["missing"]


def test_absent_key_is_reported_missing() -> None:
    values = dict(FULLY_PINNED)
    del values["model"]
    assert "model" in evaluate_pins(values)["missing"]


def test_fully_pinned_rag_run_has_nothing_missing() -> None:
    report = evaluate_pins(FULLY_PINNED, capabilities=[CAPABILITY_RAG])
    assert report["missing"] == []
    assert report["contradictions"] == []


# ---------------------------------------------------------------------------
# capability gating must not become a free pass
# ---------------------------------------------------------------------------


def test_undeclared_capabilities_are_recorded_not_silently_accepted() -> None:
    """``None`` waives capability pins but must leave a visible mark.

    Unlike ``trace_contract``, over-strictness here costs a *refused run*, and a
    harness with no benchmark module (every unit test) has nothing to say about
    RAG.  So the omission is recorded as a status instead of aborting — the
    information survives, which is what stops this from being the original
    silent-empty-pin defect.
    """
    values = dict(FULLY_PINNED)
    values["qrels_hash"] = ""
    report = evaluate_pins(values, capabilities=None)
    assert report["missing"] == []
    assert STATUS_CAPABILITIES_UNDECLARED in report["statuses"], (
        "an undeclared capability set must be visible in the artifact"
    )
    assert "qrels_hash" in report["waived"]


def test_explicit_rag_declaration_still_requires_rag_pins() -> None:
    """The gate must remain real for anything that says it does RAG."""
    values = dict(FULLY_PINNED)
    values["qrels_hash"] = ""
    report = evaluate_pins(values, capabilities=[CAPABILITY_RAG])
    assert "qrels_hash" in report["missing"]
    assert STATUS_CAPABILITIES_UNDECLARED not in report["statuses"]


def test_every_benchmark_module_declares_its_capabilities() -> None:
    """Closes the loophole the undeclared path would otherwise open.

    A real benchmark reaches the pin contract through ``eval/run.py``, which
    reads ``TRACE_CAPABILITIES`` off the benchmark module.  If a benchmark
    forgot to declare, it would land on the lenient undeclared path and have
    its RAG pins waived.  This test makes that impossible by requiring the
    declaration to exist on every benchmark module.
    """
    benchmarks_dir = REPO_ROOT / "eval" / "benchmarks"
    modules = sorted(
        p for p in benchmarks_dir.glob("*.py")
        if not p.stem.startswith("_")
        and p.stem != "base"
        and not p.stem.endswith("_predictions")
    )
    assert modules, "no benchmark modules found — this test would be vacuous"
    undeclared: list[str] = []
    for path in modules:
        tree = ast.parse(path.read_text(encoding="utf-8"))
        names = {
            target.id
            for node in tree.body
            if isinstance(node, (ast.Assign, ast.AnnAssign))
            for target in (
                node.targets if isinstance(node, ast.Assign) else [node.target]
            )
            if isinstance(target, ast.Name)
        }
        if "TRACE_CAPABILITIES" not in names:
            undeclared.append(path.name)
    assert not undeclared, (
        "these benchmark modules declare no TRACE_CAPABILITIES, so their RAG "
        f"pins would be silently waived: {undeclared}"
    )


def test_non_rag_benchmark_legitimately_waives_rag_pins() -> None:
    """SWE-bench has no corpus; demanding one makes a correct manifest impossible."""
    values = {
        "git_sha": "abc1234",
        "model": "deepseek-v4-pro",
        "prompt_hash": "f" * 64,
        "model_revision": "rev-1",
        "corpus_generation": "",
        "qrels_hash": "",
        "physical_index": "",
    }
    report = evaluate_pins(values, capabilities=())
    assert report["missing"] == []
    assert set(report["waived"]) == {
        "corpus_generation",
        "qrels_hash",
        "physical_index",
    }


def test_waiver_is_recorded_so_it_can_be_audited() -> None:
    report = evaluate_pins(FULLY_PINNED, capabilities=())
    assert report["declared_capabilities"] == []
    assert report["waived"], "a waiver that is not recorded cannot be audited"


def test_populated_but_waived_pin_is_a_contradiction() -> None:
    """Declaring RAG off while pinning an index means one of the two is a lie."""
    report = evaluate_pins(FULLY_PINNED, capabilities=())
    assert report["contradictions"]
    assert any("physical_index" in c for c in report["contradictions"])


def test_unknown_capability_is_surfaced() -> None:
    report = evaluate_pins(FULLY_PINNED, capabilities=["rag", "teleportation"])
    assert report["unknown_capabilities"] == ["teleportation"]


# ---------------------------------------------------------------------------
# degradable provider-identity case
# ---------------------------------------------------------------------------


def test_absent_model_revision_yields_a_status_not_a_failure() -> None:
    """§20.6.3 E2 mandates MODEL_IDENTITY_UNVERIFIED, not an aborted run."""
    values = dict(FULLY_PINNED)
    values["model_revision"] = ""
    report = evaluate_pins(values, capabilities=[CAPABILITY_RAG])
    assert "model_revision" not in report["missing"]
    assert STATUS_MODEL_IDENTITY_UNVERIFIED in report["statuses"]


def test_present_model_revision_yields_no_unverified_status() -> None:
    report = evaluate_pins(FULLY_PINNED, capabilities=[CAPABILITY_RAG])
    assert STATUS_MODEL_IDENTITY_UNVERIFIED not in report["statuses"]


# ---------------------------------------------------------------------------
# the contract must stay reachable and honest
# ---------------------------------------------------------------------------


def test_required_keys_shrink_when_capabilities_are_off() -> None:
    with_rag = set(required_pin_keys([CAPABILITY_RAG]))
    without = set(required_pin_keys(()))
    assert without < with_rag, "capability gating must actually gate something"


def test_every_rule_has_a_recorded_reason() -> None:
    """A pin nobody can justify is a pin nobody will maintain."""
    for rule in pin_rules():
        assert rule.why.strip(), f"{rule.key} has no recorded justification"


def test_every_rule_class_is_known() -> None:
    for rule in pin_rules():
        assert rule.pin_class in {CLASS_REQUIRED, CLASS_CAPABILITY, CLASS_DEGRADABLE}
        if rule.pin_class == CLASS_CAPABILITY:
            assert rule.capability, f"{rule.key} is gated on nothing"
        if rule.pin_class == CLASS_DEGRADABLE:
            assert rule.empty_status, (
                f"{rule.key} may be empty but names no status; an empty pin "
                "with no status is exactly the defect being fixed"
            )


def test_prompt_hash_is_actually_populated_by_the_run_path() -> None:
    """Guards against making a pin required that nothing can supply.

    ``prompt_hash`` was unpopulated before this work.  Requiring it without
    wiring it would abort every run — the unreachable-gate defect.
    """
    from eval.run import _system_prompt_hash

    digest = _system_prompt_hash()
    assert len(digest) == 64, "the run path must produce a real prompt hash"
    assert digest == _system_prompt_hash(), "the pin must be deterministic"


def test_runner_passes_the_benchmark_declaration_not_a_hardcoded_tuple() -> None:
    """AST assertion: ``_build_manifest`` must read capabilities from config.

    A hardcoded ``capabilities=()`` would satisfy every behavioural test above
    while silently waiving RAG pins for RAG benchmarks, so the wiring is
    asserted structurally rather than through behaviour.
    """
    source = (REPO_ROOT / "eval" / "harness" / "runner.py").read_text(encoding="utf-8")
    tree = ast.parse(source)
    target = next(
        node
        for node in ast.walk(tree)
        if isinstance(node, ast.FunctionDef) and node.name == "_build_manifest"
    )
    calls = [
        node
        for node in ast.walk(target)
        if isinstance(node, ast.Call)
        and isinstance(node.func, ast.Name)
        and node.func.id == "evaluate_pins"
    ]
    assert calls, "_build_manifest must call evaluate_pins"
    for call in calls:
        kwarg = next((k for k in call.keywords if k.arg == "capabilities"), None)
        assert kwarg is not None, "capabilities must be passed explicitly"
        assert not isinstance(kwarg.value, (ast.Tuple, ast.List, ast.Constant)), (
            "capabilities must come from config, not a literal in runner.py"
        )
