from __future__ import annotations

import asyncio
import importlib.util
from pathlib import Path

import pytest

from orchestrator.llm.client import ChatMessage, ChatRequest


def _fixture_module():
    path = Path(__file__).parent / "e2e" / "context_runtime_server.py"
    spec = importlib.util.spec_from_file_location("context_runtime_server", path)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def test_real_provider_mode_requires_explicit_credentials(monkeypatch):
    module = _fixture_module()
    monkeypatch.setenv("CODE_AGENT_E2E_PROVIDER", "anthropic")
    monkeypatch.delenv("ANTHROPIC_API_KEY", raising=False)
    monkeypatch.delenv("ANTHROPIC_AUTH_TOKEN", raising=False)

    with pytest.raises(RuntimeError, match="real provider credentials"):
        module.build_e2e_llm()


def test_real_provider_mode_builds_anthropic_client(monkeypatch):
    module = _fixture_module()
    monkeypatch.setenv("CODE_AGENT_E2E_PROVIDER", "anthropic")
    monkeypatch.setenv("ANTHROPIC_AUTH_TOKEN", "test-token")
    monkeypatch.setenv("ANTHROPIC_MODEL", "gpt-5.6-sol")

    client = module.build_e2e_llm()

    assert isinstance(client, module.AnthropicClient)
    assert client.model == "gpt-5.6-sol"


def test_unknown_provider_mode_fails_closed(monkeypatch):
    module = _fixture_module()
    monkeypatch.setenv("CODE_AGENT_E2E_PROVIDER", "unknown")

    with pytest.raises(ValueError, match="unsupported CODE_AGENT_E2E_PROVIDER"):
        module.build_e2e_llm()


def test_deterministic_resume_and_transport_recovery_prompts_are_distinct(monkeypatch):
    module = _fixture_module()
    monkeypatch.delenv("CODE_AGENT_CONTEXT_E2E_STAGE", raising=False)
    client = module.DeterministicContextLLM()

    resume = asyncio.run(client.chat(ChatRequest(
        model=client.model,
        messages=[ChatMessage(role="user", content="RESUME_AFTER_RESTART")],
    )))
    transport = asyncio.run(client.chat(ChatRequest(
        model=client.model,
        messages=[ChatMessage(role="user", content="BROWSER_TRANSPORT_RECOVERY")],
    )))

    assert resume.text.startswith("RECOVERY_MISSING:")
    assert transport.text.startswith("TRANSPORT_RECOVERED:")
    assert not resume.tool_calls
    assert not transport.tool_calls
