from __future__ import annotations

import re
from pathlib import Path

import yaml


def test_ci_provisions_dependencies_for_go_subprocesses_and_python_collection() -> None:
    workflow = yaml.load(
        (Path(__file__).resolve().parents[2] / ".github" / "workflows" / "ci.yml").read_text(encoding="utf-8"),
        Loader=yaml.BaseLoader,
    )
    go_steps = workflow["jobs"]["go"]["steps"]
    assert any(step.get("uses", "").startswith("actions/setup-python@") for step in go_steps)
    go_commands = "\n".join(step.get("run", "") for step in go_steps)
    assert 'pip install -e ".[trace-e2e]"' in go_commands
    assert 'PYTHON_EXECUTABLE=${pythonLocation}/bin/python' in go_commands
    assert go_commands.index("pip install") < go_commands.index("go test ./...")
    python_steps = workflow["jobs"]["python"]["steps"]
    for action in ("actions/setup-go@", "actions/setup-node@"):
        assert any(step.get("uses", "").startswith(action) for step in python_steps)
    python_commands = "\n".join(step.get("run", "") for step in python_steps)
    assert 'pip install -e ".[test,rag,eval]"' in python_commands
    assert python_commands.index("pip install") < python_commands.index("python -m pytest -q")
    assert python_commands.index("go mod download") < python_commands.index("python -m pytest -q")
    required_jobs = {"go", "python", "frontend", "windows-startup"}
    assert required_jobs <= workflow["jobs"].keys()
    for name in required_jobs:
        family = "windows-" if name == "windows-startup" else "ubuntu-"
        assert workflow["jobs"][name]["runs-on"].startswith(family)
        assert workflow["jobs"][name]["needs"] == "scope"
        assert workflow["jobs"][name]["if"] == (
            "${{ !cancelled() && (needs.scope.result != 'success' || needs.scope.outputs.full != 'false') }}"
        )
    windows_commands = "\n".join(step.get("run", "") for step in workflow["jobs"]["windows-startup"]["steps"])
    assert "python -m pytest -q tests/test_docker_desktop_startup.py" in windows_commands
    assert "python -m pip install $pytestRequirement" in windows_commands
    for job in workflow["jobs"].values():
        for step in job["steps"]:
            if "uses" in step:
                assert re.fullmatch(r"actions/[a-z-]+@[a-f0-9]{40}", step["uses"])
    assert "push" in workflow["on"] and "pull_request" in workflow["on"]
    assert workflow["permissions"] == {"contents": "read"}
    assert workflow["concurrency"]["cancel-in-progress"] == "true"


def test_release_gate_is_manual_only() -> None:
    """Release evidence is audited on demand, not for every source push."""

    workflow = (Path(__file__).resolve().parents[2] / ".github" / "workflows" / "release-gate.yml").read_text(
        encoding="utf-8"
    )

    assert "workflow_dispatch:" in workflow
    assert "  push:" not in workflow
    assert "  pull_request:" not in workflow
    parsed = yaml.load(workflow, Loader=yaml.BaseLoader)
    job = parsed["jobs"]["release-gate"]
    assert "if" not in job, "A manual release audit must actually execute"
    assert any(step.get("run") == "python -m eval.release_gate --json" for step in job["steps"])


def test_release_gate_checkout_fetches_history_for_runtime_equivalent_receipts() -> None:
    """The hosted gate must resolve runtime-equivalent source ancestors."""

    repository_root = Path(__file__).resolve().parents[2]
    workflow = yaml.load(
        (repository_root / ".github" / "workflows" / "release-gate.yml").read_text(encoding="utf-8"),
        Loader=yaml.BaseLoader,
    )
    checkout = next(step for step in workflow["jobs"]["release-gate"]["steps"]
                    if step.get("uses", "").startswith("actions/checkout@"))
    assert checkout["with"]["fetch-depth"] == "0"


def test_release_gate_cancels_superseded_runs_per_ref() -> None:
    workflow = (Path(__file__).resolve().parents[2] / ".github" / "workflows" / "release-gate.yml").read_text(
        encoding="utf-8"
    )

    assert "concurrency:" in workflow
    assert "group: release-gate-${{ github.workflow }}-${{ github.ref }}" in workflow
    assert "cancel-in-progress: true" in workflow
