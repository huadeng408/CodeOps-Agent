"""H4 execution-constraint tests (Task 7 / Phase 4 hardening).

Makes the H4 constraints *real* rather than markers-only:
  - WORKSPACE_PRESERVED marker written on failure (category + error)
  - max_processes budget enforcement (advisory process-count check)
  - _block_network pins HTTP(S)_PROXY/NO_PROXY for child processes
"""

from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path

import pytest

from eval.adapter import EvalInstance, EvalResult
from eval.harness.artifacts import RunArtifacts
from eval.harness.budget import Budget, BudgetExceeded, BudgetUsage, check_budget
from eval.harness.runner import (
    ERROR_SCORER,
    HarnessRun,
    NETWORK_DISABLED_MARKER,
    WORKSPACE_PRESERVED_MARKER,
    _block_network,
)


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


# ---------------------------------------------------------------------------
# 1. WORKSPACE_PRESERVED marker written on failure
# ---------------------------------------------------------------------------


def test_workspace_preservation_marker_written_on_failure(tmp_path: Path) -> None:
    """FailingAdapter raises → workspace kept AND WORKSPACE_PRESERVED
    exists with the failure category and error message."""
    artifacts = RunArtifacts("run-1", tmp_path)
    workspaces: list[str] = []

    class FailingAdapter:
        def solve_instance(self, instance: EvalInstance, working_dir: str, **kwargs) -> EvalResult:
            workspaces.append(working_dir)
            raise RuntimeError("simulated agent crash")

    harness = HarnessRun(
        run_id="run-1", artifacts=artifacts, adapter=FailingAdapter(),
        config=_pinned_config(),
    )
    harness.run([EvalInstance(instance_id="crash-1", task_description="")])

    assert len(workspaces) == 1
    ws = Path(workspaces[0])
    assert ws.exists(), "workspace must be preserved after failure"
    marker = ws / WORKSPACE_PRESERVED_MARKER
    assert marker.exists(), f"missing marker: {marker}"
    content = marker.read_text(encoding="utf-8")
    assert "category: agent" in content
    assert "simulated agent crash" in content


def test_workspace_preservation_marker_scorer_failure(tmp_path: Path) -> None:
    """Scorer failure → workspace kept with category: scorer marker."""
    artifacts = RunArtifacts("run-1", tmp_path)
    workspaces: list[str] = []

    class OkAdapter:
        def solve_instance(self, instance: EvalInstance, working_dir: str, **kwargs) -> EvalResult:
            workspaces.append(working_dir)
            return EvalResult(instance_id=instance.instance_id, answer="ok")

    def bad_scorer(
        result: EvalResult,
        instance: EvalInstance,
        workspace: Path,
        *,
        timeout_s: float,
    ) -> dict:
        del timeout_s
        raise RuntimeError("scorer crashed")

    harness = HarnessRun(
        run_id="run-1", artifacts=artifacts,
        adapter=OkAdapter(), scorer=bad_scorer,
        config=_pinned_config(),
    )
    harness.run([EvalInstance(instance_id="s-1", task_description="")])

    marker = Path(workspaces[0]) / WORKSPACE_PRESERVED_MARKER
    assert marker.exists(), "scorer failure must preserve workspace with marker"
    content = marker.read_text(encoding="utf-8")
    assert "category: scorer" in content
    assert "scorer crashed" in content


# ---------------------------------------------------------------------------
# 2. max_processes budget enforced
# ---------------------------------------------------------------------------


def test_max_processes_budget_enforced() -> None:
    """record_process_start over Budget.max_processes → check_budget raises
    BudgetExceeded (loud failure; the count is advisory without OS support)."""
    budget = Budget(max_processes=2)
    usage = BudgetUsage()
    usage.record_process_start()
    usage.record_process_start()
    check_budget(budget, usage)  # exactly at the limit → ok

    usage.record_process_start()  # 3 > 2 → loud failure
    with pytest.raises(BudgetExceeded) as exc:
        check_budget(budget, usage)
    assert exc.value.kind == "processes"

    usage.record_process_end()  # back to 2 → ok again
    check_budget(budget, usage)


def test_record_process_end_never_goes_negative() -> None:
    usage = BudgetUsage()
    usage.record_process_end()
    usage.record_process_end()
    assert usage.active_processes == 0


# ---------------------------------------------------------------------------
# 3. network block sets env vars
# ---------------------------------------------------------------------------

_PROXY_ENV_KEYS = (
    "HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy",
    "NO_PROXY", "no_proxy",
)


def test_network_block_sets_env_vars(tmp_path: Path) -> None:
    """_block_network pins proxy env vars for child processes + marker."""
    saved = {key: os.environ[key] for key in _PROXY_ENV_KEYS if key in os.environ}
    try:
        _block_network(tmp_path)

        # proxy env pinned to a dead proxy (discard port, fail fast)
        for key in ("HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"):
            assert os.environ.get(key) == "http://127.0.0.1:9"
        # loopback stays reachable; "*" would bypass the block entirely
        no_proxy = os.environ.get("NO_PROXY", "")
        assert "127.0.0.1" in no_proxy
        assert "*" not in no_proxy
        # marker still written
        assert (tmp_path / NETWORK_DISABLED_MARKER).exists()

        # child processes actually inherit the block
        probe = subprocess.run(
            [sys.executable, "-c", "import os; print(os.environ.get('HTTP_PROXY', ''))"],
            capture_output=True, text=True,
        )
        assert probe.returncode == 0, probe.stderr
        assert probe.stdout.strip() == "http://127.0.0.1:9"
    finally:
        for key in _PROXY_ENV_KEYS:
            os.environ.pop(key, None)
        os.environ.update(saved)
