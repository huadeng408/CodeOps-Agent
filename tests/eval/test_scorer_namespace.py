"""Defect J: both scoring call sites hardcoded ``namespace=None``.

``swebench.harness.run_evaluation`` defaults to ``namespace="swebench"``, which
means "pull the maintainers' prebuilt instance image".  Our two call sites
passed ``namespace=None`` instead, which means "build every instance image
locally" -- and a local build runs ``setup_repo.sh``, which must ``git clone``
the project's *entire history inside the build container*.

That is why official scoring failed on the very first instance that ever
reached it.  For ``astropy__astropy-12907`` the in-container clone streamed for
ten minutes and then died::

    error: RPC failed; curl 92 HTTP/2 stream 0 was not closed cleanly: CANCEL
    fatal: fetch-pack: invalid index-pack output
    The command '/bin/sh -c /bin/bash /root/setup_repo.sh' returned a non-zero
    code: 128

so no image was built, no tests ran, and no verdict existed.  Measured on this
host: the prebuilt image exists upstream and the daemon pull path works, while
the in-container full clone is the step that fails.

These tests pin the resolution contract only -- no Docker, no WSL, no network.
"""

from __future__ import annotations

import sys
from pathlib import Path

import pytest

PROJECT_ROOT = Path(__file__).resolve().parents[2]
if str(PROJECT_ROOT) not in sys.path:
    sys.path.insert(0, str(PROJECT_ROOT))

import eval.benchmarks.swebench as sb  # noqa: E402


class TestResolveNamespace:
    """``SWEBENCH_NAMESPACE`` controls pull-vs-build; the default must pull."""

    def test_default_is_upstreams_swebench_namespace(self, monkeypatch):
        monkeypatch.delenv("SWEBENCH_NAMESPACE", raising=False)
        assert sb._resolve_namespace() == "swebench"

    def test_literal_none_means_build_locally(self, monkeypatch):
        monkeypatch.setenv("SWEBENCH_NAMESPACE", "none")
        assert sb._resolve_namespace() is None

    def test_literal_none_is_case_insensitive(self, monkeypatch):
        monkeypatch.setenv("SWEBENCH_NAMESPACE", "NONE")
        assert sb._resolve_namespace() is None

    def test_empty_value_means_build_locally(self, monkeypatch):
        monkeypatch.setenv("SWEBENCH_NAMESPACE", "")
        assert sb._resolve_namespace() is None

    def test_whitespace_only_means_build_locally(self, monkeypatch):
        monkeypatch.setenv("SWEBENCH_NAMESPACE", "   ")
        assert sb._resolve_namespace() is None

    def test_custom_namespace_passes_through_stripped(self, monkeypatch):
        monkeypatch.setenv("SWEBENCH_NAMESPACE", "  myorg  ")
        assert sb._resolve_namespace() == "myorg"


class TestDispatchPassesNamespace:
    """The resolved namespace must reach whichever implementation runs."""

    @staticmethod
    def _capture(monkeypatch, system: str):
        seen: dict[str, object] = {}

        def fake_wsl(*args, **kwargs):
            seen["impl"] = "wsl"
            seen["args"] = args
            return True, "stub"

        def fake_local(*args, **kwargs):
            seen["impl"] = "local"
            seen["args"] = args
            return True, "stub"

        monkeypatch.setattr(sb.platform, "system", lambda: system)
        monkeypatch.setattr(sb, "_run_official_scoring_wsl", fake_wsl)
        monkeypatch.setattr(sb, "_run_official_scoring_local", fake_local)
        return seen

    def test_windows_path_receives_default_namespace(self, monkeypatch):
        monkeypatch.delenv("SWEBENCH_NAMESPACE", raising=False)
        seen = self._capture(monkeypatch, "Windows")
        sb._run_official_scoring("preds.jsonl", "out")
        assert seen["impl"] == "wsl"
        assert seen["args"][-1] == "swebench"

    def test_linux_path_receives_default_namespace(self, monkeypatch):
        monkeypatch.delenv("SWEBENCH_NAMESPACE", raising=False)
        seen = self._capture(monkeypatch, "Linux")
        sb._run_official_scoring("preds.jsonl", "out")
        assert seen["impl"] == "local"
        assert seen["args"][-1] == "swebench"

    def test_env_override_reaches_the_implementation(self, monkeypatch):
        monkeypatch.setenv("SWEBENCH_NAMESPACE", "none")
        seen = self._capture(monkeypatch, "Windows")
        sb._run_official_scoring("preds.jsonl", "out")
        assert seen["args"][-1] is None

    def test_explicit_none_is_not_overridden_by_the_default(self, monkeypatch):
        """An explicit ``namespace=None`` must stay None even when env is unset.

        This is why the sentinel exists: ``None`` is a meaningful value, so it
        cannot double as "caller said nothing".
        """
        monkeypatch.delenv("SWEBENCH_NAMESPACE", raising=False)
        seen = self._capture(monkeypatch, "Windows")
        sb._run_official_scoring("preds.jsonl", "out", namespace=None)
        assert seen["args"][-1] is None

    def test_explicit_namespace_beats_the_environment(self, monkeypatch):
        monkeypatch.setenv("SWEBENCH_NAMESPACE", "fromenv")
        seen = self._capture(monkeypatch, "Windows")
        sb._run_official_scoring("preds.jsonl", "out", namespace="explicit")
        assert seen["args"][-1] == "explicit"


class TestWslCommandEmbedsNamespace:
    """The WSL path builds a Python one-liner, so the value must be a literal."""

    @staticmethod
    def _capture_cmd(monkeypatch):
        captured: dict[str, str] = {}

        class Result:
            returncode = 0
            stdout = "ok"
            stderr = ""

        def fake_run(argv, **kwargs):
            captured["cmd"] = argv[-1]
            return Result()

        monkeypatch.setattr(sb.subprocess, "run", fake_run)
        return captured

    def test_string_namespace_is_quoted_in_the_command(self, monkeypatch):
        captured = self._capture_cmd(monkeypatch)
        sb._run_official_scoring_wsl(
            "preds.jsonl", "out", "ds", "test", 1, "rid", 60, "swebench",
        )
        assert "namespace='swebench'" in captured["cmd"]

    def test_none_namespace_is_bare_none_in_the_command(self, monkeypatch):
        captured = self._capture_cmd(monkeypatch)
        sb._run_official_scoring_wsl(
            "preds.jsonl", "out", "ds", "test", 1, "rid", 60, None,
        )
        assert "namespace=None" in captured["cmd"]

    def test_command_is_valid_python_for_both_values(self, monkeypatch):
        """A bare ``swebench`` would be a NameError inside the one-liner."""
        import ast

        for value in ("swebench", None):
            captured = self._capture_cmd(monkeypatch)
            sb._run_official_scoring_wsl(
                "preds.jsonl", "out", "ds", "test", 1, "rid", 60, value,
            )
            cmd = captured["cmd"]
            start = cmd.index('python3 -c "') + len('python3 -c "')
            snippet = cmd[start:].rstrip('"')
            ast.parse(snippet)

    def test_default_dispatch_no_longer_hardcodes_local_builds(self, monkeypatch):
        """Regression: the shipped default must not request a local build."""
        monkeypatch.delenv("SWEBENCH_NAMESPACE", raising=False)
        captured = self._capture_cmd(monkeypatch)
        monkeypatch.setattr(sb.platform, "system", lambda: "Windows")
        sb._run_official_scoring("preds.jsonl", "out")
        assert "namespace=None" not in captured["cmd"]
        assert "namespace='swebench'" in captured["cmd"]
