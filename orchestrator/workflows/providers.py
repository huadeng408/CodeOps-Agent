from __future__ import annotations

from collections.abc import Mapping

from orchestrator.llm.client import ChatMessage, ChatRequest, LLMClient
from orchestrator.llm.router import ProviderRouteError, ProviderRouter

from .models import WorkerResult, WorkerSpec


class ProviderWorkerExecutor:
    """Executes a workflow worker through the explicitly selected LLM provider."""

    def __init__(self, providers: Mapping[str, LLMClient] | ProviderRouter) -> None:
        self._router = providers if isinstance(providers, ProviderRouter) else None
        self._providers = {} if self._router is not None else dict(providers)

    async def __call__(self, worker: WorkerSpec, upstream: dict[str, WorkerResult]) -> WorkerResult:
        route = None
        if self._router is not None:
            requested_model = worker.context.get("model")
            model = str(requested_model).strip() if isinstance(requested_model, str) else None
            try:
                route = self._router.prepare(worker.provider, model)
            except ProviderRouteError as exc:
                raise RuntimeError(f"provider is unavailable: {worker.provider}") from exc
            client = route.client
        else:
            client = self._providers.get(worker.provider)
            if client is None:
                raise RuntimeError(f"provider is unavailable: {worker.provider}")
        upstream_text = "\n".join(
            f"{worker_id}: {result.output}" for worker_id, result in sorted(upstream.items()) if result.output
        )
        prompt = f"Worker: {worker.title}\nObjective: {worker.objective}"
        if worker.context:
            prompt += f"\nContext: {worker.context}"
        if upstream_text:
            prompt += "\nUpstream results:\n" + upstream_text
        response = await client.chat(
            ChatRequest(
                model=route.model if route is not None else str(getattr(client, "model", "")),
                messages=[ChatMessage(role="user", content=prompt)],
                purpose="workflow_worker",
            )
        )
        if not response.text.strip():
            if response.tool_calls:
                raise RuntimeError("worker returned an empty or tool-only response; workflow workers require a final text result")
            raise RuntimeError("worker returned an empty or tool-only response")
        return WorkerResult.completed(worker.id, worker.provider, response.text)
