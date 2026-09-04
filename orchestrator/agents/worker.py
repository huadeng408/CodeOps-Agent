from __future__ import annotations

import json
import sys
from typing import Any

from .deep_agent import DeepAgentManager
from .process import PROTOCOL_VERSION


def _read_request() -> dict[str, Any]:
    raw = sys.stdin.read().strip()
    if not raw:
        raise ValueError("sub-agent request is empty")
    lines = [line.strip() for line in raw.splitlines() if line.strip()]
    if len(lines) != 1:
        raise ValueError("sub-agent request must contain exactly one JSON line")
    payload = json.loads(lines[0])
    if not isinstance(payload, dict):
        raise ValueError("sub-agent request must be a JSON object")
    if payload.get("protocol_version") != PROTOCOL_VERSION:
        raise ValueError("unsupported sub-agent protocol version")
    return payload


def main() -> int:
    try:
        request = _read_request()
        manager = DeepAgentManager(
            project_root=str(request.get("project_root", ".")),
            working_dir=str(request.get("working_dir", ".")),
        )
        result = manager.run(
            kind=str(request.get("kind", "")),
            title=str(request.get("title", "")),
            objective=str(request.get("objective", "")),
            context=request.get("context") if isinstance(request.get("context"), dict) else {},
        )
        response = {
            "protocol_version": PROTOCOL_VERSION,
            "request_id": request.get("request_id", ""),
            "parent_session_id": request.get("parent_session_id", ""),
            "child_session_id": request.get("child_session_id", ""),
            "status": result.status,
            "summary": result.summary,
            "artifacts": result.artifacts,
            "notes": result.notes,
        }
        print(json.dumps(response, ensure_ascii=False, separators=(",", ":")), flush=True)
        return 0
    except Exception as exc:
        print(json.dumps({"status": "failed", "error": type(exc).__name__}), flush=True)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
