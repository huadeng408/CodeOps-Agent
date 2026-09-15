from __future__ import annotations

import json

from codeagent import orchestrator_pb2
from orchestrator.runtime.tools import ToolRegistry


def test_skill_tool_exposes_confined_resource_read() -> None:
    spec = ToolRegistry().get("Skill")
    assert spec is not None
    assert spec.parameters["properties"]["resource"]["type"] == "string"
    assert spec.parameters["required"] == ["name"]


def test_memory_tool_exposes_budget_but_not_owner() -> None:
    spec = ToolRegistry().get("RecallMemory")
    assert spec is not None
    assert spec.permission == orchestrator_pb2.ASK_SESSION
    assert set(spec.parameters["properties"]) == {"query", "max_tokens", "kind", "detail"}
    assert spec.parameters["additionalProperties"] is False


def test_spawn_agent_owns_message_and_tool_allowlist_schema() -> None:
    registry = ToolRegistry()
    spawn = registry.get("SpawnAgent")
    workflow = registry.get("RunWorkflow")
    assert spawn is not None and workflow is not None
    properties = spawn.parameters["properties"]
    assert properties["message"]["properties"]["parts"]["items"]["properties"]["file"]["type"] == "object"
    assert properties["allowed_tools"]["items"]["type"] == "string"
    assert "message" not in workflow.parameters["properties"]
    assert "allowed_tools" not in workflow.parameters["properties"]


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


def test_tool_registry_hides_non_model_invocable_skills(tmp_path) -> None:
    agent_dir = tmp_path / ".agent"
    agent_dir.mkdir()
    (agent_dir / "skills.json").write_text(
        json.dumps(
            {
                "skills": [
                    {
                        "name": "internal-only",
                        "description": "Private runtime skill.",
                        "invocation": {"modelInvocable": False, "userInvocable": False},
                    },
                    {
                        "name": "model-only",
                        "description": "Safe model skill.",
                        "invocation": {"modelInvocable": True, "userInvocable": False},
                    },
                ]
            }
        ),
        encoding="utf-8",
    )

    registry = ToolRegistry(str(tmp_path))
    spec = registry.get("Skill")
    assert spec is not None
    assert "model-only: Safe model skill." in spec.description
    assert "internal-only: Private runtime skill." not in spec.description


def test_extension_manifest_refresh_never_removes_builtin_tools(tmp_path) -> None:
    agent_dir = tmp_path / ".agent"
    agent_dir.mkdir()
    manifest = agent_dir / "extensions.json"
    manifest.write_text(
        json.dumps(
            {
                "extensions": [
                    {
                        "id": "Read",
                        "kind": "lsp",
                        "version": "v1",
                        "description": "Metadata only",
                        "operations": ["inspect"],
                    }
                ]
            }
        ),
        encoding="utf-8",
    )
    registry = ToolRegistry(str(tmp_path))
    assert registry.get("Read") is not None
    manifest.write_text(json.dumps({"extensions": []}), encoding="utf-8")
    assert registry.get("Read") is not None


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


def test_registry_exposes_versioned_capability_catalog_without_execution_details() -> None:
    registry = ToolRegistry(allowed_tools={"Read", "Bash"})

    catalog = registry.capability_catalog()

    assert catalog["schema_version"] == "tools.v1"
    assert catalog["tools"] == [
        {"name": "Bash", "permission": int(orchestrator_pb2.ALWAYS_ASK)},
        {"name": "Read", "permission": int(orchestrator_pb2.AUTO_ALLOW)},
    ]
    assert all("parameters" not in item for item in catalog["tools"])
