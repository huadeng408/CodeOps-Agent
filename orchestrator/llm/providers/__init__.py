"""Provider adapters."""

from orchestrator.config import read_env

from .anthropic import AnthropicClient
from .local import LocalClient
from .openai import OpenAIClient


def build_default_client():
    provider = read_env("LLM_PROVIDER").lower()
    if provider == "openai":
        return OpenAIClient.from_env()
    if provider == "anthropic":
        return AnthropicClient.from_env()
    if provider == "local":
        return LocalClient.from_env()

    for factory in (
        OpenAIClient.from_env,
        AnthropicClient.from_env,
        LocalClient.from_env,
    ):
        client = factory()
        if client is not None:
            return client
    return None


__all__ = ["AnthropicClient", "LocalClient", "OpenAIClient", "build_default_client"]
