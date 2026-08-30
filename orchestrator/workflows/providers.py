from __future__ import annotations

from collections.abc import Mapping

from orchestrator.llm.client import ChatMessage, ChatRequest, LLMClient

from .models import WorkerResult, WorkerSpec


class ProviderWorkerExecutor:
    """Executes a workflow worker through the explicitly selected LLM provider."""

    def __init__(self, providers: Mapping[str, LLMClient]) -> None:
        self._providers = dict(providers)

    async def __call__(self, worker: WorkerSpec, upstream: dict[str, WorkerResult]) -> WorkerResult:
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
                model=str(getattr(client, "model", "")),
                messages=[ChatMessage(role="user", content=prompt)],
            )
        )
        return WorkerResult.completed(worker.id, worker.provider, response.text)
