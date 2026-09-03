from __future__ import annotations

import json
from pathlib import Path
from dataclasses import dataclass
from typing import Any, Collection

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
    def __init__(
        self,
        project_root: str | None = None,
        allowed_tools: Collection[str] | None = None,
    ) -> None:
        self._project_root = Path(project_root).resolve() if project_root else None
        self._mcp_manifest_path = (
            self._project_root / ".agent" / "mcp-tools.json" if self._project_root else None
        )
        self._skills_manifest_path = (
            self._project_root / ".agent" / "skills.json" if self._project_root else None
        )
        self._tools: dict[str, ToolSpec] = {}
        self._mcp_tool_names: set[str] = set()
        self._allowed_tools = frozenset(allowed_tools) if allowed_tools is not None else None
        for spec in self._default_tools():
            self.register(spec, builtin=True)

    def register(self, spec: ToolSpec, builtin: bool = False) -> None:
        if self._allowed_tools is not None and spec.name not in self._allowed_tools:
            return
        self._tools[spec.name] = spec
        if not builtin:
            self._mcp_tool_names.add(spec.name)

    def get(self, name: str) -> ToolSpec | None:
        self._refresh_mcp_tools()
        self._refresh_skills_catalog()
        return self._tools.get(name)

    def list(self) -> list[ToolSpec]:
        self._refresh_mcp_tools()
        self._refresh_skills_catalog()
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
            if self._allowed_tools is not None and name not in self._allowed_tools:
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

    def _refresh_skills_catalog(self) -> None:
        if self._skills_manifest_path is None:
            return
        skill_tool = self._tools.get("Skill")
        if skill_tool is None:
            return
        description = (
            "Invoke a registered skill by name. Returns the skill prompt "
            "and instructions to be injected into the conversation."
        )
        try:
            payload = json.loads(self._skills_manifest_path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError):
            payload = {}
        raw_skills = payload.get("skills", []) if isinstance(payload, dict) else []
        catalog: list[str] = []
        for raw in raw_skills:
            if not isinstance(raw, dict):
                continue
            name = str(raw.get("name", "")).strip()
            if not name:
                continue
            summary = str(raw.get("description", "")).strip() or "No description."
            catalog.append(f"{name}: {summary}")
        if catalog:
            description += " Available skills: " + "; ".join(sorted(catalog)) + "."
        self._tools["Skill"] = ToolSpec(
            name=skill_tool.name,
            description=description,
            parameters=skill_tool.parameters,
            permission=skill_tool.permission,
        )

    @staticmethod
    def _default_tools() -> list[ToolSpec]:
        return [
            ToolSpec(
                name="Read",
                description=(
                    "Read a file or a bounded line range from the workspace. "
                    "Prefer start and limit for source files instead of reading entire files."
                ),
                permission=orchestrator_pb2.AUTO_ALLOW,
                parameters={
                    "type": "object",
                    "properties": {
                        "path": {"type": "string", "description": "Workspace file path."},
                        "start": {
                            "type": "integer",
                            "minimum": 1,
                            "description": "One-based starting line number for text files.",
                        },
                        "offset": {
                            "type": "integer",
                            "minimum": 0,
                            "description": "Zero-based line offset for text files. Prefer start.",
                        },
                        "limit": {
                            "type": "integer",
                            "minimum": 1,
                            "maximum": 400,
                            "description": "Maximum number of text lines to return.",
                        },
                        "pages": {
                            "type": "string",
                            "description": "PDF page range hint, for example '1-3'.",
                        },
                        "ocr": {
                            "type": "boolean",
                            "description": "Use MinerU OCR mode for PDF files (default: true).",
                        },
                    },
                    "required": ["path"],
                },
            ),
            ToolSpec(
                name="ReadSpill",
                description=(
                    "Retrieve a bounded complete redacted result previously returned by a tool. "
                    "Use only the opaque spill locator from that result."
                ),
                permission=orchestrator_pb2.AUTO_ALLOW,
                parameters={
                    "type": "object",
                    "properties": {
                        "locator": {
                            "type": "string",
                            "description": "Opaque spill:// locator returned by a tool.",
                        },
                        "start": {
                            "type": "integer",
                            "minimum": 1,
                            "description": "Optional one-based line number to start reading.",
                        },
                        "offset": {
                            "type": "integer",
                            "minimum": 0,
                            "description": "Optional zero-based line offset; prefer start.",
                        },
                        "limit": {
                            "type": "integer",
                            "minimum": 1,
                            "description": "Optional maximum number of lines to return.",
                        },
                    },
                    "required": ["locator"],
                },
            ),
            ToolSpec(
                name="Glob",
                description="Find files in the workspace by glob pattern, with bounded output.",
                permission=orchestrator_pb2.AUTO_ALLOW,
                parameters={
                    "type": "object",
                    "properties": {
                        "pattern": {"type": "string", "description": "Glob pattern."},
                        "head_limit": {
                            "type": "integer",
                            "minimum": 1,
                            "maximum": 1000,
                            "description": "Maximum number of matching file paths to return.",
                        },
                    },
                    "required": ["pattern"],
                },
            ),
            ToolSpec(
                name="Grep",
                description="Search workspace files for text. Prefer files_with_matches before content output.",
                permission=orchestrator_pb2.AUTO_ALLOW,
                parameters={
                    "type": "object",
                    "properties": {
                        "pattern": {"type": "string", "description": "Text to search for."},
                        "path": {"type": "string", "description": "Directory to search."},
                        "output_mode": {
                            "type": "string",
                            "enum": ["files_with_matches", "content", "count"],
                            "description": "Output files, matching lines, or match counts.",
                        },
                        "glob": {
                            "type": "string",
                            "description": "Only search files matching this workspace glob.",
                        },
                        "head_limit": {
                            "type": "integer",
                            "minimum": 0,
                            "maximum": 500,
                            "description": "Maximum output rows; 0 means no row limit.",
                        },
                        "context": {
                            "type": "integer",
                            "minimum": 0,
                            "maximum": 5,
                            "description": "Context lines before and after each match.",
                        },
                        "ignore_case": {"type": "boolean"},
                        "only_matching": {"type": "boolean"},
                    },
                    "required": ["pattern"],
                },
            ),
            ToolSpec(
                name="SearchKnowledge",
                description="Search the configured internal RAG knowledge base.",
                permission=orchestrator_pb2.AUTO_ALLOW,
                parameters={
                    "type": "object",
                    "properties": {
                        "query": {"type": "string"},
                        "top_k": {"type": "integer", "minimum": 1, "maximum": 50},
                        "mode": {
                            "type": "string",
                            "enum": ["hybrid", "bm25", "vector"],
                        },
                        "disable_rerank": {"type": "boolean"},
                    },
                    "required": ["query"],
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
                name="NotebookEdit",
                description=(
                    "Edit a Jupyter notebook (.ipynb) at the cell level: insert, "
                    "replace, or delete a cell identified by cell_id or cell_index."
                ),
                permission=orchestrator_pb2.ASK_SESSION,
                parameters={
                    "type": "object",
                    "properties": {
                        "path": {
                            "type": "string",
                            "description": "Workspace .ipynb file path.",
                        },
                        "cell_id": {
                            "type": "string",
                            "description": (
                                "ID of the target cell. For insert, the new cell "
                                "is inserted after this cell."
                            ),
                        },
                        "cell_index": {
                            "type": "integer",
                            "minimum": 0,
                            "description": "Zero-based fallback when cell_id is absent.",
                        },
                        "cell_type": {
                            "type": "string",
                            "enum": ["code", "markdown", "raw"],
                            "description": "Cell type for insert/replace (default code).",
                        },
                        "edit_mode": {
                            "type": "string",
                            "enum": ["insert", "replace", "delete"],
                            "description": "Edit mode (default replace).",
                        },
                        "source": {
                            "type": "string",
                            "description": "New cell source text for insert/replace.",
                        },
                    },
                    "required": ["path", "edit_mode"],
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
                        "cwd": {"type": "string", "description": "Workspace-relative working directory."},
                        "timeout_seconds": {
                            "type": "number",
                            "minimum": 1,
                            "maximum": 300,
                            "description": "Command timeout in seconds.",
                        },
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
                name="RunWorkflow",
                description="Run a resumable multi-worker workflow with provider selection and dependencies.",
                permission=orchestrator_pb2.AUTO_ALLOW,
                parameters={
                    "type": "object",
                    "properties": {
                        "id": {"type": "string", "description": "Stable workflow identifier used for checkpoint recovery."},
                        "workers": {
                            "type": "array",
                            "items": {
                                "type": "object",
                                "properties": {
                                    "id": {"type": "string"},
                                    "title": {"type": "string"},
                                    "objective": {"type": "string"},
                                    "provider": {"type": "string", "description": "Provider name such as openai, anthropic, local, or default."},
                                    "depends_on": {"type": "array", "items": {"type": "string"}},
                                    "context": {"type": "object"},
                                },
                                "required": ["id", "title", "objective"],
                            },
                        },
                    },
                    "required": ["id", "workers"],
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
            ToolSpec(
                name="Skill",
                description=(
                    "Invoke a registered skill by name. Returns the skill prompt "
                    "and instructions to be injected into the conversation."
                ),
                permission=orchestrator_pb2.AUTO_ALLOW,
                parameters={
                    "type": "object",
                    "properties": {
                        "name": {
                            "type": "string",
                            "description": "Exact name of the skill to invoke.",
                        },
                        "args": {
                            "type": "string",
                            "description": "Optional arguments or focus to pass to the skill.",
                        },
                    },
                    "required": ["name"],
                },
            ),
        ]
