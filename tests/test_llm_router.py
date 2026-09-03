from __future__ import annotations

import asyncio

import pytest

from orchestrator.llm.client import ChatMessage, ChatRequest, ChatResponse, LLMClient
from orchestrator.llm.router import (
    ModelInfo,
    ProviderRouteError,
    ProviderRouter,
)


class RecordingClient(LLMClient):
    def __init__(self, model: str, response: str = "ok") -> None:
        self.model = model
        self.response = response
        self.requests: list[ChatRequest] = []

    async def chat(self, request: ChatRequest) -> ChatResponse:
        self.requests.append(request)
        return ChatResponse(text=self.response)


def test_router_registers_and_discovers_provider_models_without_credentials() -> None:
    client = RecordingClient("gpt-main")
    router = ProviderRouter()
    router.register(
        "openai",
        client,
        display_name="OpenAI",
        models=[
            ModelInfo(
                provider="openai",
                id="gpt-main",
                name="GPT Main",
                context_window=128_000,
                max_output_tokens=4_096,
                reasoning_efforts=("low", "high"),
            )
        ],
    )

    assert router.providers() == ({"id": "openai", "name": "OpenAI"},)
    assert router.models("openai")[0]["id"] == "gpt-main"
    manifest = router.manifest()
    assert manifest[0]["provider"] == "openai"
    assert "api_key" not in str(manifest)
    assert "context_window" in manifest[0]["models"][0]


def test_router_rejects_duplicate_provider_registration() -> None:
    router = ProviderRouter()
    router.register("local", RecordingClient("local-model"))

    with pytest.raises(ProviderRouteError) as error:
        router.register("local", RecordingClient("other-model"))

    assert error.value.code == "DUPLICATE_PROVIDER"


def test_router_resolves_unlisted_model_as_text_only_route() -> None:
    router = ProviderRouter()
    router.register("relay", RecordingClient("default-model"))

    route = router.prepare("relay", "new-model")

    assert route.provider == "relay"
    assert route.model == "new-model"
    assert route.model_info.input_modalities == ("text",)


def test_prepared_route_pins_client_and_model_across_registry_replacement() -> None:
    first = RecordingClient("first", response="from-first")
    second = RecordingClient("second", response="from-second")
    router = ProviderRouter()
    router.register("relay", first)
    prepared = router.prepare("relay", "first")

    router.replace("relay", second)
    response = asyncio.run(
        prepared.chat(
            ChatRequest(model="ignored", messages=[ChatMessage(role="user", content="hello")])
        )
    )

    assert response.text == "from-first"
    assert len(first.requests) == 1
    assert first.requests[0].model == "first"
    assert second.requests == []


def test_router_rejects_unknown_provider_with_stable_code() -> None:
    with pytest.raises(ProviderRouteError) as error:
        ProviderRouter().prepare("missing", "model")

    assert error.value.code == "NO_PROVIDER"


def test_route_for_client_prefers_named_provider_over_default_alias() -> None:
    client = RecordingClient("claude-main")
    router = ProviderRouter.from_clients(
        {"default": client, "anthropic": client}
    )

    route = router.route_for_client(client)

    assert route is not None
    assert route.provider == "anthropic"


def test_router_model_control_aliases_share_one_registry() -> None:
    router = ProviderRouter()
    router.register("local", RecordingClient("local-model"))

    assert router.list_providers() == router.providers()
    assert router.list_models("local") == router.models("local")
    assert router.resolve_model("local", "local-model").id == "local-model"
    assert router.prepare_call("local", "local-model").provider == "local"
