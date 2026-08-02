"""Eval run artifacts (plan Task 8.1).

Canonical artifact tree per run:

    eval_results/<run_id>/
      run-manifest.json      # reproducible pins (eval.manifest.RunPin)
      instances.jsonl        # the instances evaluated
      predictions.jsonl      # per-instance predictions
      events.jsonl           # per-instance status transitions
      failures.jsonl         # classified failures (never silently dropped)
      summary.json           # atomic final summary
      environment.txt        # env + machine summary (secrets redacted)

Summary writes are atomic (temp file + rename) so an interrupted run never
leaves a half-written summary.
"""

from __future__ import annotations

import json
import os
import platform
import tempfile
from pathlib import Path
from typing import Any


class RunArtifacts:
    def __init__(self, run_id: str, root: str | Path) -> None:
        self.run_id = run_id
        self.root = Path(root) / run_id
        self.root.mkdir(parents=True, exist_ok=True)

    def write(self, name: str, payload: Any) -> Path:
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(payload, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
        return path

    def append_line(self, name: str, record: dict[str, Any]) -> None:
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        with path.open("a", encoding="utf-8") as handle:
            handle.write(json.dumps(record, ensure_ascii=False) + "\n")

    def write_manifest(self, manifest: dict[str, Any]) -> Path:
        return self.write("run-manifest.json", manifest)

    def record_instance(self, instance: dict[str, Any]) -> None:
        self.append_line("instances.jsonl", instance)

    def record_prediction(self, prediction: dict[str, Any]) -> None:
        self.append_line("predictions.jsonl", prediction)

    def record_event(self, instance_id: str, status: str, detail: str = "") -> None:
        self.append_line(
            "events.jsonl",
            {"instance_id": instance_id, "status": status, "detail": detail},
        )

    def record_failure(self, instance_id: str, category: str, message: str) -> None:
        self.append_line(
            "failures.jsonl",
            {"instance_id": instance_id, "category": category, "message": message},
        )

    def write_summary(self, summary: dict[str, Any]) -> Path:
        """Atomic summary write: temp file + rename, never half-written."""
        path = self.root / "summary.json"
        fd, tmp = tempfile.mkstemp(dir=self.root, suffix=".tmp")
        try:
            with os.fdopen(fd, "w", encoding="utf-8") as handle:
                json.dump(summary, handle, indent=2, ensure_ascii=False)
            os.replace(tmp, path)
        finally:
            if os.path.exists(tmp):
                os.unlink(tmp)
        return path

    def write_environment(self, secret_keys: tuple[str, ...] = ("API_KEY", "SECRET", "TOKEN")) -> Path:
        env = {}
        for key, value in os.environ.items():
            if any(token in key.upper() for token in secret_keys):
                env[key] = "<redacted>"
            else:
                env[key] = value
        payload = {
            "platform": platform.platform(),
            "python": platform.python_version(),
            "environment": env,
        }
        return self.write("environment.txt", payload)
