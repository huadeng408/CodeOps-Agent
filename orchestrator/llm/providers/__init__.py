"""Provider adapters."""

from orchestrator.config import get_model_fast, read_env

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


def build_fast_client():
    """Build a lightweight LLM client for simple queries.

    Reads MODEL_FAST from environment (default: gpt-4o-mini).
    Detects the provider from the model name to build the right client.
    Returns None when MODEL_FAST is explicitly empty/disabled or no
    credentials are available.
    """
    model = get_model_fast()
    if not model:
        return None

    model_lower = model.lower()
    if "claude" in model_lower:
        api_key = read_env("ANTHROPIC_API_KEY")
        if not api_key or api_key.startswith("<"):
            return None
        return AnthropicClient(
            api_key=api_key,
            base_url=read_env("ANTHROPIC_BASE_URL", "https://api.anthropic.com"),
            model=model,
        )
    if "gpt" in model_lower or model_lower.startswith("o"):
        api_key = read_env("OPENAI_API_KEY")
        if not api_key or api_key.startswith("<"):
            return None
        return OpenAIClient(
            api_key=api_key,
            base_url=read_env("OPENAI_BASE_URL", "https://api.openai.com"),
            model=model,
        )
    return None


__all__ = [
    "AnthropicClient",
    "LocalClient",
    "OpenAIClient",
    "build_default_client",
    "build_fast_client",
]
