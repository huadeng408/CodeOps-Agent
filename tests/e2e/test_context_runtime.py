from __future__ import annotations

import json
import os
import subprocess
import sys
from pathlib import Path


def _run_python(code: str, *args: str, cwd: Path) -> subprocess.CompletedProcess[str]:
    env = os.environ.copy()
    existing = env.get("PYTHONPATH", "")
    env["PYTHONPATH"] = str(cwd) + (os.pathsep + existing if existing else "")
    return subprocess.run(
        [sys.executable, "-c", code, *args],
        cwd=cwd,
        env=env,
        check=False,
        capture_output=True,
        text=True,
    )


def test_context_store_survives_python_process_restart(tmp_path: Path) -> None:
    database = tmp_path / "context.sqlite"
    writer = _run_python(
        """
from orchestrator.context import SQLiteContextStore
store = SQLiteContextStore(__import__('sys').argv[1])
store.append('runtime', 'plan', {'steps': ['inspect', 'recover']})
store.append('runtime', 'tool_call', {'tool': 'Read', 'path': 'src/app.py'})
store.append('runtime', 'execution_result', {'status': 'passed', 'api_key': 'hidden'})
store.close()
""",
        str(database),
        cwd=Path(__file__).resolve().parents[2],
    )
    assert writer.returncode == 0, writer.stderr

    reader = _run_python(
        """
import json, sys
from orchestrator.context import SQLiteContextStore
store = SQLiteContextStore(sys.argv[1])
events = store.events('runtime')
print(json.dumps({'kinds': [event.kind for event in events], 'payload': events[-1].payload}))
store.close()
""",
        str(database),
        cwd=Path(__file__).resolve().parents[2],
    )
    assert reader.returncode == 0, reader.stderr
    receipt = json.loads(reader.stdout)
    assert receipt["kinds"] == ["plan", "tool_call", "execution_result"]
    assert "hidden" not in reader.stdout
    assert receipt["payload"]["status"] == "passed"
