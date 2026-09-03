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


def test_tool_registry_refreshes_skill_catalog_into_model_visible_description(tmp_path) -> None:
    agent_dir = tmp_path / ".agent"
    agent_dir.mkdir()
    (agent_dir / "skills.json").write_text(
        json.dumps(
            {
                "skills": [
                    {
                        "name": "release",
                        "description": "Prepare a release.",
                        "tools": ["Git"],
                    }
                ]
            }
        ),
        encoding="utf-8",
    )

    registry = ToolRegistry(str(tmp_path))
    first = registry.get("Skill")
    assert first is not None
    assert "release: Prepare a release." in first.description

    (agent_dir / "skills.json").write_text(
        json.dumps({"skills": [{"name": "deploy", "description": "Deploy safely."}]}),
        encoding="utf-8",
    )
    refreshed = registry.get("Skill")
    assert refreshed is not None
    assert "deploy: Deploy safely." in refreshed.description
    assert "release: Prepare a release." not in refreshed.description


def test_builtin_tool_schemas_expose_bounded_file_search_parameters() -> None:
    registry = ToolRegistry()
    specs = {tool.name: tool for tool in registry.list()}

    read_props = specs["Read"].parameters["properties"]
    assert read_props["start"]["minimum"] == 1
    assert read_props["limit"]["maximum"] == 400
    assert read_props["offset"]["minimum"] == 0

    glob_props = specs["Glob"].parameters["properties"]
    assert glob_props["head_limit"]["maximum"] == 1000

    grep_props = specs["Grep"].parameters["properties"]
    assert grep_props["output_mode"]["enum"] == ["files_with_matches", "content", "count"]
    assert grep_props["glob"]["type"] == "string"
    assert grep_props["head_limit"]["maximum"] == 500
    assert grep_props["context"]["maximum"] == 5


def test_search_knowledge_schema_is_bounded_and_auto_allowed() -> None:
    registry = ToolRegistry()
    spec = registry.get("SearchKnowledge")

    assert spec is not None
    assert spec.permission == orchestrator_pb2.AUTO_ALLOW
    assert spec.parameters["required"] == ["query"]

    props = spec.parameters["properties"]
    assert props["query"]["type"] == "string"
    assert props["top_k"] == {"type": "integer", "minimum": 1, "maximum": 50}
    assert props["mode"]["enum"] == ["hybrid", "bm25", "vector"]
    assert props["disable_rerank"]["type"] == "boolean"


def test_background_job_schemas_expose_lifecycle_controls() -> None:
    registry = ToolRegistry()
    specs = {tool.name: tool for tool in registry.list()}

    assert specs["JobStart"].permission == orchestrator_pb2.ASK_SESSION
    assert specs["JobStart"].parameters["required"] == ["command"]
    assert specs["JobStart"].parameters["properties"]["interactive"]["type"] == "boolean"
    assert specs["JobOutput"].permission == orchestrator_pb2.AUTO_ALLOW
    assert specs["JobOutput"].parameters["properties"]["timeout_ms"]["maximum"] == 600000
    assert specs["JobKill"].permission == orchestrator_pb2.ASK_SESSION
    assert specs["JobWrite"].parameters["required"] == ["job_id", "input"]


def test_session_control_schemas_are_versioned_and_fail_closed() -> None:
    registry = ToolRegistry()
    specs = {tool.name: tool for tool in registry.list()}

    fork = specs["SessionFork"]
    assert fork.permission == orchestrator_pb2.ASK_SESSION
    assert fork.parameters["required"] == ["api_version", "operation", "target_session_id", "target_seq"]
    assert fork.parameters["properties"]["api_version"]["enum"] == ["v1"]
    assert fork.parameters["properties"]["operation"]["enum"] == ["fork"]
    assert fork.parameters["properties"]["target_seq"]["minimum"] == 0

    rewind = specs["SessionRewind"]
    assert rewind.permission == orchestrator_pb2.ASK_SESSION
    assert rewind.parameters["required"] == ["api_version", "operation", "target_seq"]
    assert rewind.parameters["properties"]["api_version"]["enum"] == ["v1"]
    assert rewind.parameters["properties"]["operation"]["enum"] == ["rewind"]
    assert rewind.parameters["properties"]["target_seq"]["minimum"] == 0


def test_registry_allowlist_hides_other_tools_and_mcp_entries(tmp_path) -> None:
    (tmp_path / ".agent").mkdir()
    (tmp_path / ".agent" / "mcp-tools.json").write_text(
        '{"tools":[{"name":"external_tool","input_schema":{"type":"object"}}]}',
        encoding="utf-8",
    )

    registry = ToolRegistry(str(tmp_path), allowed_tools=frozenset({"SearchKnowledge"}))

    assert [tool.name for tool in registry.list()] == ["SearchKnowledge"]
    assert [schema["function"]["name"] for schema in registry.openai_schemas()] == [
        "SearchKnowledge"
    ]
    assert registry.get("Read") is None
