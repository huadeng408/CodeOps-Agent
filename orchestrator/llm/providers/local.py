from __future__ import annotations

from dataclasses import dataclass

from orchestrator.config import read_env

from .openai import OpenAIClient


@dataclass(slots=True)
class LocalClient(OpenAIClient):
    @classmethod
    def from_env(cls) -> LocalClient | None:
        base_url = read_env("LOCAL_LLM_BASE_URL") or read_env("LOCAL_OPENAI_BASE_URL")
        if not base_url:
            return None
        return cls(
            api_key=read_env("LOCAL_LLM_API_KEY") or read_env("LOCAL_OPENAI_API_KEY"),
            base_url=base_url,
            model=read_env("LOCAL_LLM_MODEL") or "local",
        )
