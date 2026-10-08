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
    for job in workflow["jobs"].values():
        assert job["runs-on"] == "ubuntu-24.04"
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


def test_release_gate_checkout_fetches_history_for_parent_bound_receipts() -> None:
    """The hosted gate must resolve the source commit immediately before a receipt commit."""

    repository_root = Path(__file__).resolve().parents[2]
    lines = (repository_root / ".github" / "workflows" / "release-gate.yml").read_text(
        encoding="utf-8"
    ).splitlines()
    checkout_step = lines.index("      - name: Check out source")
    next_step = next(
        index
        for index in range(checkout_step + 1, len(lines))
        if lines[index].startswith("      - name:")
    )

    assert "          fetch-depth: 0" in lines[checkout_step:next_step]


def test_release_gate_cancels_superseded_runs_per_ref() -> None:
    workflow = (Path(__file__).resolve().parents[2] / ".github" / "workflows" / "release-gate.yml").read_text(
        encoding="utf-8"
    )

    assert "concurrency:" in workflow
    assert "group: release-gate-${{ github.workflow }}-${{ github.ref }}" in workflow
    assert "cancel-in-progress: true" in workflow
