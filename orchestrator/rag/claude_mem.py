from __future__ import annotations

import httpx


class ClaudeMemClient:
    """Boundary adapter for Claude-Mem; benchmark modes never read L3."""

    def __init__(
        self,
        worker_url: str,
        *,
        transport: httpx.AsyncBaseTransport | None = None,
        timeout_seconds: float = 1.0,
    ) -> None:
        self.worker_url = worker_url.rstrip("/")
        self._transport = transport
        self._timeout_seconds = timeout_seconds

    async def context(
        self,
        project: str,
        *,
        query: str = "*",
        memory_mode: str = "disabled",
    ) -> str:
        if memory_mode != "enabled":
            return ""
        return ""
