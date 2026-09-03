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

import subprocess
import sys
import time
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


def test_wsl_distro_uses_explicit_override(monkeypatch):
    monkeypatch.setenv("SWEBENCH_WSL_DISTRO", "  Ubuntu-22.04  ")
    assert sb._wsl_distro() == "Ubuntu-22.04"


def test_windows_scorer_backend_defaults_to_wsl(monkeypatch):
    monkeypatch.delenv("SWEBENCH_WINDOWS_BACKEND", raising=False)
    assert sb._windows_scorer_backend() == "wsl"


def test_windows_scorer_backend_rejects_unknown_value(monkeypatch):
    monkeypatch.setenv("SWEBENCH_WINDOWS_BACKEND", "unknown")
    with pytest.raises(ValueError, match="SWEBENCH_WINDOWS_BACKEND"):
        sb._windows_scorer_backend()


def test_wsl_runner_uses_same_explicit_distro(monkeypatch):
    captured: dict[str, object] = {}

    class Result:
        returncode = 0
        stdout = "ok"
        stderr = ""

    def fake_run(argv, **kwargs):
        captured["argv"] = argv
        captured["kwargs"] = kwargs
        return Result()

    monkeypatch.setenv("SWEBENCH_WSL_DISTRO", "Ubuntu-22.04")
    monkeypatch.setattr(sb.subprocess, "run", fake_run)

    sb._run_official_scoring_wsl(
        "preds.jsonl", "out", "ds", "test", 1, "rid", 60, "swebench"
    )

    assert captured["argv"][:4] == ["wsl.exe", "-d", "Ubuntu-22.04", "--"]


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

        def fake_native(*args, **kwargs):
            seen["impl"] = "native"
            seen["args"] = args
            return True, "stub"

        monkeypatch.setattr(sb.platform, "system", lambda: system)
        monkeypatch.setattr(sb, "_run_official_scoring_wsl", fake_wsl)
        monkeypatch.setattr(sb, "_run_official_scoring_local", fake_local)
        monkeypatch.setattr(
            sb, "_run_official_scoring_windows_native", fake_native, raising=False
        )
        return seen

    def test_windows_path_receives_default_namespace(self, monkeypatch):
        monkeypatch.delenv("SWEBENCH_NAMESPACE", raising=False)
        monkeypatch.delenv("SWEBENCH_WINDOWS_BACKEND", raising=False)
        seen = self._capture(monkeypatch, "Windows")
        sb._run_official_scoring("preds.jsonl", "out")
        assert seen["impl"] == "wsl"
        assert seen["args"][-1] == "swebench"

    def test_windows_native_backend_receives_default_namespace(self, monkeypatch):
        monkeypatch.delenv("SWEBENCH_NAMESPACE", raising=False)
        monkeypatch.setenv("SWEBENCH_WINDOWS_BACKEND", "native")
        seen = self._capture(monkeypatch, "Windows")
        sb._run_official_scoring("preds.jsonl", "out")
        assert seen["impl"] == "native"
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
        captured: dict[str, object] = {}

        class Result:
            returncode = 0
            stdout = "ok"
            stderr = ""

        def fake_run(argv, **kwargs):
            captured["cmd"] = argv[-1]
            captured["timeout"] = kwargs["timeout"]
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

    def test_outer_subprocess_timeout_does_not_extend_scorer_deadline(self, monkeypatch):
        captured = self._capture_cmd(monkeypatch)

        sb._run_official_scoring_wsl(
            "preds.jsonl", "out", "ds", "test", 1, "rid", 60, "swebench",
        )

        assert 0 < captured["timeout"] <= 60

    def test_transport_retry_shares_one_deadline(self, monkeypatch):
        timeouts: list[float] = []

        class Result:
            stdout = ""

            def __init__(self, returncode: int, stderr: str = "") -> None:
                self.returncode = returncode
                self.stderr = stderr

        def fake_run(argv, **kwargs):
            del argv
            timeouts.append(float(kwargs["timeout"]))
            if len(timeouts) == 1:
                time.sleep(0.02)
                return Result(1, "SSLEOFError")
            return Result(0)

        monkeypatch.setattr(sb.subprocess, "run", fake_run)

        ok, _ = sb._run_official_scoring_wsl(
            "preds.jsonl", "out", "ds", "test", 1, "rid", 1.0, "swebench",
        )

        assert ok is True
        assert len(timeouts) == 2
        assert 0 < timeouts[1] < timeouts[0] <= 1.0

    def test_subprocess_timeout_is_raised_for_harness_classification(self, monkeypatch):
        def fake_run(argv, **kwargs):
            raise subprocess.TimeoutExpired(argv, kwargs["timeout"])

        monkeypatch.setattr(sb.subprocess, "run", fake_run)

        with pytest.raises(TimeoutError, match="deadline"):
            sb._run_official_scoring_wsl(
                "preds.jsonl", "out", "ds", "test", 1, "rid", 1.0, "swebench",
            )

    def test_wsl_process_tree_has_an_in_distro_deadline_guard(self, monkeypatch):
        captured = self._capture_cmd(monkeypatch)

        sb._run_official_scoring_wsl(
            "preds.jsonl", "out", "ds", "test", 1, "rid", 60, "swebench",
        )

        assert "timeout --signal=TERM --kill-after=" in captured["cmd"]
        assert "s python3 -c" in captured["cmd"]

    @pytest.mark.parametrize("returncode", [124, 137])
    def test_wsl_deadline_exit_is_raised_for_harness_classification(
        self, monkeypatch, returncode
    ):
        class Result:
            stdout = ""
            stderr = "deadline"

            def __init__(self) -> None:
                self.returncode = returncode

        monkeypatch.setattr(sb.subprocess, "run", lambda *a, **k: Result())

        with pytest.raises(TimeoutError, match="deadline"):
            sb._run_official_scoring_wsl(
                "preds.jsonl", "out", "ds", "test", 1, "rid", 60, "swebench",
            )


class TestLocalScorerDeadline:
    """The Linux scorer needs an OS-enforced boundary, not an advisory value."""

    @staticmethod
    def _install_fake_swebench(monkeypatch) -> None:
        import types

        swebench = types.ModuleType("swebench")
        harness = types.ModuleType("swebench.harness")
        run_evaluation = types.ModuleType("swebench.harness.run_evaluation")
        run_evaluation.main = lambda **_kwargs: None
        monkeypatch.setitem(sys.modules, "resource", types.ModuleType("resource"))
        monkeypatch.setitem(sys.modules, "swebench", swebench)
        monkeypatch.setitem(sys.modules, "swebench.harness", harness)
        monkeypatch.setitem(
            sys.modules, "swebench.harness.run_evaluation", run_evaluation
        )

    def test_local_scorer_runs_in_a_new_session_with_exact_outer_deadline(
        self, monkeypatch
    ) -> None:
        self._install_fake_swebench(monkeypatch)
        captured: dict[str, object] = {}

        class Process:
            pid = 1234
            returncode = 0

            def communicate(self, *, timeout):
                captured["communicate_timeout"] = timeout
                return "Instances resolved: 1", ""

        def fake_popen(argv, **kwargs):
            captured["argv"] = argv
            captured.update(kwargs)
            return Process()

        monkeypatch.setattr(sb.subprocess, "Popen", fake_popen)

        ok, _ = sb._run_official_scoring_local(
            "preds.jsonl", "out", "ds", "test", 1, "rid", 0.5, "swebench"
        )

        assert ok is True
        assert captured["start_new_session"] is True
        assert 0 < float(captured["communicate_timeout"]) < 0.5

    def test_local_scorer_timeout_terminates_its_process_tree(self, monkeypatch) -> None:
        self._install_fake_swebench(monkeypatch)
        terminated: list[tuple[int, float]] = []

        class Process:
            pid = 4321
            returncode = None

            def communicate(self, *, timeout):
                raise subprocess.TimeoutExpired(["python"], timeout)

        monkeypatch.setattr(sb.subprocess, "Popen", lambda *a, **k: Process())
        monkeypatch.setattr(
            sb,
            "_terminate_local_scorer_tree",
            lambda process, grace_s: terminated.append((process.pid, grace_s)),
            raising=False,
        )

        with pytest.raises(TimeoutError, match="Harness deadline"):
            sb._run_official_scoring_local(
                "preds.jsonl", "out", "ds", "test", 1, "rid", 0.5, "swebench"
            )

        assert len(terminated) == 1
        assert terminated[0][0] == 4321

    def test_local_scorer_reaps_when_process_group_already_exited(self, monkeypatch) -> None:
        class Process:
            pid = 9876

            def __init__(self) -> None:
                self.communicate_calls = 0

            def communicate(self, **kwargs):
                del kwargs
                self.communicate_calls += 1
                return "", ""

        process = Process()
        monkeypatch.setattr(
            sb.os,
            "killpg",
            lambda *_args: (_ for _ in ()).throw(ProcessLookupError()),
            raising=False,
        )

        sb._terminate_local_scorer_tree(process, 0.1)

        assert process.communicate_calls == 1


class TestWindowsNativeScorer:
    """The native Windows path must keep the same deadline and UTF-8 contract."""

    def test_native_scorer_shims_resource_and_pins_utf8(self, monkeypatch) -> None:
        captured: dict[str, object] = {}

        class Process:
            pid = 6789
            returncode = 0

            def communicate(self, *, timeout):
                captured["communicate_timeout"] = timeout
                return "Instances resolved: 1", ""

        def fake_popen(argv, **kwargs):
            captured["argv"] = argv
            captured.update(kwargs)
            return Process()

        monkeypatch.setattr(sb.subprocess, "Popen", fake_popen)
        ok, detail = sb._run_official_scoring_windows_native(
            "preds.jsonl", "out", "ds", "test", 1, "rid", 0.5, "swebench"
        )

        assert ok is True
        assert "Instances resolved: 1" in detail
        assert captured["env"]["PYTHONUTF8"] == "1"
        assert captured["creationflags"] == getattr(
            subprocess, "CREATE_NEW_PROCESS_GROUP", 0
        )
        command = captured["argv"][2]
        assert "sys.modules['resource']" in command
        assert "from swebench.harness.run_evaluation import main" in command
        assert 0 < float(captured["communicate_timeout"]) < 0.5

    def test_native_scorer_proxy_is_explicit_and_host_allowlisted(self, monkeypatch) -> None:
        captured: dict[str, object] = {}

        class Process:
            pid = 6791
            returncode = 0

            def communicate(self, *, timeout):
                captured["communicate_timeout"] = timeout
                return "Instances resolved: 1", ""

        def fake_popen(argv, **kwargs):
            captured["argv"] = argv
            captured.update(kwargs)
            return Process()

        monkeypatch.setenv("HTTP_PROXY", "http://127.0.0.1:9")
        monkeypatch.setenv("HTTPS_PROXY", "http://127.0.0.1:9")
        monkeypatch.setenv("SWEBENCH_SCORER_PROXY", "http://127.0.0.1:7890")
        monkeypatch.setattr(sb.subprocess, "Popen", fake_popen)

        ok, _ = sb._run_official_scoring_windows_native(
            "preds.jsonl", "out", "ds", "test", 1, "rid", 0.5, "swebench"
        )

        assert ok is True
        env = captured["env"]
        assert env["HTTP_PROXY"] == "http://127.0.0.1:7890"
        assert env["HTTPS_PROXY"] == "http://127.0.0.1:7890"
        assert env["NO_PROXY"] == "127.0.0.1,localhost,::1"
        command = captured["argv"][2]
        assert "raw.githubusercontent.com" in command
        compile(command, "<native-scorer>", "exec")

    @pytest.mark.parametrize(
        "proxy",
        [
            "http://user:password@127.0.0.1:7890",
            "http://127.0.0.1:7890/path",
            "ftp://127.0.0.1:7890",
        ],
    )
    def test_native_scorer_rejects_unsafe_proxy(self, monkeypatch, proxy: str) -> None:
        monkeypatch.setenv("SWEBENCH_SCORER_PROXY", proxy)
        with pytest.raises(ValueError, match="SWEBENCH_SCORER_PROXY"):
            sb._native_scorer_env()

    def test_native_scorer_timeout_reaps_windows_process_tree(self, monkeypatch) -> None:
        class Process:
            pid = 6790
            returncode = None

            def communicate(self, *, timeout):
                raise subprocess.TimeoutExpired(["python"], timeout)

        terminated: list[int] = []
        monkeypatch.setattr(sb.subprocess, "Popen", lambda *a, **k: Process())
        monkeypatch.setattr(
            sb,
            "_terminate_windows_process_tree",
            lambda process: terminated.append(process.pid),
            raising=False,
        )

        with pytest.raises(TimeoutError, match="Harness deadline"):
            sb._run_official_scoring_windows_native(
                "preds.jsonl", "out", "ds", "test", 1, "rid", 0.5, "swebench"
            )

        assert terminated == [6790]


def test_local_scorer_group_exit_still_has_bounded_reap(monkeypatch) -> None:
    class Process:
        pid = 9877

        def __init__(self) -> None:
            self.communicate_calls = 0
            self.killed = False

        def communicate(self, **kwargs):
            self.communicate_calls += 1
            if self.communicate_calls == 1:
                raise subprocess.TimeoutExpired(["python"], kwargs["timeout"])
            return "", ""

        def kill(self):
            self.killed = True

    process = Process()
    monkeypatch.setattr(
        sb.os,
        "killpg",
        lambda *_args: (_ for _ in ()).throw(ProcessLookupError()),
        raising=False,
    )

    sb._terminate_local_scorer_tree(process, 0.1)

    assert process.killed is True
    assert process.communicate_calls == 2


def test_wsl_availability_probe_reaps_hung_process_tree(monkeypatch) -> None:
    class Process:
        pid = 2468

        def communicate(self, **kwargs):
            raise subprocess.TimeoutExpired(["wsl.exe"], kwargs["timeout"])

    terminated: list[int] = []
    monkeypatch.setattr(sb.subprocess, "Popen", lambda *a, **k: Process())
    monkeypatch.setattr(
        sb,
        "_terminate_windows_process_tree",
        lambda process: terminated.append(process.pid),
        raising=False,
    )

    monkeypatch.setattr(sb.platform, "system", lambda: "Windows")

    available, detail = sb._can_score_official()

    assert available is False
    assert "timed out" in detail.lower()
    assert terminated == [2468]
