from __future__ import annotations

import importlib
import os
import sys
from pathlib import Path

import pytest


def test_run_redacted_command_filters_both_streams_and_preserves_exit_code(
    tmp_path: Path,
    capsys,
) -> None:
    from eval.swebench_work.credential_redaction import run_redacted_command

    child = tmp_path / "emit_secret.py"
    child.write_text(
        "import os, sys\n"
        "value = os.environ['REDACTION_TEST_SECRET']\n"
        "print('stdout=' + value)\n"
        "print('stderr=Bearer ' + value, file=sys.stderr)\n"
        "print('generic=' + 's' + 'k-' + 'unit-test-token_1234567890')\n"
        "sys.exit(7)\n",
        encoding="utf-8",
    )
    secret = "python-redaction-test-sentinel"
    env = os.environ.copy()
    env["REDACTION_TEST_SECRET"] = secret

    returncode = run_redacted_command(
        [sys.executable, str(child)],
        cwd=tmp_path,
        env=env,
        secrets=(secret,),
    )

    output = capsys.readouterr().out
    assert returncode == 7
    assert secret not in output
    assert "unit-test-token_1234567890" not in output
    assert output.count("<redacted>") >= 3


@pytest.mark.parametrize(
    "module_name",
    ("eval.swebench_work.run_h5_full", "eval.swebench_work.run_arm"),
)
def test_swebench_runner_redacts_real_child_output(
    module_name: str,
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    capfd: pytest.CaptureFixture[str],
) -> None:
    module = importlib.import_module(module_name)
    package = tmp_path / "eval"
    package.mkdir()
    (package / "__init__.py").write_text("", encoding="utf-8")
    (package / "run.py").write_text(
        "import os, sys\n"
        "value = os.environ['LOCAL_LLM_API_KEY']\n"
        "print('stdout=' + value)\n"
        "print('stderr=Bearer ' + value, file=sys.stderr)\n"
        "print('generic=' + 's' + 'k-' + 'unit-test-token_1234567890')\n"
        "sys.exit(7)\n",
        encoding="utf-8",
    )
    secret = "runner-redaction-test-sentinel"
    monkeypatch.setenv("LOCAL_LLM_API_KEY", secret)
    monkeypatch.setattr(module, "REPO_ROOT", tmp_path)
    if module_name.endswith("run_arm"):
        subset = tmp_path / "subset.json"
        subset.write_text("[]", encoding="utf-8")
        monkeypatch.setattr(module, "SUBSET", subset)
        monkeypatch.setattr(module, "preflight_auth", lambda _key: (True, "HTTP 200"))
        monkeypatch.setattr(module.sys, "argv", ["run_arm.py", "baseline"])

    returncode = module.main()

    output = capfd.readouterr().out
    assert returncode == 7
    assert secret not in output
    assert "unit-test-token_1234567890" not in output
    assert output.count("<redacted>") >= 3
