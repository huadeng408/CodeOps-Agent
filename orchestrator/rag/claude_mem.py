from __future__ import annotations


class ClaudeMemClient:
    """Boundary adapter for Claude-Mem; benchmark modes never read L3."""

    def __init__(self, worker_url: str) -> None:
        self.worker_url = worker_url.rstrip("/")

    async def context(self, project: str, *, memory_mode: str = "disabled") -> str:
        if memory_mode != "enabled":
            return ""
        raise RuntimeError("Claude-Mem progressive read is not configured")
