from __future__ import annotations

from typing import Any

import httpx

from .config import Settings
from .models import (
    KnowledgeSearchRequestPayload,
    KnowledgeSearchResponse,
    MemorySearchRequestPayload,
    MemorySearchResponse,
    PersistRequestPayload,
    PromptContextRequestPayload,
    PromptContextResponse,
    RerankContextRequestPayload,
    RerankContextResponse,
    RetrieveRequestPayload,
    RetrieveResponse,
    SessionResponse,
)
from .trace import current_trace_id, log_request


class GoBackendClient:
    def __init__(self, settings: Settings) -> None:
        headers = {"X-Internal-Token": settings.internal_token}
        self._client = httpx.AsyncClient(
            base_url=settings.go_base_url.rstrip("/"),
            timeout=settings.request_timeout_seconds,
            headers=headers,
            trust_env=False,
        )

    async def close(self) -> None:
        await self._client.aclose()

    async def load_session(self, user_id: int) -> SessionResponse:
        payload = {"userId": user_id}
        data = await self._post_json("/internal/orchestrator/session", payload)
        return SessionResponse.model_validate(data)

    async def retrieve_context(self, payload: RetrieveRequestPayload) -> RetrieveResponse:
        data = await self._post_json(
            "/internal/orchestrator/retrieve",
            payload.model_dump(mode="json", exclude_none=True),
        )
        return RetrieveResponse.model_validate(data)

    async def prepare_prompt_context(self, payload: PromptContextRequestPayload) -> PromptContextResponse:
        data = await self._post_json(
            "/internal/orchestrator/prompt-context",
            payload.model_dump(mode="json", exclude_none=True),
        )
        return PromptContextResponse.model_validate(data)

    async def search_knowledge(self, payload: KnowledgeSearchRequestPayload) -> KnowledgeSearchResponse:
        data = await self._post_json(
            "/internal/orchestrator/knowledge-search",
            payload.model_dump(mode="json", exclude_none=True),
        )
        return KnowledgeSearchResponse.model_validate(data)

    async def search_memory(self, payload: MemorySearchRequestPayload) -> MemorySearchResponse:
        data = await self._post_json(
            "/internal/orchestrator/memory-search",
            payload.model_dump(mode="json", exclude_none=True),
        )
        return MemorySearchResponse.model_validate(data)

    async def rerank_context(self, payload: RerankContextRequestPayload) -> RerankContextResponse:
        data = await self._post_json(
            "/internal/orchestrator/rerank-context",
            payload.model_dump(mode="json", exclude_none=True),
        )
        return RerankContextResponse.model_validate(data)

    async def persist_turn(self, payload: PersistRequestPayload) -> None:
        await self._post_json(
            "/internal/orchestrator/persist",
            payload.model_dump(mode="json", exclude_none=True),
        )

    async def _post_json(self, path: str, payload: dict[str, Any]) -> dict[str, Any]:
        headers: dict[str, str] = {}
        trace_id = current_trace_id()
        if trace_id:
            headers["X-Trace-ID"] = trace_id
        response = await self._client.post(path, json=payload, headers=headers)
        response.raise_for_status()
        body = response.json()
        log_request("backend_call", path=path, status_code=response.status_code)
        data = body.get("data")
        if data is None:
            return {}
        return data
