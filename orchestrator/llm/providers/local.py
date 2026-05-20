from __future__ import annotations

from dataclasses import dataclass

from ..client import ChatRequest, ChatResponse, LLMClient


@dataclass(slots=True)
class LocalClient(LLMClient):
    model: str = "local"

    async def chat(self, request: ChatRequest) -> ChatResponse:
        raise NotImplementedError("Local provider wiring is pending")
