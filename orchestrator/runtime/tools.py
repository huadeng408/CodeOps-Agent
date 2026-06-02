from __future__ import annotations

import json
from pathlib import Path
from dataclasses import dataclass
from typing import Any

from codeagent import orchestrator_pb2


@dataclass(frozen=True, slots=True)
class ToolSpec:
    name: str
    description: str
    parameters: dict[str, Any]
    permission: int

    def to_openai_schema(self) -> dict[str, Any]:
        return {
            "type": "function",
            "function": {
                "name": self.name,
                "description": self.description,
                "parameters": self.parameters,
            },
        }


class ToolRegistry:
    def __init__(self, project_root: str | None = None) -> None:
        self._project_root = Path(project_root).resolve() if project_root else None
        self._mcp_manifest_path = (
            self._project_root / ".agent" / "mcp-tools.json" if self._project_root else None
        )
        self._tools: dict[str, ToolSpec] = {}
        self._mcp_tool_names: set[str] = set()
        for spec in self._default_tools():
            self.register(spec, builtin=True)

    def register(self, spec: ToolSpec, builtin: bool = False) -> None:
        self._tools[spec.name] = spec
        if not builtin:
            self._mcp_tool_names.add(spec.name)

    def get(self, name: str) -> ToolSpec | None:
        self._refresh_mcp_tools()
        return self._tools.get(name)

    def list(self) -> list[ToolSpec]:
        self._refresh_mcp_tools()
        return sorted(self._tools.values(), key=lambda tool: tool.name.lower())

    def openai_schemas(self) -> list[dict[str, Any]]:
        self._refresh_mcp_tools()
        return [tool.to_openai_schema() for tool in self.list()]

    def permission_for(self, name: str) -> int:
        spec = self.get(name)
        if spec is None:
            return orchestrator_pb2.ALWAYS_ASK
        return spec.permission

    def _refresh_mcp_tools(self) -> None:
        if self._mcp_manifest_path is None:
            return
        if not self._mcp_manifest_path.exists():
            for name in list(self._mcp_tool_names):
                self._tools.pop(name, None)
            self._mcp_tool_names.clear()
            return

        try:
            payload = json.loads(self._mcp_manifest_path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError):
            return
        raw_tools = payload.get("tools", []) if isinstance(payload, dict) else []
        next_names: set[str] = set()
        for raw in raw_tools:
            if not isinstance(raw, dict):
                continue
            name = str(raw.get("name", "")).strip()
            if not name:
                continue
            description = str(raw.get("description", "")).strip() or "MCP tool"
            server = str(raw.get("server", "")).strip()
            if server:
                description = f"{description} (MCP server: {server})"
            parameters = raw.get("input_schema") or raw.get("inputSchema") or {
                "type": "object",
                "properties": {},
            }
            if not isinstance(parameters, dict):
                parameters = {
                    "type": "object",
                    "properties": {},
                }
            spec = ToolSpec(
                name=name,
                description=description,
                parameters=parameters,
                permission=orchestrator_pb2.ASK_SESSION,
            )
            self._tools[name] = spec
            next_names.add(name)

        for name in list(self._mcp_tool_names - next_names):
            self._tools.pop(name, None)
        self._mcp_tool_names = next_names

    @staticmethod
    def _default_tools() -> list[ToolSpec]:
        return [
            ToolSpec(
                name="Read",
                description="Read a file from the workspace.",
                permission=orchestrator_pb2.AUTO_ALLOW,
                parameters={
                    "type": "object",
                    "properties": {
                        "path": {"type": "string", "description": "Workspace file path."}
                    },
                    "required": ["path"],
                },
            ),
            ToolSpec(
                name="Glob",
                description="Find files in the workspace by glob pattern.",
                permission=orchestrator_pb2.AUTO_ALLOW,
                parameters={
                    "type": "object",
                    "properties": {
                        "pattern": {"type": "string", "description": "Glob pattern."}
                    },
                    "required": ["pattern"],
                },
            ),
            ToolSpec(
                name="Grep",
                description="Search workspace files for text.",
                permission=orchestrator_pb2.AUTO_ALLOW,
                parameters={
                    "type": "object",
                    "properties": {
                        "pattern": {"type": "string", "description": "Text to search for."},
                        "path": {"type": "string", "description": "Directory to search."},
                    },
                    "required": ["pattern"],
                },
            ),
            ToolSpec(
                name="Write",
                description="Write a file in the workspace.",
                permission=orchestrator_pb2.ASK_SESSION,
                parameters={
                    "type": "object",
                    "properties": {
                        "path": {"type": "string"},
                        "content": {"type": "string"},
                    },
                    "required": ["path", "content"],
                },
            ),
            ToolSpec(
                name="Edit",
                description="Replace text in a workspace file.",
                permission=orchestrator_pb2.ASK_SESSION,
                parameters={
                    "type": "object",
                    "properties": {
                        "path": {"type": "string"},
                        "old": {"type": "string"},
                        "new": {"type": "string"},
                    },
                    "required": ["path", "old", "new"],
                },
            ),
            ToolSpec(
                name="Bash",
                description="Run a shell command after harness permission checks.",
                permission=orchestrator_pb2.ALWAYS_ASK,
                parameters={
                    "type": "object",
                    "properties": {
                        "command": {"type": "string"},
                    },
                    "required": ["command"],
                },
            ),
            ToolSpec(
                name="TodoWrite",
                description="Update the task list for the current conversation.",
                permission=orchestrator_pb2.AUTO_ALLOW,
                parameters={
                    "type": "object",
                    "properties": {
                        "todos": {
                            "type": "array",
                            "items": {
                                "type": "object",
                                "properties": {
                                    "content": {"type": "string"},
                                    "active_form": {"type": "string"},
                                    "status": {
                                        "type": "string",
                                        "enum": ["pending", "in_progress", "completed"],
                                    },
                                },
                                "required": ["content", "active_form", "status"],
                            },
                        }
                    },
                    "required": ["todos"],
                },
            ),
            ToolSpec(
                name="PlanWrite",
                description="Update the current plan for the conversation.",
                permission=orchestrator_pb2.AUTO_ALLOW,
                parameters={
                    "type": "object",
                    "properties": {
                        "steps": {
                            "type": "array",
                            "items": {"type": "string"},
                        },
                        "current_index": {
                            "type": "integer",
                            "minimum": 0,
                        },
                        "mode": {
                            "type": "string",
                            "description": "Planning mode label.",
                        },
                    },
                    "required": ["steps"],
                },
            ),
            ToolSpec(
                name="SpawnAgent",
                description="Spawn a sub-agent for an independent task.",
                permission=orchestrator_pb2.AUTO_ALLOW,
                parameters={
                    "type": "object",
                    "properties": {
                        "kind": {
                            "type": "string",
                            "description": "Sub-agent type such as explore, general, plan, or background.",
                        },
                        "title": {
                            "type": "string",
                            "description": "Short task title.",
                        },
                        "objective": {
                            "type": "string",
                            "description": "Task objective for the sub-agent.",
                        },
                        "parallel": {
                            "type": "boolean",
                            "description": "Whether the task can run in parallel.",
                        },
                        "context": {
                            "type": "object",
                            "description": "Structured context for the sub-agent.",
                        },
                        "context_json": {
                            "type": "string",
                            "description": "Serialized structured context for the sub-agent.",
                        },
                    },
                    "required": ["kind", "title", "objective"],
                },
            ),
            ToolSpec(
                name="AskUser",
                description="Ask the user a question when human input is needed.",
                permission=orchestrator_pb2.AUTO_ALLOW,
                parameters={
                    "type": "object",
                    "properties": {
                        "question": {
                            "type": "string",
                            "description": "Question to present to the user.",
                        },
                        "options": {
                            "type": "array",
                            "items": {
                                "type": "object",
                                "properties": {
                                    "label": {"type": "string"},
                                    "description": {"type": "string"},
                                    "preview": {"type": "string"},
                                },
                                "required": ["label"],
                            },
                        },
                        "multi_select": {
                            "type": "boolean",
                            "description": "Whether more than one option may be selected.",
                        },
                    },
                    "required": ["question"],
                },
            ),
            ToolSpec(
                name="Git",
                description="Run a safe git subcommand.",
                permission=orchestrator_pb2.ASK_SESSION,
                parameters={
                    "type": "object",
                    "properties": {
                        "command": {"type": "string"},
                        "args": {"type": "array", "items": {"type": "string"}},
                    },
                    "required": ["command"],
                },
            ),
            ToolSpec(
                name="WebFetch",
                description="Fetch a URL.",
                permission=orchestrator_pb2.ASK_SESSION,
                parameters={
                    "type": "object",
                    "properties": {
                        "url": {"type": "string"},
                    },
                    "required": ["url"],
                },
            ),
            ToolSpec(
                name="WebSearch",
                description="Search the web for current information.",
                permission=orchestrator_pb2.ASK_SESSION,
                parameters={
                    "type": "object",
                    "properties": {
                        "query": {"type": "string"},
                    },
                    "required": ["query"],
                },
            ),
        ]
