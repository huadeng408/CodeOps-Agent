"""H5 precondition: the WSL2 official-scorer path translation must be correct.

Historical scorer risk: ``_run_official_scoring_wsl()`` blindly prefixed
``/mnt/d/vscode/localcode/`` onto whatever path it was handed.  That is only
correct for a path that is *relative to the repo root*.  The unified Harness
hands the scorer an **absolute** workspace path (``RunArtifacts.root`` is
absolute, and ``AgentBenchmark.score`` receives an absolute workspace), so the
prefix produced a nonsense path such as::

    /mnt/d/vscode/localcode/D:\\vscode\\localcode\\eval_results\\...\\predictions.jsonl

The official scorer then either fails or, worse, silently reports on nothing.
H5 (the 1-instance official smoke) runs through exactly this code path, so the
translation must be proven correct before H5 can be attempted.

These tests are pure path-translation assertions: no WSL, no Docker, no network.
"""

from __future__ import annotations

import sys
from pathlib import Path

import pytest

PROJECT_ROOT = Path(__file__).resolve().parents[2]
if str(PROJECT_ROOT) not in sys.path:
    sys.path.insert(0, str(PROJECT_ROOT))

from eval.benchmarks.swebench import _to_wsl_path  # noqa: E402


class TestWindowsAbsolutePaths:
    """An absolute Windows path must map to /mnt/<drive>/... exactly once."""

    def test_absolute_windows_path_on_d_drive(self):
        got = _to_wsl_path(r"D:\vscode\localcode\eval_results\run\predictions.jsonl")
        assert got == "/mnt/d/vscode/localcode/eval_results/run/predictions.jsonl"

    def test_absolute_windows_path_forward_slashes(self):
        got = _to_wsl_path("D:/vscode/localcode/eval_results/run/predictions.jsonl")
        assert got == "/mnt/d/vscode/localcode/eval_results/run/predictions.jsonl"

    def test_absolute_path_outside_repo_is_not_forced_under_repo(self):
        """The old code produced /mnt/d/vscode/localcode/C:\\tmp\\... — nonsense."""
        got = _to_wsl_path(r"C:\tmp\scratch\predictions.jsonl")
        assert got == "/mnt/c/tmp/scratch/predictions.jsonl"
        assert "vscode/localcode/C:" not in got

    def test_no_double_repo_prefix(self):
        got = _to_wsl_path(r"D:\vscode\localcode\out\predictions.jsonl")
        assert got.count("/mnt/") == 1
        assert got.count("vscode/localcode") == 1

    def test_backslashes_never_survive_translation(self):
        got = _to_wsl_path(r"D:\vscode\localcode\a\b\c.jsonl")
        assert "\\" not in got


class TestRepoRelativePaths:
    """Relative paths stay repo-anchored (the original, legitimate case)."""

    def test_relative_path_is_anchored_to_repo_root(self):
        got = _to_wsl_path("eval_results/run/predictions.jsonl")
        assert got == "/mnt/d/vscode/localcode/eval_results/run/predictions.jsonl"

    def test_relative_path_with_backslashes(self):
        got = _to_wsl_path(r"eval_results\run\predictions.jsonl")
        assert got == "/mnt/d/vscode/localcode/eval_results/run/predictions.jsonl"

    def test_dot_prefixed_relative_path(self):
        got = _to_wsl_path("./eval_results/run/predictions.jsonl")
        assert got == "/mnt/d/vscode/localcode/eval_results/run/predictions.jsonl"

    def test_relative_path_uses_the_actual_checkout_location(self, monkeypatch):
        from eval.benchmarks import swebench as mod

        monkeypatch.setattr(mod, "_REPO_ROOT", Path(r"C:\projects\code-agent"))

        assert mod._to_wsl_path("eval_results/run/predictions.jsonl") == (
            "/mnt/c/projects/code-agent/eval_results/run/predictions.jsonl"
        )


class TestAlreadyTranslatedPaths:
    """An already-POSIX /mnt/... path must be idempotent, not re-prefixed."""

    def test_mnt_path_is_idempotent(self):
        already = "/mnt/d/vscode/localcode/eval_results/run/predictions.jsonl"
        assert _to_wsl_path(already) == already

    def test_plain_posix_absolute_path_is_preserved(self):
        assert _to_wsl_path("/home/user/preds.jsonl") == "/home/user/preds.jsonl"


class TestPathObjectsAccepted:
    """The Harness hands over pathlib objects, not strings."""

    def test_path_object_absolute(self):
        got = _to_wsl_path(Path(r"D:\vscode\localcode\out\predictions.jsonl"))
        assert got == "/mnt/d/vscode/localcode/out/predictions.jsonl"


class TestFailClosed:
    """A path that cannot be translated must raise, never guess."""

    def test_empty_path_raises(self):
        with pytest.raises(ValueError):
            _to_wsl_path("")


class TestScorerProxy:
    """The scorer subprocess must be able to reach the network it needs.

    Measured on this machine: WSL runs with ``networkingMode=mirrored`` and
    ``dnsTunneling``, and ``raw.githubusercontent.com`` resolves to ``::`` —
    so a direct connection is refused instantly and the official scorer dies
    with ``ConnectionError`` while fetching the environment requirements.
    Through the host proxy the same URL returns 200.

    WSL inherits no Windows proxy variables (``WSLENV`` is empty), so the proxy
    has to be exported *inside* the WSL command.  It is read from
    ``SWEBENCH_WSL_PROXY`` rather than hardcoded, and when unset nothing is
    exported so an environment with working DNS is unaffected.
    """

    def _capture_cmd(self, monkeypatch, **env):
        from eval.benchmarks import swebench as mod

        captured = {}

        class FakeCompleted:
            returncode = 0
            stdout = "ok"
            stderr = ""

        def fake_run(cmd, **kwargs):
            captured["cmd"] = cmd
            return FakeCompleted()

        for key in ("SWEBENCH_WSL_PROXY",):
            monkeypatch.delenv(key, raising=False)
        for key, value in env.items():
            monkeypatch.setenv(key, value)
        monkeypatch.setattr(mod.subprocess, "run", fake_run)

        mod._run_official_scoring_wsl(
            "D:/vscode/localcode/out/predictions.jsonl",
            "D:/vscode/localcode/out",
            "princeton-nlp/SWE-bench_Verified",
            "test",
            4,
            "run-1",
            60,
        )
        return " ".join(captured["cmd"])

    def test_proxy_is_exported_into_wsl_when_configured(self, monkeypatch):
        joined = self._capture_cmd(
            monkeypatch, SWEBENCH_WSL_PROXY="http://127.0.0.1:7890"
        )
        assert "http_proxy=http://127.0.0.1:7890" in joined
        assert "https_proxy=http://127.0.0.1:7890" in joined

    def test_no_proxy_exported_when_unset(self, monkeypatch):
        joined = self._capture_cmd(monkeypatch)
        assert "http_proxy=" not in joined
        assert "https_proxy=" not in joined

    def test_loopback_stays_direct_inside_wsl(self, monkeypatch):
        """Docker/ES on loopback must not be forced through the proxy."""
        joined = self._capture_cmd(
            monkeypatch, SWEBENCH_WSL_PROXY="http://127.0.0.1:7890"
        )
        assert "no_proxy=" in joined
        assert "127.0.0.1" in joined.split("no_proxy=", 1)[1][:80]


class TestScorerUsesTranslator:
    """The WSL scorer must route both paths through the translator.

    Guards against a regression back to inline f-string prefixing.
    """

    def test_wsl_scorer_source_has_no_inline_prefix(self):
        import ast
        import inspect

        from eval.benchmarks import swebench as mod

        source = inspect.getsource(mod._run_official_scoring_wsl)
        assert 'f"/mnt/d/vscode/localcode/{' not in source, (
            "inline /mnt prefix reintroduced — use _to_wsl_path()"
        )

        tree = ast.parse(source.lstrip())
        called = {
            node.func.id
            for node in ast.walk(tree)
            if isinstance(node, ast.Call) and isinstance(node.func, ast.Name)
        }
        assert "_to_wsl_path" in called, (
            "_run_official_scoring_wsl must translate paths via _to_wsl_path()"
        )
