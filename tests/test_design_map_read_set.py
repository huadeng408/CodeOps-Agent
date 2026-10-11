from __future__ import annotations

import subprocess
import sys
from pathlib import Path

import pytest
import yaml


@pytest.mark.parametrize("missing_path", [False, True])
def test_design_map_accepts_needed_files_but_checks_paths(tmp_path: Path, missing_path: bool) -> None:
    source = Path(__file__).resolve().parents[1] / "scripts/design_map/check.py"
    script = tmp_path / "scripts/design_map/check.py"
    script.parent.mkdir(parents=True)
    script.write_bytes(source.read_bytes())
    docs = tmp_path / "docs"
    (docs / "design").mkdir(parents=True)
    (docs / "DESIGN-MAP.md").write_text("fixture map\n", encoding="utf-8")
    (docs / "design/state.yaml").write_text("workstreams: {}\n", encoding="utf-8")
    read_set = [f"source-{index}.txt" for index in range(6)]
    for name in read_set:
        (tmp_path / name).write_text("fixture source\n", encoding="utf-8")
    if missing_path:
        read_set.append("missing-source.txt")
    (docs / "design/workplan.yaml").write_text(
        yaml.safe_dump({"tasks": [{"id": "policy", "read_set": read_set}]}), encoding="utf-8"
    )
    result = subprocess.run([sys.executable, str(script)], capture_output=True, text=True, timeout=10)
    if missing_path:
        assert result.returncode == 1
        assert "missing read_set file: missing-source.txt" in result.stderr
    else:
        assert result.returncode == 0, result.stderr
        assert "PASS (1 task(s), 0 workstream(s))" in result.stdout
