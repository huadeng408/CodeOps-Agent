from __future__ import annotations

import importlib.util
from pathlib import Path

import pytest


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
