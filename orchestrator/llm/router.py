"""Provider-neutral model routing for the Python orchestration layer.

The router is deliberately small at its seam: providers register one client and
an advisory model catalog, callers resolve an exact route, and a prepared route
pins the client/model generation used by one request. Provider credentials stay
inside the client adapter and are never part of discovery output.
"""

from __future__ import annotations

from dataclasses import dataclass, replace
from typing import Any, Iterable, Mapping

from .client import ChatRequest, ChatResponse, LLMClient, StreamDelta


class ProviderRouteError(RuntimeError):
    """Stable, provider-neutral routing failure."""

    def __init__(self, code: str, message: str) -> None:
        self.code = str(code)
        super().__init__(message)


@dataclass(frozen=True, slots=True)
class ModelInfo:
    """Advisory metadata for one provider/model route."""

    provider: str
    id: str
    name: str = ""
    context_window: int | None = None
    max_output_tokens: int | None = None
    input_modalities: tuple[str, ...] = ("text",)
    reasoning_efforts: tuple[str, ...] = ()

    def __post_init__(self) -> None:
        provider = str(self.provider).strip()
        model_id = str(self.id).strip()
        if not provider:
            raise ValueError("model provider must not be empty")
        if not model_id:
            raise ValueError("model id must not be empty")
        if self.context_window is not None and int(self.context_window) < 1:
            raise ValueError("model context_window must be positive")
        if self.max_output_tokens is not None and int(self.max_output_tokens) < 1:
            raise ValueError("model max_output_tokens must be positive")
        object.__setattr__(self, "provider", provider)
        object.__setattr__(self, "id", model_id)
        object.__setattr__(self, "name", str(self.name or model_id))
        object.__setattr__(self, "input_modalities", tuple(str(item) for item in self.input_modalities))
        object.__setattr__(self, "reasoning_efforts", tuple(str(item) for item in self.reasoning_efforts))

    def public_dict(self) -> dict[str, Any]:
        """Return model metadata safe for catalogs and audit receipts."""

        result: dict[str, Any] = {
            "provider": self.provider,
            "id": self.id,
            "name": self.name,
            "input_modalities": list(self.input_modalities),
        }
        if self.context_window is not None:
            result["context_window"] = self.context_window
        if self.max_output_tokens is not None:
            result["max_output_tokens"] = self.max_output_tokens
        if self.reasoning_efforts:
            result["reasoning_efforts"] = list(self.reasoning_efforts)
        return result


@dataclass(frozen=True, slots=True)
class PreparedRoute:
    """One immutable provider/model dispatch generation."""

    provider: str
    model: str
    client: LLMClient
    model_info: ModelInfo
    generation: int

    def _request(self, request: ChatRequest) -> ChatRequest:
        # The caller cannot override the route's model after preparation. A
        # dataclass copy retains cancellation, tools, and sampling settings.
        return replace(request, model=self.model)

    async def chat(self, request: ChatRequest) -> ChatResponse:
        return await self.client.chat(self._request(request))

    async def stream(self, request: ChatRequest):
        stream = getattr(self.client, "stream", None)
        if stream is None:
            response = await self.chat(request)
            if response.text:
                yield StreamDelta(kind="text", text=response.text)
            if response.tool_calls:
                yield StreamDelta(kind="tool_calls", tool_calls=list(response.tool_calls))
            yield StreamDelta(kind="usage", usage=response.usage)
            yield StreamDelta(kind="done", thinking_blocks=list(response.thinking_blocks))
            return
        async for delta in stream(self._request(request)):
            yield delta


@dataclass(slots=True)
class _ProviderEntry:
    client: LLMClient
    display_name: str
    models: dict[str, ModelInfo]
    generation: int


class ProviderRouter:
    """Registry and resolver for provider/model adapters.

    Model catalogs are advisory: a registered provider may accept an unlisted
    model id, which is resolved as a text-only route. Provider registration and
    replacement are atomic from a caller's perspective; prepared routes keep
    their original client and generation after a replacement.
    """

    def __init__(self) -> None:
        self._providers: dict[str, _ProviderEntry] = {}

    @classmethod
    def from_clients(cls, clients: Mapping[str, LLMClient]) -> "ProviderRouter":
        router = cls()
        for provider, client in clients.items():
            router.register(str(provider), client)
        return router

    def register(
        self,
        provider: str,
        client: LLMClient,
        *,
        display_name: str | None = None,
        models: Iterable[ModelInfo] = (),
    ) -> None:
        provider_id = self._validate_provider(provider)
        if provider_id in self._providers:
            raise ProviderRouteError("DUPLICATE_PROVIDER", "provider is already registered")
        self._providers[provider_id] = self._make_entry(
            provider_id,
            client,
            display_name=display_name,
            models=models,
            generation=1,
        )

    def replace(
        self,
        provider: str,
        client: LLMClient,
        *,
        display_name: str | None = None,
        models: Iterable[ModelInfo] = (),
    ) -> None:
        provider_id = self._validate_provider(provider)
        previous = self._providers.get(provider_id)
        if previous is None:
            raise ProviderRouteError("NO_PROVIDER", "provider is not registered")
        candidate = self._make_entry(
            provider_id,
            client,
            display_name=display_name if display_name is not None else previous.display_name,
            models=models,
            generation=previous.generation + 1,
        )
        self._providers[provider_id] = candidate

    def providers(self) -> tuple[dict[str, str], ...]:
        """List public provider metadata in registration order."""

        return tuple(
            {"id": provider, "name": entry.display_name}
            for provider, entry in self._providers.items()
        )

    def list_providers(self) -> tuple[dict[str, str], ...]:
        """Compatibility spelling for model-control integrations."""
        return self.providers()

    def models(self, provider: str) -> tuple[dict[str, Any], ...]:
        entry = self._entry(provider)
        return tuple(info.public_dict() for info in entry.models.values())

    def list_models(self, provider: str) -> tuple[dict[str, Any], ...]:
        """Compatibility spelling for model discovery integrations."""
        return self.models(provider)

    def manifest(self) -> list[dict[str, Any]]:
        """Return a credential-free provider/model catalog."""

        return [
            {
                "provider": provider,
                "name": entry.display_name,
                "models": [info.public_dict() for info in entry.models.values()],
            }
            for provider, entry in self._providers.items()
        ]

    def prepare(self, provider: str, model: str | None = None) -> PreparedRoute:
        provider_id = self._validate_provider(provider)
        entry = self._entry(provider_id)
        selected = str(model or "").strip()
        if not selected:
            selected = str(getattr(entry.client, "model", "")).strip()
        if not selected and entry.models:
            selected = next(iter(entry.models))
        if not selected:
            raise ProviderRouteError("MODEL_REQUIRED", "provider has no default model")
        info = entry.models.get(selected)
        if info is None:
            info = ModelInfo(provider=provider_id, id=selected, name=selected)
        return PreparedRoute(
            provider=provider_id,
            model=selected,
            client=entry.client,
            model_info=info,
            generation=entry.generation,
        )

    def resolve_model(self, provider: str, model: str | None = None) -> ModelInfo:
        """Resolve model metadata without exposing the provider client."""
        return self.prepare(provider, model).model_info

    def prepare_call(self, provider: str, model: str | None = None) -> PreparedRoute:
        """Bind one provider/model generation for a call."""
        return self.prepare(provider, model)

    def route_for_client(self, client: LLMClient | None) -> PreparedRoute | None:
        """Resolve the named route owned by *client*, then its default alias."""

        if client is None:
            return None
        default_provider: str | None = None
        for provider, entry in self._providers.items():
            if entry.client is client:
                if provider != "default":
                    return self.prepare(provider)
                default_provider = provider
        return self.prepare(default_provider) if default_provider is not None else None

    def _entry(self, provider: str) -> _ProviderEntry:
        provider_id = self._validate_provider(provider)
        entry = self._providers.get(provider_id)
        if entry is None:
            raise ProviderRouteError("NO_PROVIDER", "provider is not registered")
        return entry

    @staticmethod
    def _validate_provider(provider: str) -> str:
        provider_id = str(provider).strip()
        if not provider_id:
            raise ProviderRouteError("INVALID_PROVIDER", "provider must not be empty")
        return provider_id

    @staticmethod
    def _make_entry(
        provider: str,
        client: LLMClient,
        *,
        display_name: str | None,
        models: Iterable[ModelInfo],
        generation: int,
    ) -> _ProviderEntry:
        if not callable(getattr(client, "chat", None)):
            raise ProviderRouteError("INVALID_ADAPTER", "provider client must implement chat")
        catalog: dict[str, ModelInfo] = {}
        for info in models:
            if not isinstance(info, ModelInfo):
                raise ProviderRouteError("INVALID_MODEL", "provider models must use ModelInfo")
            if info.provider != provider:
                raise ProviderRouteError("INVALID_MODEL", "model provider does not match registration")
            if info.id in catalog:
                raise ProviderRouteError("DUPLICATE_MODEL", "model is already registered")
            catalog[info.id] = info
        if not catalog:
            default_model = str(getattr(client, "model", "")).strip()
            if default_model:
                catalog[default_model] = ModelInfo(provider=provider, id=default_model, name=default_model)
        return _ProviderEntry(
            client=client,
            display_name=str(display_name or provider),
            models=catalog,
            generation=generation,
        )


__all__ = ["ModelInfo", "PreparedRoute", "ProviderRouteError", "ProviderRouter"]
