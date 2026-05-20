from __future__ import annotations

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
    def __init__(self) -> None:
        self._tools: dict[str, ToolSpec] = {}
        for spec in self._default_tools():
            self.register(spec)

    def register(self, spec: ToolSpec) -> None:
        self._tools[spec.name] = spec

    def get(self, name: str) -> ToolSpec | None:
        return self._tools.get(name)

    def openai_schemas(self) -> list[dict[str, Any]]:
        return [tool.to_openai_schema() for tool in self._tools.values()]

    def permission_for(self, name: str) -> int:
        spec = self.get(name)
        if spec is None:
            return orchestrator_pb2.ALWAYS_ASK
        return spec.permission

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
        ]
