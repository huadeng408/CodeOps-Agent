from __future__ import annotations

from dataclasses import dataclass

from ..client import ChatRequest, ChatResponse, LLMClient


@dataclass(slots=True)
class AnthropicClient(LLMClient):
    model: str = "claude-sonnet-4-6"

    async def chat(self, request: ChatRequest) -> ChatResponse:
        raise NotImplementedError("Anthropic provider wiring is pending")
