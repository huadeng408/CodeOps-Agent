from __future__ import annotations

from pathlib import Path


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
