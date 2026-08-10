"""Phase 1 gate compliance — verbatim checks against DESIGN-MAP §20.6.1 / §20.7.

These tests encode the design map's literal gate wording, not a paraphrase:

- H0: "每个 adapter 声明 official package、dataset revision、image digest、
  prediction schema 与 scorer parser，缺 pin 时启动前失败"
  -> eval/run.py MUST validate pins and refuse to launch when one is missing.

- H3: "启动前验证 Git/dirty、dataset、model response identity、prompt、seed、
  budget、network、package/image、qrels、physical index/mapping pins；
  结束后对 artifact tree 生成并校验 checksums.sha256"
  -> checksums must cover the WHOLE artifact tree (§20.7 puts official-output
  under scorer/ and trace-summary under traces/), and there must be a
  verification path, not only generation.

- H4: "测试并实现 pre-start/runtime budget、timeout/cancel、max_processes、
  网络 allowlist、容器隔离及失败 workspace 保留；标记文件不能代替隔离"
  -> max_processes must be reachable from a real HarnessRun, not dead code.
"""

from __future__ import annotations

import ast
import hashlib
import json
from pathlib import Path

import pytest

from eval.adapter import EvalInstance, EvalResult
from eval.harness.artifacts import RunArtifacts
from eval.harness.budget import Budget, BudgetExceeded, BudgetUsage, check_budget
from eval.harness.runner import HarnessRun

PROJECT_ROOT = Path(__file__).resolve().parents[2]


def _pinned_config(**overrides: object) -> dict[str, object]:
    config: dict[str, object] = {
        "git_sha": "a1b2c3d",
        "dirty_hash": "0" * 64,
        "model": "test-model",
        "prompt_hash": "0" * 64,
    }
    config.update(overrides)
    return config


class FakeAdapter:
    def solve_instance(self, instance: EvalInstance, working_dir: str, **kwargs) -> EvalResult:
        return EvalResult(instance_id=instance.instance_id, answer="ok")


# ---------------------------------------------------------------------------
# H0 gate — pins validated before launch
# ---------------------------------------------------------------------------


class TestH0PinPreflight:
    def test_eval_run_calls_validate_pins_before_launch(self):
        """eval/run.py must call validate_pins() on the AgentBenchmark.

        Design map H0: "缺 pin 时启动前失败".  Constructing an adapter that
        reports missing pins and running anyway produces artifacts that claim
        reproducibility they do not have.
        """
        source = (PROJECT_ROOT / "eval" / "run.py").read_text(encoding="utf-8")
        tree = ast.parse(source)

        called = {
            node.func.attr
            for node in ast.walk(tree)
            if isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute)
        }
        assert "validate_pins" in called, (
            "eval/run.py never calls validate_pins() — a benchmark with an "
            "incomplete pin set can still start a run (H0 gate violation)"
        )

    def test_preflight_helper_exists_and_fails_closed(self):
        """A named preflight must exist and return the missing pin names."""
        from eval.run import _validate_benchmark_pins

        class Incomplete:
            name = "incomplete"

            @property
            def pins(self) -> dict[str, str]:
                return {
                    "benchmark": "incomplete",
                    "dataset_name": "",
                    "dataset_revision": "",
                    "scorer_name": "x",
                }

            def validate_pins(self) -> list[str]:
                return [k for k in ("dataset_name", "dataset_revision") if not self.pins[k]]

        issues = _validate_benchmark_pins(Incomplete())
        assert issues, "preflight accepted a benchmark with empty pins"
        assert "dataset_revision" in " ".join(issues)

    def test_preflight_passes_for_complete_pins(self):
        from eval.run import _validate_benchmark_pins

        class Complete:
            name = "complete"

            @property
            def pins(self) -> dict[str, str]:
                return {
                    "benchmark": "complete",
                    "dataset_name": "ds",
                    "dataset_revision": "abc1234",
                    "scorer_name": "scorer",
                }

            def validate_pins(self) -> list[str]:
                return []

        assert _validate_benchmark_pins(Complete()) == []


# ---------------------------------------------------------------------------
# H3 gate — checksums cover the whole tree and are verifiable
# ---------------------------------------------------------------------------


class TestH3ChecksumTreeCoverage:
    def test_checksums_cover_subdirectories(self, tmp_path: Path) -> None:
        """§20.7 puts official-output under scorer/ and trace files under traces/.

        A checksums file that silently skips subdirectories does not pin the
        artifact tree — the official scorer output could change with no
        checksum mismatch.
        """
        artifacts = RunArtifacts("run-tree", tmp_path)
        artifacts.record_prediction({"instance_id": "i1", "answer": "x"})

        (artifacts.root / "scorer").mkdir()
        (artifacts.root / "scorer" / "official-output.json").write_text(
            json.dumps({"resolved": True}), encoding="utf-8"
        )
        (artifacts.root / "traces").mkdir()
        (artifacts.root / "traces" / "trace-summary.json").write_text(
            json.dumps({"trace_id": "t1"}), encoding="utf-8"
        )

        text = artifacts.write_checksums().read_text(encoding="utf-8")
        names = [line.split("  ", 1)[1] for line in text.splitlines() if line.strip()]

        assert "scorer/official-output.json" in names, (
            f"scorer/ output not pinned by checksums; got {names}"
        )
        assert "traces/trace-summary.json" in names, (
            f"traces/ output not pinned by checksums; got {names}"
        )
        assert "predictions.jsonl" in names

    def test_checksum_paths_use_forward_slashes(self, tmp_path: Path) -> None:
        """Checksum manifests must be platform-stable (POSIX separators)."""
        artifacts = RunArtifacts("run-sep", tmp_path)
        (artifacts.root / "scorer").mkdir()
        (artifacts.root / "scorer" / "out.txt").write_text("x", encoding="utf-8")

        text = artifacts.write_checksums().read_text(encoding="utf-8")
        assert "\\" not in text, f"backslash in checksum manifest: {text!r}"

    def test_verify_checksums_detects_tampering(self, tmp_path: Path) -> None:
        """Design map H3 says 生成并校验 — generation alone is not the gate."""
        artifacts = RunArtifacts("run-verify", tmp_path)
        artifacts.record_prediction({"instance_id": "i1", "answer": "x"})
        (artifacts.root / "scorer").mkdir()
        (artifacts.root / "scorer" / "official-output.json").write_text(
            json.dumps({"resolved": False}), encoding="utf-8"
        )
        artifacts.write_checksums()

        assert artifacts.verify_checksums() == [], "clean tree reported mismatches"

        (artifacts.root / "scorer" / "official-output.json").write_text(
            json.dumps({"resolved": True}), encoding="utf-8"
        )
        problems = artifacts.verify_checksums()
        assert problems, "tampered official scorer output passed verification"
        assert any("official-output.json" in p for p in problems)

    def test_verify_checksums_detects_missing_file(self, tmp_path: Path) -> None:
        artifacts = RunArtifacts("run-missing", tmp_path)
        artifacts.record_prediction({"instance_id": "i1"})
        artifacts.write_checksums()

        (artifacts.root / "predictions.jsonl").unlink()
        problems = artifacts.verify_checksums()
        assert any("predictions.jsonl" in p for p in problems)

    def test_full_run_checksums_verify(self, tmp_path: Path) -> None:
        """A finalized HarnessRun tree must verify against its own manifest."""
        artifacts = RunArtifacts("run-final", tmp_path)
        harness = HarnessRun(
            run_id="run-final",
            artifacts=artifacts,
            adapter=FakeAdapter(),
            config=_pinned_config(),
        )
        harness.run([EvalInstance(instance_id="i1", task_description="t")])

        assert (artifacts.root / "checksums.sha256").exists()
        assert artifacts.verify_checksums() == []

        # And every digest matches by recomputation
        for line in (artifacts.root / "checksums.sha256").read_text(encoding="utf-8").splitlines():
            if not line.strip():
                continue
            digest, rel = line.split("  ", 1)
            actual = hashlib.sha256((artifacts.root / rel).read_bytes()).hexdigest()
            assert digest == actual, f"digest mismatch for {rel}"


# ---------------------------------------------------------------------------
# H4 gate — max_processes must be reachable, not dead code
# ---------------------------------------------------------------------------


class TestH4MaxProcessesReachable:
    def test_runner_records_process_lifecycle(self):
        """runner.py must call record_process_start/end around the adapter.

        Design map H4 requires max_processes to be implemented, and explicitly
        rejects marker files as a substitute for enforcement.  A budget field
        that nothing increments is dead code.
        """
        source = (PROJECT_ROOT / "eval" / "harness" / "runner.py").read_text(encoding="utf-8")
        assert "record_process_start" in source, (
            "runner.py never calls record_process_start() — max_processes is "
            "dead code (H4 gate violation)"
        )
        assert "record_process_end" in source, (
            "runner.py never calls record_process_end() — process count leaks"
        )

    def test_max_processes_exceeded_is_classified(self, tmp_path: Path) -> None:
        """Exceeding max_processes must surface as a classified failure."""
        from eval.harness.runner import classify_error

        usage = BudgetUsage()
        budget = Budget(max_processes=1)
        usage.active_processes = 5
        with pytest.raises(BudgetExceeded) as excinfo:
            check_budget(budget, usage)
        assert excinfo.value.kind == "processes"
        assert classify_error(excinfo.value) == "agent"

    def test_concurrent_process_budget_reachable_from_harness(self, tmp_path: Path) -> None:
        """A HarnessRun whose adapter spawns beyond the cap must fail closed."""

        class SpawningAdapter:
            """Simulates an agent that forks past the process cap."""

            def __init__(self, harness_usage_hook):
                self._hook = harness_usage_hook

            def solve_instance(self, instance: EvalInstance, working_dir: str, **kwargs) -> EvalResult:
                # Report 10 concurrent children while the cap is 2.
                self._hook(10)
                return EvalResult(instance_id=instance.instance_id, answer="ok")

        artifacts = RunArtifacts("run-proc", tmp_path)
        harness = HarnessRun(
            run_id="run-proc",
            artifacts=artifacts,
            budget=Budget(max_processes=2),
            adapter=None,
            config=_pinned_config(),
        )
        harness.adapter = SpawningAdapter(harness.report_active_processes)

        result = harness.run([EvalInstance(instance_id="i1", task_description="t")])
        summary = result["summary"]
        assert summary["completed"] == 0, (
            "instance completed despite blowing the process budget"
        )
        assert summary["failed"] >= 1
        failures = (artifacts.root / "failures.jsonl").read_text(encoding="utf-8")
        assert "processes" in failures, f"process budget not recorded: {failures}"


# ---------------------------------------------------------------------------
# H5 gate — must be explicitly BLOCKED, never implicitly passed
# ---------------------------------------------------------------------------


class TestH5NotSilentlyClaimed:
    def test_design_map_marks_h5_blocked(self):
        """The design map must not record H5 as passed while no smoke ran."""
        text = (
            PROJECT_ROOT
            / "docs"
            / "DESIGN-MAP-2026-08-07-HARNESS-MULTIMODAL-RAG-EVAL-OBSERVABILITY.md"
        ).read_text(encoding="utf-8")
        assert "H5" in text
        h5_context = [line for line in text.splitlines() if "H5" in line]
        joined = " ".join(h5_context)
        assert "BLOCKED" in joined or "未执行" in joined or "推迟" in joined, (
            f"H5 status not marked as unexecuted: {joined[:400]}"
        )
