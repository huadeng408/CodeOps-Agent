from __future__ import annotations

import json

from codeagent import orchestrator_pb2
from orchestrator.runtime.tools import ToolRegistry


def test_tool_registry_loads_mcp_manifest(tmp_path) -> None:
    agent_dir = tmp_path / ".agent"
    agent_dir.mkdir()
    (agent_dir / "mcp-tools.json").write_text(
        json.dumps(
            {
                "tools": [
                    {
                        "name": "fake_echo",
                        "description": "Echo text.",
                        "server": "fake",
                        "inputSchema": {
                            "type": "object",
                            "properties": {"text": {"type": "string"}},
                            "required": ["text"],
                        },
                    }
                ]
            }
        ),
        encoding="utf-8",
    )

    registry = ToolRegistry(str(tmp_path))
    names = [tool.name for tool in registry.list()]
    assert "fake_echo" in names
    spec = registry.get("fake_echo")
    assert spec is not None
    assert spec.permission == orchestrator_pb2.ASK_SESSION
    assert spec.parameters["properties"]["text"]["type"] == "string"
