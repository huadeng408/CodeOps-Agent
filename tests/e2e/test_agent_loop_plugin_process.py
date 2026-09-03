from __future__ import annotations

import json
import os
import subprocess
import sys
from pathlib import Path


def test_agent_loop_plugin_process_e2e(tmp_path: Path) -> None:
    repository_root = Path(__file__).resolve().parents[2]
    script = Path(__file__).with_name("agent_loop_plugin_process.py")
    environment = dict(os.environ)
    environment["PYTHONPATH"] = str(repository_root)
    completed = subprocess.run(
        [sys.executable, str(script), str(tmp_path)],
        cwd=repository_root,
        env=environment,
        check=True,
        capture_output=True,
        text=True,
        timeout=30,
    )

    receipt = json.loads(completed.stdout)
    assert receipt == {
        "status": "ok",
        "message": "",
        "phases": [
            "loop_start",
            "model_before",
            "model_after",
            "tool_before",
            "tool_after",
            "model_before",
            "model_after",
            "loop_end",
        ],
        "calls": 2,
        "plugin_errors": [],
    }
