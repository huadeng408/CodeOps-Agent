from __future__ import annotations

import importlib
from pathlib import Path

import pytest

REPO_ROOT = Path(__file__).resolve().parents[2]
RECEIPT_SCRIPTS = (
    "run_h5_smoke.ps1",
    "run-terminalbench-official-receipt.ps1",
    "run-tau2-official-receipt.ps1",
    "run-o3-receipt.ps1",
)


@pytest.mark.parametrize("script_name", RECEIPT_SCRIPTS)
def test_receipt_scripts_accept_credentials_only_from_environment(script_name: str) -> None:
    script = (REPO_ROOT / "scripts" / script_name).read_text(encoding="utf-8")

    assert "D:\\Obsidian" not in script
    assert "C:\\Python312" not in script
    assert "D:\\vscode\\localcode" not in script
    assert "TAU2_CHECKOUT_PATH" in script or script_name != "run-tau2-official-receipt.ps1"
    assert "api-key.md" not in script
    assert "keyFile" not in script
    assert "LOCAL_LLM_API_KEY" in script
    assert "IsNullOrWhiteSpace" in script


@pytest.mark.parametrize("script_name", RECEIPT_SCRIPTS)
def test_receipt_scripts_never_report_credential_fingerprints(script_name: str) -> None:
    script = (REPO_ROOT / "scripts" / script_name).read_text(encoding="utf-8")

    assert "key loaded" not in script
    assert ".Substring(0" not in script


@pytest.mark.parametrize(
    "module_name",
    ("eval.swebench_work.run_h5_full", "eval.swebench_work.run_arm"),
)
def test_swebench_runners_use_process_environment_without_file_fallback(
    module_name: str,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    module = importlib.import_module(module_name)
    monkeypatch.setenv("LOCAL_LLM_API_KEY", " local-only-secret ")
    monkeypatch.setenv("DEEPSEEK_API_KEY", "deepseek-secret")

    if module.read_key() != "local-only-secret":
        pytest.fail("runner did not prefer LOCAL_LLM_API_KEY")

    monkeypatch.delenv("LOCAL_LLM_API_KEY")
    if module.read_key() != "deepseek-secret":
        pytest.fail("runner did not fall back to DEEPSEEK_API_KEY")

    monkeypatch.delenv("DEEPSEEK_API_KEY")

    with monkeypatch.context() as file_guard:
        def fail_file_access(*_args: object, **_kwargs: object) -> object:
            raise AssertionError("credential lookup must not read a key file")

        file_guard.setattr(Path, "exists", fail_file_access)
        file_guard.setattr(Path, "read_text", fail_file_access)
        assert module.read_key() in (None, "")

    source_path = REPO_ROOT / ("eval/swebench_work/" + module_name.rsplit(".", 1)[-1] + ".py")
    source = source_path.read_text(encoding="utf-8")
    assert "api-key.md" not in source
    assert "key loaded" not in source
    assert "len(key)" not in source


@pytest.mark.parametrize(
    "module_name",
    ("eval.swebench_work.run_h5_full", "eval.swebench_work.run_arm"),
)
def test_swebench_runners_do_not_echo_environment_credential(
    module_name: str,
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
) -> None:
    module = importlib.import_module(module_name)
    monkeypatch.delenv("LOCAL_LLM_API_KEY", raising=False)
    monkeypatch.delenv("DEEPSEEK_API_KEY", raising=False)
    monkeypatch.setattr(module, "read_key", lambda: "")
    monkeypatch.setattr(module.sys, "argv", [module_name.rsplit(".", 1)[-1] + ".py"] + (["baseline"] if module_name.endswith("run_arm") else []))

    assert module.main() == 2
    output = capsys.readouterr().out
    assert "length" not in output
    assert "secret" not in output
