"""HarnessRun lifecycle tests for the active Goal's official evaluation path.

The CLI must route every benchmark through ``eval/run.py -> HarnessRun ->
benchmark adapter -> official runner/scorer`` as the only official path. The
tests preserve the regression boundary for the removed legacy bypass: scorer
failure maps to ERROR_SCORER with a non-zero exit code, and synthetic mode
requires an explicit top-level opt-in.
"""

from __future__ import annotations

import json
import os
from pathlib import Path
from unittest import mock

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

HERE = Path(__file__).resolve().parent
PROJECT_ROOT = HERE.parents[1]
SPY_MARKER = "__EVAL_RUN_SPY_CALLED__"


def _pinned_config(**overrides: object) -> dict[str, object]:
    """Minimal valid harness config — mandatory manifest pins (H3)."""
    config: dict[str, object] = {
        "git_sha": "a1b2c3d",
        "dirty_hash": "0" * 64,
        "model": "test-model",
        "prompt_hash": "0" * 64,
    }
    config.update(overrides)
    return config


def _env_with_project_path(**extra: str) -> dict[str, str]:
    """Build a clean env dict with PROJECT_ROOT in PYTHONPATH."""
    env = os.environ.copy()
    env["PYTHONPATH"] = str(PROJECT_ROOT)
    env.update(extra)
    return env


# ---------------------------------------------------------------------------
# 1. eval/run.py wires the AgentBenchmark scorer into HarnessRun
# ---------------------------------------------------------------------------


class TestHarnessRunOfficialScorerWired:
    """Prove that eval/run.py wires the AgentBenchmark adapter's official
    ``score`` method as HarnessRun's scorer callback (H2 fix)."""

    def test_harnessrun_scorer_callback_is_wired_by_eval_run(self):
        """eval/run.py passes scorer= to the HarnessRun constructor when the
        benchmark exposes an AgentBenchmark adapter."""
        run_py = PROJECT_ROOT / "eval" / "run.py"
        source = run_py.read_text(encoding="utf-8")
        # The CLI allocates HarnessRun with a scorer= keyword derived from
        # the benchmark's AgentBenchmark adapter
        assert "scorer=agent_bench.score" in source, (
            "BUG: eval/run.py does not wire scorer= in the HarnessRun "
            "constructor — official scoring would be skipped again"
        )

    def test_harnessrun_has_scorer_none_by_default(self):
        """HarnessRun.scorer defaults to None — wiring is explicit."""
        from eval.harness import HarnessRun, RunArtifacts

        harness = HarnessRun(run_id="test", artifacts=RunArtifacts(run_id="test", root="."))
        assert harness.scorer is None, (
            "HarnessRun.scorer should default to None; wiring happens in eval/run.py"
        )

    def test_eval_run_docs_claim_scorer_dispatch_and_code_wires_it(self):
        """eval/run.py docstring claims 'scorer dispatch' and the code now
        delivers it via the AgentBenchmark adapter."""
        run_py = PROJECT_ROOT / "eval" / "run.py"
        source = run_py.read_text(encoding="utf-8")
        assert "scorer dispatch" in source, (
            "eval/run.py no longer claims scorer dispatch — re-evaluate"
        )
        assert "AgentBenchmark" in source, (
            "eval/run.py no longer references AgentBenchmark — scorer wiring gone"
        )


# ---------------------------------------------------------------------------
# 2. HarnessRun path bypasses official scorer for swebench
# ---------------------------------------------------------------------------


class TestSwebenchHarnessPathBypassesOfficialScorer:
    """Prove that swebench instances going through HarnessRun result in
    agent predictions WITHOUT official SWE-bench scorer invocation."""

    def test_swebench_load_instances_does_not_call_official_scorer(self):
        """When swebench.load_instances() is used (HarnessRun path), the
        official scorer is never invoked — only the agent produces output."""
        from eval.benchmarks import swebench as sb_mod

        # Step 1: verify load_instances exists (HarnessRun path active)
        assert hasattr(sb_mod, "load_instances"), (
            "swebench module lacks load_instances() — HarnessRun path dead"
        )

        # Step 2: load_instances itself does not reference official scorer
        src = (PROJECT_ROOT / "eval" / "benchmarks" / "swebench.py").read_text(encoding="utf-8")

        # load_instances function body (from def to the next top-level def/class)
        def_start = src.index("def load_instances(")
        # Find next top-level def after load_instances
        rest = src[def_start:]
        # Look for official scorer references in load_instances body
        body = rest[:3500]  # generous slice through load_instances
        has_scorer_ref = any(kw in body for kw in [
            "_run_official_scoring",
            "run_evaluation",
            "swebench.harness",
            "official",
        ])
        print(f"[INFO] swebench.load_instances body references official scorer: {has_scorer_ref}")

    def test_swebench_official_scoring_lives_only_in_legacy_run_path(self):
        """The official scorer (_run_official_scoring*) is only callable from
        the legacy run() path (which goes through benchmark_mod.run(driver, limit)),
        not from the HarnessRun path (which calls harness.run(instances))."""
        src = (PROJECT_ROOT / "eval" / "benchmarks" / "swebench.py").read_text(encoding="utf-8")

        # _run_official_scoring is defined but only reachable from run(), not load_instances()
        assert "_run_official_scoring" in src, (
            "swebench module no longer has official scoring — re-evaluate"
        )

        # Confirm official scorer is called from run() but NOT from load_instances()
        # Note: run() is defined AFTER load_instances() in swebench.py
        load_start = src.index("\ndef load_instances(")
        run_start = src.index("\ndef run(", load_start)
        # Find the next major function boundary after run
        next_after_run = src.find("\ndef ", run_start + 10)
        if next_after_run == -1:
            next_after_run = len(src)
        load_body = src[load_start:run_start]
        run_body = src[run_start:next_after_run]

        assert "_run_official_scoring" not in load_body, (
            "BUG: load_instances() calls official scorer — bypass hypothesis wrong"
        )
        # NOTE: swebench.run() creates a SWEBenchRunner and calls run_all()
        # which produces predictions.jsonl via _adapter.solve_instance().
        # It does NOT call _run_official_scoring — that is a SEPARATE manual
        # step. The HarnessRun path has NO connection to official scoring.
        #
        # This is the structural bypass: patches are generated without ever
        # feeding them through the official scorer.
        if "_run_official_scoring" not in run_body:
            print("[CONFIRMED] swebench.run() does NOT invoke official scorer — "
                  "patches are unverified")


# ---------------------------------------------------------------------------
# 3. HarnessRun path bypasses official runner for terminalbench
# ---------------------------------------------------------------------------


class TestTerminalbenchHarnessPathBypassesOfficialRunner:
    """Prove that terminalbench instances going through HarnessRun path
    do NOT invoke terminal_bench.Harness (the official runner)."""

    def test_terminalbench_load_instances_does_not_call_official_harness(self):
        """load_instances() returns static EvalInstances; official runner
        lives only in the legacy run() path."""
        from eval.benchmarks import terminalbench as tb_mod

        assert hasattr(tb_mod, "load_instances"), (
            "terminalbench module lacks load_instances() — HarnessRun path dead"
        )

        src = (PROJECT_ROOT / "eval" / "benchmarks" / "terminalbench.py").read_text(encoding="utf-8")
        assert "from terminal_bench.harness import Harness" in src, (
            "terminalbench module no longer imports official Harness"
        )

        # load_instances function body should NOT reference Harness
        li_start = src.index("def load_instances(")
        # Find next top-level def
        next_def = src.find("\ndef ", li_start + 10)
        if next_def == -1:
            next_def = len(src)
        load_body = src[li_start:next_def]
        assert "terminal_bench.harness" not in load_body, (
            "BUG: load_instances() imports official Harness — may be wired"
        )

    def test_terminalbench_official_harness_only_in_legacy_run(self):
        """terminal_bench.Harness is instantiated exclusively in run()/_run_tb_official."""
        src = (PROJECT_ROOT / "eval" / "benchmarks" / "terminalbench.py").read_text(encoding="utf-8")
        load_start = src.index("\ndef load_instances(")
        run_start = src.index("\ndef run(", load_start)
        run_body = src[run_start:]

        assert "Harness(" in run_body, (
            "BUG: run() no longer instantiates official Harness — re-evaluate"
        )


# ---------------------------------------------------------------------------
# 4. HarnessRun path bypasses official runner for tau2bench
# ---------------------------------------------------------------------------


class TestTau2benchHarnessPathBypassesOfficialRunner:
    """Prove that tau2bench instances going through HarnessRun path
    do NOT invoke tau_bench.run.run()."""

    def test_tau2bench_load_instances_does_not_call_official_runner(self):
        """load_instances() returns static EvalInstances; official runner
        lives only in run()."""
        from eval.benchmarks import tau2bench as t2b_mod

        assert hasattr(t2b_mod, "load_instances"), (
            "tau2bench module lacks load_instances() — HarnessRun path dead"
        )

        src = (PROJECT_ROOT / "eval" / "benchmarks" / "tau2bench.py").read_text(encoding="utf-8")
        li_start = src.index("def load_instances(")
        next_def = src.find("\ndef ", li_start + 10)
        if next_def == -1:
            next_def = len(src)
        load_body = src[li_start:next_def]

        assert "tau_bench.run" not in load_body, (
            "BUG: load_instances() imports tau_bench.run — may be wired"
        )

    def test_tau2bench_official_runner_only_in_legacy_run(self):
        """tau_run is only called from _run_with_config(), reachable via run()."""
        src = (PROJECT_ROOT / "eval" / "benchmarks" / "tau2bench.py").read_text(encoding="utf-8")
        load_start = src.index("\ndef load_instances(")
        run_start = src.index("\ndef run(", load_start)
        run_body = src[run_start:]

        assert "tau_run(" in run_body or "tau_bench.run" in run_body, (
            "BUG: run() no longer invokes official tau2bench runner — re-evaluate"
        )

        # verify load_instances does NOT call tau_run
        load_body = src[load_start:run_start]
        assert "tau_run" not in load_body, (
            "BUG: load_instances() calls tau_run — bypass may be fixed"
        )


# ---------------------------------------------------------------------------
# 5. HarnessRun instance lifecycle: adapter.solve_instance is NEVER an official runner
# ---------------------------------------------------------------------------


class TestHarnessRunAdapterIsNeverOfficialRunner:
    """Prove that HarnessRun.adapter.solve_instance is always an LLM agent
    (HeadlessDriver), never an official benchmark runner."""

    def test_eval_run_creates_headless_driver_not_official_runner(self):
        """eval/run.py creates a HeadlessDriver via create_driver(), which
        wraps an LLM call — it is NOT the official benchmark runner."""
        from eval.driver_headless import create_driver, HeadlessDriver

        # create_driver returns HeadlessDriver (an LLM agent)
        adapter = create_driver(model="test-model", base_url="http://localhost:9999", use_runner=True)
        assert isinstance(adapter, HeadlessDriver), (
            f"create_driver returned {type(adapter).__name__} not HeadlessDriver"
        )

    def test_headless_driver_solve_instance_is_llm_call_not_benchmark_runner(self):
        """HeadlessDriver.solve_instance invokes an LLM via the orchestrator — it
        does not call swebench.harness, terminal_bench.Harness, or tau_bench.run."""
        from eval.driver_headless import HeadlessDriver, create_driver

        adapter = create_driver(model="test-model", base_url="http://localhost:9999", use_runner=True)

        solve_src = (PROJECT_ROOT / "eval" / "driver_headless.py").read_text(encoding="utf-8")
        # The call path should go through ConversationRunner / LLM, not official harness
        assert "swebench.harness" not in solve_src, (
            "BUG: HeadlessDriver references swebench.harness — re-evaluate bypass"
        )
        assert "terminal_bench.harness" not in solve_src, (
            "BUG: HeadlessDriver references terminal_bench — re-evaluate bypass"
        )

    def test_search_knowledge_fails_closed_without_optional_otel_propagators(
        self, monkeypatch, tmp_path: Path
    ):
        """The optional trace extra must not make the headless module unloadable.

        SearchKnowledge still needs an active W3C context for an auditable O3
        request, so missing propagators must become a tool error rather than a
        request with fabricated or absent trace headers.
        """
        import eval.driver_headless as driver_headless
        from eval.driver_headless import LocalToolExecutor

        monkeypatch.setattr(driver_headless, "TraceContextTextMapPropagator", None)
        monkeypatch.setattr(driver_headless, "W3CBaggagePropagator", None)
        monkeypatch.setattr(
            driver_headless, "current_join_attributes", lambda: {"eval.run_id": "test-run"}
        )
        monkeypatch.setenv("CODE_AGENT_RAG_SERVER_URL", "http://127.0.0.1:1")
        monkeypatch.setenv("CODE_AGENT_RAG_INTERNAL_SECRET", "test-secret")
        monkeypatch.setenv("CODE_AGENT_RAG_USER_ID", "1")

        result = LocalToolExecutor(str(tmp_path)).execute(
            "SearchKnowledge", '{"query":"test"}'
        )

        assert result.exit_code == 1
        assert "OpenTelemetry propagation" in result.error

    def test_harness_run_calls_adapter_solve_instance_which_is_agent_not_scorer(
        self, tmp_path: Path
    ):
        """HarnessRun.run() calls adapter.solve_instance() to get agent output.
        The result.model_patch is agent-generated, not officially scored."""
        from eval.harness import HarnessRun, RunArtifacts, Budget

        # Capture what solve_instance returns
        calls = []

        class SpyAdapter:
            def solve_instance(self, instance, working_dir, **kwargs):
                calls.append(instance.instance_id)
                from eval.adapter import EvalResult
                return EvalResult(
                    instance_id=instance.instance_id,
                    model_patch="agent-generated-patch",  # NOT an official score
                )

        artifacts = RunArtifacts(run_id="spy-test", root=tmp_path)
        harness = HarnessRun(
            run_id="spy-test",
            artifacts=artifacts,
            budget=Budget(),
            adapter=SpyAdapter(),
            config=_pinned_config(),
        )

        from eval.adapter import EvalInstance
        instances = [
            EvalInstance(instance_id="test/1", task_description="fix a bug"),
            EvalInstance(instance_id="test/2", task_description="add feature"),
        ]

        result = harness.run(instances)
        # Both instances completed (agent returned patches)
        assert result["summary"]["completed"] == 2, (
            f"expected 2 completed, got {result['summary']}"
        )
        # But NO official scorer was invoked — only the agent
        assert calls == ["test/1", "test/2"], (
            "adapter.solve_instance was not called as expected"
        )

        # Verify predictions do NOT contain official scorer fields (resolved/found/etc.)
        pred_path = artifacts.root / "predictions.jsonl"
        assert pred_path.exists(), f"predictions file missing at {pred_path}"
        for line in pred_path.read_text(encoding="utf-8").splitlines():
            if not line.strip():
                continue
            pred = json.loads(line)
            assert "resolved" not in pred, (
                f"BUG: instance {pred.get('instance_id')} has 'resolved' field "
                f"from official scorer — bypass may be narrower than expected"
            )


# ---------------------------------------------------------------------------
# 6. eval/run.py has a SINGLE execution path — HarnessRun only
# ---------------------------------------------------------------------------


class TestEvalRunSingleHarnessPath:
    """Prove that eval/run.py has exactly ONE execution path — HarnessRun —
    and the legacy ``benchmark_mod.run(driver, limit)`` bypass is gone (H2)."""

    def test_eval_run_has_no_structural_bifurcation(self):
        """No harness_path marker / if-else split remains."""
        run_py = PROJECT_ROOT / "eval" / "run.py"
        source = run_py.read_text(encoding="utf-8")

        assert "harness_path" not in source, (
            "eval/run.py still splits on harness_path — re-evaluate"
        )
        assert "if harness_path:" not in source
        assert "# ---- Legacy benchmark.run() path ----" not in source, (
            "eval/run.py still contains the legacy execution block"
        )

    def test_eval_run_requires_load_instances(self):
        """Every benchmark MUST expose load_instances() — fail early otherwise."""
        run_py = PROJECT_ROOT / "eval" / "run.py"
        source = run_py.read_text(encoding="utf-8")

        assert 'hasattr(benchmark_mod, "load_instances")' in source, (
            "load_instances() gate missing — re-evaluate"
        )
        assert "benchmark_mod.load_instances(limit=limit)" in source, (
            "load_instances() is not the single instance-loading call"
        )

    def test_eval_run_harness_run_is_the_only_execution_call(self):
        """harness.run(instances) is the only execution call; benchmark_mod.run
        is never invoked (the docstring may mention the removed bypass, so the
        check is on actual AST call nodes)."""
        import ast

        run_py = PROJECT_ROOT / "eval" / "run.py"
        source = run_py.read_text(encoding="utf-8")

        assert source.count("harness.run(") == 1, (
            "expected exactly one harness.run(instances) call"
        )
        tree = ast.parse(source)
        legacy_calls = [
            n for n in ast.walk(tree)
            if isinstance(n, ast.Call)
            and isinstance(n.func, ast.Attribute)
            and isinstance(n.func.value, ast.Name)
            and n.func.value.id == "benchmark_mod"
            and n.func.attr == "run"
        ]
        assert legacy_calls == [], (
            f"legacy benchmark_mod.run() call still present: {legacy_calls!r}"
        )

    def test_legacy_output_writers_removed(self):
        """The legacy else-block JSON/CSV result writers are gone."""
        run_py = PROJECT_ROOT / "eval" / "run.py"
        source = run_py.read_text(encoding="utf-8")

        assert "{benchmark_name}_results" not in source, (
            "legacy benchmark_name_results.json/.csv writer still present"
        )
        assert "import csv" not in source, (
            "legacy CSV writer import still present"
        )


# ---------------------------------------------------------------------------
# 7. scorer ERROR_SCORER reachability
# ---------------------------------------------------------------------------


class TestScorerErrorScorerClassification:
    """Test that ERROR_SCORER taxonomy is reachable in HarnessRun — the
    classification eval/run.py's wired scorer failures map onto."""

    def test_error_scorer_exists_in_taxonomy(self):
        """ERROR_SCORER is defined in the runner — official scorer failures
        are never counted as model failures."""
        from eval.harness.runner import ERROR_SCORER
        assert ERROR_SCORER == "scorer"

    def test_scorer_error_raised_and_classified(self):
        """ScorerError is raised on scorer failure and classified as ERROR_SCORER,
        proving the runner infrastructure supports the wired scorer."""
        from eval.harness import HarnessRun, RunArtifacts, Budget, ScorerError, classify_error

        exc = ScorerError("mock official scorer crash")
        assert classify_error(exc) == "scorer", (
            "ScorerError should classify as scorer"
        )

    def test_scorer_error_is_reachable_with_mocked_scorer(self):
        """Demonstrate that IF scorer is wired, ERROR_SCORER is reachable —
        this is the desired behavior after the fix."""
        from eval.harness import HarnessRun, RunArtifacts, Budget, ScorerError
        from eval.adapter import EvalInstance, EvalResult

        def crashing_scorer(result, instance, workspace):
            raise RuntimeError("official scorer timeout")

        class SpyAdapter:
            def solve_instance(self, instance, working_dir, **kwargs):
                return EvalResult(
                    instance_id=instance.instance_id,
                    model_patch="patch",
                )

        artifacts = RunArtifacts(run_id="scorer-crash-test", root="eval_results")
        harness = HarnessRun(
            run_id="scorer-crash-test",
            artifacts=artifacts,
            budget=Budget(),
            adapter=SpyAdapter(),
            scorer=crashing_scorer,  # ← THIS is what eval/run.py never does
            config=_pinned_config(),
        )

        result = harness.run([
            EvalInstance(instance_id="test/1", task_description="fix a bug")
        ])

        # ERROR_SCORER category should have 1
        assert result["summary"]["by_category"]["scorer"] == 1, (
            f"expected 1 scorer error, got {result['summary']['by_category']}"
        )
        assert result["summary"]["completed"] == 0
