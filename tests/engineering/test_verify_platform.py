"""Unsupported process containment fails before command dispatch."""
import json
import os
from pathlib import Path
import subprocess
import sys

import pytest


@pytest.mark.skipif(os.name == "nt", reason="Windows Job containment is supported")
def test_public_verification_refuses_unsupported_platform_without_dispatch(tmp_path: Path) -> None:
    marker = tmp_path / "must-not-run"
    plan = tmp_path / "plan.json"
    plan.write_text(json.dumps({"schema_version": 1, "scope_kind": "targeted", "scope_name": "unsupported containment", "checks": [{"id": "command", "argv": [sys.executable, "-c", f"from pathlib import Path; Path({str(marker)!r}).touch()"]}]}), encoding="utf-8")
    output = tmp_path / "receipts"
    result = subprocess.run([sys.executable, "-m", "scripts.verify", "--plan", str(plan), "--output", str(output), "--run-id", "refused"], cwd=tmp_path, env={**os.environ, "PYTHONPATH": str(Path(__file__).resolve().parents[2])}, capture_output=True, text=True, encoding="utf-8", timeout=15)
    assert result.returncode == 2
    assert "Windows Job" in json.loads(result.stdout)["error"]
    assert not marker.exists() and not output.exists()
