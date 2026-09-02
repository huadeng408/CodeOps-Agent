"""Explicit integration test for the deterministic offline Harness demo."""

from __future__ import annotations

import json
import os
import re
import subprocess
import sys
from pathlib import Path

from eval.harness.artifacts import RunArtifacts


PROJECT_ROOT = Path(__file__).resolve().parents[2]
RUN_ID = "integration-demo"
SECRET_SENTINEL = "demo-secret-must-not-be-persisted"


def test_offline_demo_emits_honest_checksum_verified_artifacts(tmp_path: Path) -> None:
    output_root = tmp_path / "demo-results"
    env = os.environ.copy()
    env["DEMO_API_KEY"] = SECRET_SENTINEL

    completed = subprocess.run(
        [
            sys.executable,
            "-m",
            "eval.demo",
            "--output-dir",
            str(output_root),
            "--run-id",
            RUN_ID,
        ],
        cwd=PROJECT_ROOT,
        env=env,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        timeout=60,
        check=False,
    )

    assert completed.returncode == 0, completed.stdout + completed.stderr
    cli_result = json.loads(completed.stdout)
    assert cli_result["status"] == "ok"
    assert cli_result["run_id"] == RUN_ID
    assert cli_result["summary"]["total"] == 1
    assert cli_result["summary"]["completed"] == 1
    assert cli_result["summary"]["failed"] == 0
    assert cli_result["checksum_problems"] == []

    run_root = output_root / RUN_ID
    expected_files = {
        "run-manifest.json",
        "instances.jsonl",
        "predictions.jsonl",
        "events.jsonl",
        "summary.json",
        "environment.txt",
        "scorer/demo-output.json",
        "traces/trace-summary.json",
        "traces/span-assertion.json",
        "checksums.sha256",
    }
    actual_files = {
        path.relative_to(run_root).as_posix()
        for path in run_root.rglob("*")
        if path.is_file()
    }
    assert expected_files <= actual_files

    manifest = json.loads((run_root / "run-manifest.json").read_text(encoding="utf-8"))
    assert manifest["mode"] == "demo"
    assert manifest["synthetic"] is True
    assert manifest["model"] == "deterministic-local-demo"
    assert manifest["network_policy"] == "disabled"
    assert re.fullmatch(r"[0-9a-f]{64}", manifest["prompt_hash"])

    prediction = json.loads(
        (run_root / "predictions.jsonl").read_text(encoding="utf-8").strip()
    )
    assert prediction["demo_passed"] is True
    assert prediction["scorer_kind"] == "synthetic-demo"
    assert "scorer_raw_output" not in prediction

    scorer_output = json.loads(
        (run_root / "scorer" / "demo-output.json").read_text(encoding="utf-8")
    )
    assert scorer_output == {
        "demo_passed": True,
        "instance_id": "demo/echo-1",
        "scorer_kind": "synthetic-demo",
    }

    trace_assertion = json.loads(
        (run_root / "traces" / "span-assertion.json").read_text(encoding="utf-8")
    )
    assert trace_assertion["verdict"] != "PASS"
    assert trace_assertion["run_id"] == RUN_ID

    artifacts = RunArtifacts(RUN_ID, output_root)
    assert artifacts.verify_checksums() == []

    artifact_text = "\n".join(
        path.read_text(encoding="utf-8", errors="replace")
        for path in run_root.rglob("*")
        if path.is_file()
    )
    assert SECRET_SENTINEL not in artifact_text
    environment = json.loads((run_root / "environment.txt").read_text(encoding="utf-8"))
    assert {"platform", "python", "machine", "processor"} <= set(environment)
    assert "environment" not in environment
    assert "DEMO_API_KEY" not in artifact_text
