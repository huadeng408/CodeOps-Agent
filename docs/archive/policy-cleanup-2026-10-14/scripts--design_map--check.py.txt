"""Validate the lightweight design-map control plane."""

from __future__ import annotations

import pathlib
import sys

import yaml


ROOT = pathlib.Path(__file__).resolve().parents[2]


def fail(message: str) -> None:
    print(f"design-map check: {message}", file=sys.stderr)
    raise SystemExit(1)


def main() -> int:
    control = ROOT / "docs" / "DESIGN-MAP.md"
    state_path = ROOT / "docs" / "design" / "state.yaml"
    plan_path = ROOT / "docs" / "design" / "workplan.yaml"
    for path in (control, state_path, plan_path):
        if not path.exists():
            fail(f"missing required file: {path.relative_to(ROOT)}")

    state = yaml.safe_load(state_path.read_text(encoding="utf-8")) or {}
    plan = yaml.safe_load(plan_path.read_text(encoding="utf-8")) or {}
    statuses = {"DESIGNED", "IMPLEMENTED", "VERIFIED", "BLOCKED"}
    for name, item in (state.get("workstreams") or {}).items():
        if item.get("status") not in statuses:
            fail(f"invalid status for workstream {name}")
        evidence = ROOT / item["evidence"]
        if not evidence.exists():
            fail(f"missing evidence for workstream {name}: {item['evidence']}")

    task_ids: set[str] = set()
    for task in plan.get("tasks") or []:
        task_id = task.get("id")
        if not task_id or task_id in task_ids:
            fail(f"duplicate or missing task id: {task_id}")
        task_ids.add(task_id)
        for field in ("read_set", "write_set"):
            for relative in task.get(field) or []:
                if not (ROOT / relative).exists():
                    fail(f"task {task_id} references missing {field} file: {relative}")

    print(f"design-map check: PASS ({len(task_ids)} task(s), {len(state.get('workstreams') or {})} workstream(s))")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
