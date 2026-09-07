from __future__ import annotations

import json
import os
import subprocess
import sys
import threading
import time
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from .types import AgentResult

PROTOCOL_VERSION = "agent.v1"
_SECRET_ENV_NAMES = {
    "OPENAI_API_KEY",
    "ANTHROPIC_API_KEY",
    "BEEAPI_API_KEY",
    "DEEPSEEK_API_KEY",
}


@dataclass(slots=True)
class ProcessAgentExecutor:
    """Run a sub-agent in a separate Python process over a JSONL contract."""

    project_root: str | Path
    working_dir: str | Path
    timeout: float = 30.0
    python_executable: str = sys.executable

    def run(
        self,
        *,
        kind: str,
        title: str,
        objective: str,
        context: dict[str, Any] | None,
        request_id: str,
        parent_session_id: str,
        child_session_id: str,
        worktree_path: str | Path | None = None,
        require_worktree: bool = False,
        cancel_event: threading.Event | None = None,
    ) -> AgentResult:
        if self.timeout <= 0:
            raise ValueError("sub-agent timeout must be positive")
        if cancel_event is not None and cancel_event.is_set():
            raise RuntimeError("sub-agent process canceled")
        effective_working_dir = Path(worktree_path or self.working_dir).resolve()
        if require_worktree:
            project_root = Path(self.project_root).resolve()
            expected_root = (project_root / ".agent" / "worktrees").resolve()
            try:
                effective_working_dir.relative_to(expected_root)
            except ValueError as exc:
                raise ValueError("harness worktree path is outside the controlled worktree root") from exc
            if not effective_working_dir.is_dir():
                raise RuntimeError("harness-assigned worktree does not exist")

        payload = {
            "protocol_version": PROTOCOL_VERSION,
            "request_id": _required_id(request_id, "request_id"),
            "parent_session_id": _required_id(parent_session_id, "parent_session_id"),
            "child_session_id": _required_id(child_session_id, "child_session_id"),
            "kind": str(kind).strip(),
            "title": str(title).strip(),
            "objective": str(objective).strip(),
            "context": context or {},
            "project_root": str(Path(self.project_root).resolve()),
            "working_dir": str(effective_working_dir),
        }
        if not payload["kind"] or not payload["title"] or not payload["objective"]:
            raise ValueError("sub-agent kind, title, and objective must not be empty")
        try:
            request_json = json.dumps(payload, ensure_ascii=False, separators=(",", ":"))
        except TypeError as exc:
            raise ValueError(f"sub-agent context must be JSON serializable: {exc}") from exc

        process = subprocess.Popen(
            [self.python_executable, "-m", "orchestrator.agents.worker"],
            cwd=str(effective_working_dir),
            env=_child_environment(),
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
        try:
            process.stdin.write(request_json + "\n")
            process.stdin.flush()
            process.stdin.close()
            # communicate() flushes stdin when the Popen object still owns a
            # stream.  Keep the EOF we just sent, but detach the closed handle
            # so Python 3.12 runners do not flush it a second time.
            process.stdin = None
            deadline = time.monotonic() + self.timeout
            while True:
                if cancel_event is not None and cancel_event.is_set():
                    process.kill()
                    stdout, stderr = process.communicate()
                    raise RuntimeError("sub-agent process canceled")
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    process.kill()
                    stdout, stderr = process.communicate()
                    raise TimeoutError("sub-agent process timed out")
                try:
                    stdout, stderr = process.communicate(timeout=min(0.1, remaining))
                    break
                except subprocess.TimeoutExpired:
                    continue
        except BrokenPipeError as exc:
            process.kill()
            process.communicate()
            raise RuntimeError("sub-agent process failed before accepting request") from exc

        if process.returncode != 0:
            detail = (stderr or "").strip().splitlines()[-1:] or ["unknown child failure"]
            response = _decode_response(stdout) if stdout.strip() else {}
            if response.get("error_code") == "required_artifact_missing":
                raise RuntimeError("sub-agent required artifact missing")
            raise RuntimeError(f"sub-agent process failed with exit code {process.returncode}: {detail[0]}")
        response = _decode_response(stdout)
        for key in ("request_id", "parent_session_id", "child_session_id"):
            if response.get(key) != payload[key]:
                raise ValueError(f"sub-agent response {key} does not match request")
        if response.get("protocol_version") != PROTOCOL_VERSION:
            raise ValueError("unsupported sub-agent protocol version")
        status = str(response.get("status", "failed"))
        if status != "completed":
            raise RuntimeError("sub-agent process returned a non-completed status")
        notes = [str(item) for item in response.get("notes", []) if str(item).strip()]
        notes.extend(
            [
                f"protocol_version: {PROTOCOL_VERSION}",
                f"child_session_id: {payload['child_session_id']}",
            ]
        )
        return AgentResult(
            summary=str(response.get("summary", "")),
            status=status,
            artifacts=[str(item) for item in response.get("artifacts", [])],
            notes=notes,
        )


def _required_id(value: str, field_name: str) -> str:
    result = str(value).strip()
    if not result:
        raise ValueError(f"sub-agent {field_name} must not be empty")
    if len(result) > 96:
        raise ValueError(f"sub-agent {field_name} is too long")
    return result


def _decode_response(stdout: str) -> dict[str, Any]:
    lines = [line.strip() for line in stdout.splitlines() if line.strip()]
    if len(lines) != 1:
        raise ValueError("sub-agent response must contain exactly one JSON line")
    try:
        payload = json.loads(lines[0])
    except json.JSONDecodeError as exc:
        raise ValueError("sub-agent response is not valid JSON") from exc
    if not isinstance(payload, dict):
        raise ValueError("sub-agent response must be a JSON object")
    return payload


def _child_environment() -> dict[str, str]:
    environment = {
        key: value for key, value in os.environ.items() if key not in _SECRET_ENV_NAMES
    }
    repository_root = str(Path(__file__).resolve().parents[2])
    existing = environment.get("PYTHONPATH", "")
    environment["PYTHONPATH"] = os.pathsep.join(item for item in (repository_root, existing) if item)
    environment["PYTHONUTF8"] = "1"
    return environment
