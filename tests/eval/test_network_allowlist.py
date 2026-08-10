"""Network isolation must admit the model endpoint without lying about it (H5).

``_block_network`` pins HTTP(S)_PROXY to a dead proxy and restricts NO_PROXY to
loopback.  That was written for a *localhost* model endpoint (ollama), so a run
against a remote API (DeepSeek) had every LLM call die with ConnectionRefused:
the agent produced ``tokens_in=0, tokens_out=0`` and an empty patch, and the run
still exited 0.

The fix is an explicit allowlist, not a blanket unblock:

* only hosts named in ``network_allowlist`` bypass the dead proxy;
* everything else (github.com, pypi, the upstream fix) still fails closed;
* the run manifest must record the allowlist, and must NOT claim
  ``network_policy: disabled`` while a host was reachable.

That last point is the honesty requirement: evidence that says "network
disabled" while the agent was talking to a remote API is a false claim about
the conditions of the measurement.
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
from pathlib import Path
from typing import Any

from eval.adapter import EvalInstance, EvalResult
from eval.harness.runner import NETWORK_DISABLED_MARKER, _block_network

_PROXY_ENV_KEYS = (
    "HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy",
    "NO_PROXY", "no_proxy",
)


def _pinned_config(**overrides: object) -> dict[str, object]:
    config: dict[str, object] = {
        "git_sha": "a1b2c3d",
        "dirty_hash": "0" * 64,
        "model": "test-model",
        "prompt_hash": "0" * 64,
    }
    config.update(overrides)
    return config


# ---------------------------------------------------------------------------
# _block_network: allowlisted hosts bypass the dead proxy, others do not
# ---------------------------------------------------------------------------


def test_allowlisted_host_is_added_to_no_proxy(tmp_path: Path) -> None:
    saved = {k: os.environ[k] for k in _PROXY_ENV_KEYS if k in os.environ}
    try:
        _block_network(tmp_path, allow_hosts=("api.deepseek.com",))

        # dead proxy still pinned for everything not allowlisted
        for key in ("HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"):
            assert os.environ.get(key) == "http://127.0.0.1:9"

        for key in ("NO_PROXY", "no_proxy"):
            no_proxy = os.environ.get(key, "")
            assert "api.deepseek.com" in no_proxy, (
                "the model endpoint must bypass the dead proxy or the agent "
                "cannot call the model at all"
            )
            assert "127.0.0.1" in no_proxy  # loopback stays reachable
            assert "*" not in no_proxy      # never a blanket bypass
    finally:
        for key in _PROXY_ENV_KEYS:
            os.environ.pop(key, None)
        os.environ.update(saved)


def test_default_block_allows_nothing_beyond_loopback(tmp_path: Path) -> None:
    """Fail-closed default: no allowlist argument means no external host."""
    saved = {k: os.environ[k] for k in _PROXY_ENV_KEYS if k in os.environ}
    try:
        _block_network(tmp_path)
        no_proxy = os.environ.get("NO_PROXY", "")
        assert "127.0.0.1" in no_proxy
        for host in ("github.com", "api.deepseek.com", "pypi.org"):
            assert host not in no_proxy
        assert (tmp_path / NETWORK_DISABLED_MARKER).exists()
    finally:
        for key in _PROXY_ENV_KEYS:
            os.environ.pop(key, None)
        os.environ.update(saved)


def test_marker_records_the_allowlist(tmp_path: Path) -> None:
    """The workspace marker must state what was reachable, not just 'blocked'."""
    saved = {k: os.environ[k] for k in _PROXY_ENV_KEYS if k in os.environ}
    try:
        _block_network(tmp_path, allow_hosts=("api.deepseek.com",))
        text = (tmp_path / NETWORK_DISABLED_MARKER).read_text(encoding="utf-8")
        assert "api.deepseek.com" in text
    finally:
        for key in _PROXY_ENV_KEYS:
            os.environ.pop(key, None)
        os.environ.update(saved)


def test_child_process_inherits_the_allowlist(tmp_path: Path) -> None:
    saved = {k: os.environ[k] for k in _PROXY_ENV_KEYS if k in os.environ}
    try:
        _block_network(tmp_path, allow_hosts=("api.deepseek.com",))
        probe = subprocess.run(
            [sys.executable, "-c",
             "import os; print(os.environ.get('NO_PROXY',''))"],
            capture_output=True, text=True,
        )
        assert probe.returncode == 0, probe.stderr
        assert "api.deepseek.com" in probe.stdout
    finally:
        for key in _PROXY_ENV_KEYS:
            os.environ.pop(key, None)
        os.environ.update(saved)


# ---------------------------------------------------------------------------
# HarnessRun: allowlist is plumbed and recorded honestly in the manifest
# ---------------------------------------------------------------------------


def test_harness_passes_allowlist_and_manifest_records_it(tmp_path: Path) -> None:
    from eval.harness import Budget, HarnessRun, RunArtifacts

    saved = {k: os.environ[k] for k in _PROXY_ENV_KEYS if k in os.environ}
    seen: dict[str, str] = {}

    class Adapter:
        def solve_instance(
            self, instance: EvalInstance, working_dir: str, **kwargs: Any
        ) -> EvalResult:
            seen["no_proxy"] = os.environ.get("NO_PROXY", "")
            return EvalResult(instance_id=instance.instance_id, answer="ok")

    try:
        run_id = "test-allowlist"
        root = tmp_path / run_id
        harness = HarnessRun(
            run_id=run_id,
            artifacts=RunArtifacts(run_id=run_id, root=str(tmp_path)),
            budget=Budget(wall_clock_seconds=60, max_tokens=10_000),
            adapter=Adapter(),
            network_allowlist=("api.deepseek.com",),
            config=_pinned_config(),
        )
        harness.run([EvalInstance(instance_id="i-1", task_description="t")])

        assert "api.deepseek.com" in seen["no_proxy"], (
            "the adapter must actually see the allowlist while it runs"
        )

        manifest = json.loads(
            root.joinpath("run-manifest.json").read_text(encoding="utf-8")
        )
        assert manifest["network_allowlist"] == ["api.deepseek.com"]
        assert manifest["network_policy"] != "disabled", (
            "claiming 'disabled' while a remote host was reachable is a false "
            "statement about the measurement conditions"
        )
        assert manifest["network_policy"] == "allowlist"
    finally:
        for key in _PROXY_ENV_KEYS:
            os.environ.pop(key, None)
        os.environ.update(saved)


def test_manifest_still_says_disabled_with_no_allowlist(tmp_path: Path) -> None:
    from eval.harness import Budget, HarnessRun, RunArtifacts

    saved = {k: os.environ[k] for k in _PROXY_ENV_KEYS if k in os.environ}
    try:
        run_id = "test-no-allowlist"
        root = tmp_path / run_id
        harness = HarnessRun(
            run_id=run_id,
            artifacts=RunArtifacts(run_id=run_id, root=str(tmp_path)),
            adapter=None,
            config=_pinned_config(),
        )
        harness.run([EvalInstance(instance_id="i-1", task_description="t")])
        manifest = json.loads(
            root.joinpath("run-manifest.json").read_text(encoding="utf-8")
        )
        assert manifest["network_policy"] == "disabled"
        assert manifest["network_allowlist"] == []
    finally:
        for key in _PROXY_ENV_KEYS:
            os.environ.pop(key, None)
        os.environ.update(saved)


# ---------------------------------------------------------------------------
# eval/run.py derives the allowlist host from --base-url
# ---------------------------------------------------------------------------


def test_run_cli_allowlists_only_the_model_endpoint_host(
    tmp_path: Path, monkeypatch
) -> None:
    import eval.benchmarks.swebench as swebench_mod
    import eval.driver_headless as driver_mod
    import eval.harness as harness_mod
    from eval import run as run_mod

    captured: dict[str, Any] = {}

    class CapturingHarness:
        def __init__(self, **kwargs: Any) -> None:
            captured.update(kwargs)

        def run(self, instances: list[EvalInstance]) -> dict[str, Any]:
            return {
                "summary": {"total": 1, "ok": 1, "failed": 0},
                "summary_path": str(tmp_path / "s.json"),
            }

    class FakeDriver:
        model = "fake-model"

        def solve_instance(self, instance, working_dir="", **kwargs):
            return EvalResult(instance_id=instance.instance_id, answer="")

    monkeypatch.setattr(harness_mod, "HarnessRun", CapturingHarness)
    monkeypatch.setattr(driver_mod, "create_driver", lambda **kw: FakeDriver())
    monkeypatch.setattr(
        swebench_mod, "load_instances",
        lambda limit=None, **kw: [
            EvalInstance(instance_id="i-1", task_description="t", metadata={})
        ],
    )

    rc = run_mod.main([
        "-b", "swebench", "-m", "fake-model", "--smoke",
        "--base-url", "https://api.deepseek.com/v1", "-o", str(tmp_path),
    ])
    assert rc == 0
    assert captured.get("network_allowlist") == ("api.deepseek.com",), (
        "the CLI must allowlist exactly the model endpoint host derived from "
        f"--base-url, got {captured.get('network_allowlist')!r}"
    )


def test_localhost_base_url_needs_no_allowlist(tmp_path: Path, monkeypatch) -> None:
    """A loopback endpoint is already reachable; nothing extra is opened."""
    import eval.benchmarks.swebench as swebench_mod
    import eval.driver_headless as driver_mod
    import eval.harness as harness_mod
    from eval import run as run_mod

    captured: dict[str, Any] = {}

    class CapturingHarness:
        def __init__(self, **kwargs: Any) -> None:
            captured.update(kwargs)

        def run(self, instances):
            return {"summary": {"total": 1, "ok": 1, "failed": 0}, "summary_path": ""}

    class FakeDriver:
        model = "fake-model"

        def solve_instance(self, instance, working_dir="", **kwargs):
            return EvalResult(instance_id=instance.instance_id, answer="")

    monkeypatch.setattr(harness_mod, "HarnessRun", CapturingHarness)
    monkeypatch.setattr(driver_mod, "create_driver", lambda **kw: FakeDriver())
    monkeypatch.setattr(
        swebench_mod, "load_instances",
        lambda limit=None, **kw: [
            EvalInstance(instance_id="i-1", task_description="t", metadata={})
        ],
    )

    run_mod.main([
        "-b", "swebench", "-m", "fake-model", "--smoke",
        "--base-url", "http://127.0.0.1:11434/v1", "-o", str(tmp_path),
    ])
    assert captured.get("network_allowlist") == ()
