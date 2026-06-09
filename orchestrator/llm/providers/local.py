from __future__ import annotations

from dataclasses import dataclass

from orchestrator.config import read_env

from .openai import OpenAIClient, _read_float, _read_int


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
            timeout=_read_float("LOCAL_LLM_TIMEOUT", _read_float("OPENAI_TIMEOUT", 60.0)),
            max_retries=_read_int("LOCAL_LLM_MAX_RETRIES", _read_int("OPENAI_MAX_RETRIES", 1)),
        )
