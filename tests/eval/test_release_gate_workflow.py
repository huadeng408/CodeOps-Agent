from __future__ import annotations

from pathlib import Path


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
