from __future__ import annotations

import pytest

from orchestrator.runtime.extensions import (
    ExtensionRegistry,
    ExtensionSpec,
    ExtensionInvocationError,
)
from orchestrator.llm.client import ToolCall


def test_python_extension_registry_exposes_metadata_without_execution_details():
    registry = ExtensionRegistry()
    registry.register(
        ExtensionSpec(
            id="python",
            kind="code_runtime",
            version="v1",
            description="Run checked-in code",
            operations=("test",),
            max_payload_bytes=1024,
        )
    )
    with pytest.raises(ValueError):
        registry.register(
            ExtensionSpec(
                id="python",
                kind="code_runtime",
                version="v1",
                description="duplicate",
                operations=("test",),
                max_payload_bytes=1024,
            )
        )
    assert registry.metadata() == [
        {
            "id": "python",
            "kind": "code_runtime",
            "version": "v1",
            "description": "Run checked-in code",
            "operations": ["test"],
            "max_payload_bytes": 1024,
        }
    ]
    with pytest.raises(ValueError, match="operations"):
        ExtensionSpec(
            id="bad",
            kind="lsp",
            version="v1",
            description="Invalid operation shape",
            operations="inspect",
        )
    with pytest.raises(ValueError, match="description"):
        ExtensionSpec(
            id="unsafe",
            kind="lsp",
            version="v1",
            description="line1\nline2",
            operations=("inspect",),
        )


def test_python_registry_loads_go_manifest_metadata_only(tmp_path):
    manifest = tmp_path / "extensions.json"
    manifest.write_text(
        '{"extensions":[{"id":"python","kind":"code_runtime","version":"v1",'
        '"description":"Run checked-in code","operations":["test"],'
        '"max_payload_bytes":1024,"adapter":"must-not-load"}]}',
        encoding="utf-8",
    )
    registry = ExtensionRegistry.from_manifest(manifest)
    assert registry.metadata()[0]["id"] == "python"
    assert "adapter" not in registry.metadata()[0]


def test_python_invoke_requires_harness_callback_and_bounds_payload():
    registry = ExtensionRegistry()
    registry.register(
        ExtensionSpec(
            id="lsp",
            kind="lsp",
            version="v1",
            description="Language server",
            operations=("diagnostics",),
            max_payload_bytes=8,
        )
    )
    with pytest.raises(ExtensionInvocationError, match="Harness callback is required"):
        registry.invoke("lsp", "session-1", "diagnostics", b"{}")
    with pytest.raises(ExtensionInvocationError, match="payload exceeds"):
        registry.invoke("lsp", "session-1", "diagnostics", b"123456789", lambda **_: {})


def test_conversation_runner_rejects_unregistered_extension_before_harness_request():
    from orchestrator.runtime.conversation import ConversationRunner

    runner = ConversationRunner.__new__(ConversationRunner)
    runner.extensions = ExtensionRegistry()
    call = ToolCall(
        id="extension-1",
        name="Extension",
        arguments={"extension_id": "unknown", "operation": "run", "payload": {}},
        arguments_json='{"extension_id":"unknown","operation":"run","payload":{}}',
    )
    assert runner._extension_validation_error(call, "session-1") == "extension is not registered"
