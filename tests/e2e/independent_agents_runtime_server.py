from __future__ import annotations

import argparse
import json
import os
import sys
import threading
import time
from pathlib import Path

import orchestrator.server as server_module
from orchestrator.llm.client import (
    ChatRequest,
    ChatResponse,
    LLMClient,
    ToolCall,
    Usage,
    message_content_text,
)
from orchestrator.llm.providers import (
    AnthropicClient,
    OpenAIClient,
    build_provider_router,
)
from orchestrator.server import OrchestratorServer, ServerConfig, create_grpc_server

_IDENTITY_KEYS = (
    "endpoint_host",
    "requested_model",
    "reported_model",
    "response_id",
    "system_fingerprint",
    "created",
    "identity_verified",
)
_MCP_ECHO_TOOL = "e2e_echo"


def _run_mcp_stdio() -> None:
    """Serve the one deterministic MCP tool used by the provider E2E lane."""

    for line in sys.stdin:
        try:
            request = json.loads(line)
        except json.JSONDecodeError:
            continue
        if not isinstance(request, dict) or "id" not in request:
            continue
        method = request.get("method")
        if method == "initialize":
            result = {
                "protocolVersion": "2024-11-05",
                "capabilities": {"tools": {}},
                "serverInfo": {"name": "independent-agent-e2e", "version": "1"},
            }
        elif method == "tools/list":
            result = {
                "tools": [
                    {
                        "name": _MCP_ECHO_TOOL,
                        "description": "Echo a marker for the independent-agent E2E.",
                        "inputSchema": {
                            "type": "object",
                            "properties": {"text": {"type": "string"}},
                            "required": ["text"],
                            "additionalProperties": False,
                        },
                    }
                ]
            }
        elif method == "tools/call":
            params = request.get("params")
            params = params if isinstance(params, dict) else {}
            arguments = params.get("arguments")
            arguments = arguments if isinstance(arguments, dict) else {}
            if params.get("name") != _MCP_ECHO_TOOL or not isinstance(
                arguments.get("text"), str
            ):
                response = {
                    "jsonrpc": "2.0",
                    "id": request["id"],
                    "error": {"code": -32602, "message": "invalid e2e_echo arguments"},
                }
                print(json.dumps(response, sort_keys=True), flush=True)
                continue
            result = {
                "content": [{"type": "text", "text": f"e2e_echo: {arguments['text']}"}]
            }
        else:
            response = {
                "jsonrpc": "2.0",
                "id": request["id"],
                "error": {"code": -32601, "message": "method not found"},
            }
            print(json.dumps(response, sort_keys=True), flush=True)
            continue
        print(
            json.dumps(
                {"jsonrpc": "2.0", "id": request["id"], "result": result},
                sort_keys=True,
            ),
            flush=True,
        )


class IndependentAgentModel:
    """Deterministic model double, not a provider or benchmark scorer."""

    model = "independent-agent-fixture"

    def __init__(self, observations: Path) -> None:
        self.observations = observations
        self.lock = threading.Lock()

    def record(self, **values) -> None:
        with self.lock, self.observations.open("a", encoding="utf-8") as stream:
            stream.write(json.dumps(values, sort_keys=True) + "\n")

    @staticmethod
    def call(tool_name: str, call_id: str, **arguments) -> ChatResponse:
        return ChatResponse(
            tool_calls=[
                ToolCall(
                    name=tool_name,
                    id=call_id,
                    arguments=arguments,
                    arguments_json=json.dumps(arguments, sort_keys=True),
                )
            ],
            usage=Usage(input_tokens=1, output_tokens=1),
        )

    @staticmethod
    def output(message):
        text = message_content_text(message.content)
        if "[Untrusted tool output]\n" in text:
            text = text.partition("[Untrusted tool output]\n")[2]
        return json.loads(text)

    async def chat(self, request):
        if request.purpose == "memory_reflection":
            assert request.tools == [] and not request.allow_tools
            sources = json.loads(message_content_text(request.messages[-1].content))
            self.record(reflection_without_tools=True)
            return ChatResponse(
                text=json.dumps(
                    {
                        "candidates": [
                            {
                                "kind": "patterns",
                                "key": "agents/independent-history",
                                "abstract": "Keep independent agent history recoverable",
                                "overview": "Keep independent agent history and provenance recoverable across process restart.",
                                "content": "Independent task history is recovered from its child Session Ledger, without parent history.",
                                "source_event_ids": [sources[-1]["event_id"]],
                            }
                        ]
                    }
                ),
                usage=Usage(input_tokens=1, output_tokens=1),
            )
        rendered = "\n".join(message_content_text(m.content) for m in request.messages)
        user_index = max(i for i, m in enumerate(request.messages) if m.role == "user")
        latest_user = message_content_text(request.messages[user_index].content)
        tool_messages = [
            m for m in request.messages[user_index + 1 :] if m.role == "tool"
        ]
        is_child = "You are an independent child agent." in rendered
        if is_child:
            child_context_isolated = (
                "PARENT_PRIVATE_HISTORY" not in rendered
                and "PARENT_FILE_MEMORY" not in rendered
                and "EXPLICIT_CHILD_MATERIAL" in rendered
            )
            spawn_agent_excluded = not any(
                t["function"]["name"] == "SpawnAgent" for t in request.tools
            )
            self.record(
                child_context_isolated=child_context_isolated,
                parent_private_absent="PARENT_PRIVATE_HISTORY" not in rendered,
                parent_file_absent="PARENT_FILE_MEMORY" not in rendered,
                explicit_material_present="EXPLICIT_CHILD_MATERIAL" in rendered,
                spawn_agent_excluded=spawn_agent_excluded,
                child_tool_results=[m.name for m in tool_messages],
            )
            assert child_context_isolated
            assert spawn_agent_excluded
            resumed = "Continue after process restart" in latest_user
            self.record(restart_history=resumed and "Use branch main" in rendered)
            if "Use branch main" not in rendered:
                return self.call(
                    "AskUser", "child-ask", question="Which branch?", options=["main"]
                )
            if resumed and not any(m.name == "RecallMemory" for m in tool_messages):
                return self.call(
                    "RecallMemory",
                    "child-recall",
                    query="independent agent history recoverable",
                    detail="overview",
                )
            if resumed:
                recalled = next(
                    (m for m in tool_messages if m.name == "RecallMemory"), None
                )
                recalled_text = (
                    message_content_text(recalled.content)
                    if recalled is not None
                    else ""
                )
                self.record(
                    memory_recalled_after_restart=(
                        recalled is not None
                        and "EXPLICIT_CHILD_MATERIAL" in recalled_text
                        and "PARENT_PRIVATE_HISTORY" not in recalled_text
                    )
                )
            if not any(m.name == "Skill" for m in tool_messages):
                assert "SKILL_BODY_LAZY_MARKER" not in str(request.tools)
                self.record(skill_metadata_only=True)
                return self.call("Skill", "child-skill", name="case-inspect")
            self.record(skill_body_loaded="SKILL_BODY_LAZY_MARKER" in rendered)
            if not any(m.name == "PublishArtifact" for m in tool_messages):
                assert "SKILL_BODY_LAZY_MARKER" in rendered
                return self.call(
                    "PublishArtifact",
                    "child-file",
                    artifact={
                        "name": "Independent source report",
                        "parts": [
                            {
                                "file": {
                                    "path": "report.txt",
                                    "media_type": "text/plain",
                                }
                            },
                            {
                                "data_json": json.dumps(
                                    {"branch": "main", "restarted": resumed}
                                )
                            },
                        ],
                    },
                )
            artifact = self.output(
                next(m for m in reversed(tool_messages) if m.name == "PublishArtifact")
            )
            assert (
                len(artifact["checksum"]) == 64
                and artifact["parts"][0]["file"]["sha256"]
            )
            self.record(file_artifact_pinned=True)
            return ChatResponse(
                text="CHILD_INDEPENDENT_REPORT",
                usage=Usage(input_tokens=1, output_tokens=1),
            )
        if "RESUME_AGENT_CASE" in latest_user:
            if not tool_messages:
                return self.call("AgentTask", "parent-list-restart", action="list")
            if tool_messages[-1].tool_call_id == "parent-list-restart":
                task = self.output(tool_messages[-1])[0]
                self.record(task_id_after_restart=task["id"])
                return self.call(
                    "AgentTask",
                    "parent-message-restart",
                    action="message",
                    task_id=task["id"],
                    message={"parts": [{"text": "Continue after process restart"}]},
                )
        elif not tool_messages:
            return self.call(
                "SpawnAgent",
                "parent-spawn",
                kind="explore",
                title="Inspect sources",
                objective="Inspect explicit sources; ask which branch to use.",
                context={"material": "EXPLICIT_CHILD_MATERIAL"},
                message={
                    "parts": [
                        {"file": {"path": "report.txt", "media_type": "text/plain"}}
                    ]
                },
            )
        task = self.output(tool_messages[-1])
        if task["status"] == "input_required":
            self.record(task_input_required=True)
            return self.call(
                "AgentTask",
                "parent-message",
                action="message",
                task_id=task["id"],
                message={
                    "parts": [
                        {"text": "Use branch main"},
                        {"data_json": json.dumps({"scope": "source"})},
                    ]
                },
            )
        if task["status"] != "completed":
            assert task["status"] not in {"failed", "canceled"}
            wait_call_id = (
                "parent-wait-restart"
                if "RESUME_AGENT_CASE" in latest_user
                else "parent-wait-" + str(len(tool_messages))
            )
            return self.call(
                "AgentTask", wait_call_id, action="wait", task_id=task["id"]
            )
        assert task["artifacts"] and task["working_dir"]
        self.record(completed_task_id=task["id"])
        return ChatResponse(
            text="PARENT_DELEGATION_COMPLETED",
            usage=Usage(input_tokens=1, output_tokens=1),
        )


class ObservedProviderModel(LLMClient):
    """Observe bounded request properties while delegating to a real provider."""

    def __init__(self, delegate: LLMClient, observations: Path) -> None:
        self.delegate = delegate
        self.model = str(getattr(delegate, "model", ""))
        self.observations = observations
        self.lock = threading.Lock()
        self.local = threading.local()

    def record(self, **values) -> None:
        with (
            self.lock,
            self.observations.open("a", encoding="utf-8", newline="\n") as stream,
        ):
            stream.write(json.dumps(values, sort_keys=True) + "\n")

    def _observe_request(self, request: ChatRequest) -> None:
        self.local.model_identity = {}
        rendered = "\n".join(
            message_content_text(message.content) for message in request.messages
        )
        recalled_history = any(
            message.role == "tool"
            and message.name == "RecallMemory"
            and "EXPLICIT_CHILD_MATERIAL" in message_content_text(message.content)
            and "PARENT_PRIVATE_HISTORY" not in message_content_text(message.content)
            for message in request.messages
        )
        if request.purpose == "memory_reflection":
            self.record(
                reflection_without_tools=(
                    request.tools == [] and not request.allow_tools
                )
            )
        if "You are an independent child agent." not in rendered:
            return
        self.record(
            child_context_isolated=(
                "PARENT_PRIVATE_HISTORY" not in rendered
                and "PARENT_FILE_MEMORY" not in rendered
                and "EXPLICIT_CHILD_MATERIAL" in rendered
            ),
            skill_metadata_only=("SKILL_BODY_LAZY_MARKER" not in str(request.tools)),
            skill_body_loaded=("SKILL_BODY_LAZY_MARKER" in rendered),
            restart_history=(
                "Continue after process restart" in rendered
                and "Use branch main" in rendered
            ),
            memory_recalled_after_restart=(
                "Continue after process restart" in rendered and recalled_history
            ),
        )

    async def chat(self, request: ChatRequest) -> ChatResponse:
        self._observe_request(request)
        response = await self.delegate.chat(request)
        self.local.model_identity = dict(response.model_identity)
        return response

    def take_model_identity(self) -> dict[str, object]:
        identity = _safe_identity(getattr(self.local, "model_identity", {}))
        self.local.model_identity = {}
        return identity


def _provider_model(mode: str, observations: Path) -> LLMClient:
    if mode == "openai":
        client = OpenAIClient.from_env()
    elif mode == "anthropic":
        client = AnthropicClient.from_env()
    else:
        raise ValueError("provider mode must be openai or anthropic")
    if client is None:
        raise RuntimeError("selected provider credential is unavailable")
    return ObservedProviderModel(client, observations)


def _safe_identity(value) -> dict[str, object]:
    source = value if isinstance(value, dict) else {}
    return {key: source[key] for key in _IDENTITY_KEYS if key in source}


def main() -> None:
    if sys.argv[1:] == ["--mcp-stdio"]:
        _run_mcp_stdio()
        return
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, required=True)
    parser.add_argument("--project-root", type=Path, required=True)
    parser.add_argument("--observations", type=Path, required=True)
    parser.add_argument("--stop-file", type=Path, required=True)
    args = parser.parse_args()

    mode = os.environ.get("CODE_AGENT_E2E_PROVIDER", "").strip().lower()
    provider_backed = mode in {"openai", "anthropic"}
    # This process accepts credentials only from its explicitly allowlisted
    # environment. Local dotenv and provider-profile files are never consulted.
    server_module.load_dotenv = lambda: None
    server_module.build_default_client = lambda: None
    server_module.build_fast_client = lambda: None
    server_module._build_provider_clients = lambda _default: {}
    os.environ.pop("CODE_AGENT_PROVIDER_CONFIG", None)
    os.environ.pop("CODE_AGENT_PROVIDER_PROFILE", None)
    root = args.project_root.resolve()
    app = OrchestratorServer(
        ServerConfig(
            port=args.port,
            project_root=str(root),
            working_dir=str(root),
            memory_dir=str(root / ".agent" / "fixture-memory"),
        )
    )
    model: LLMClient
    if provider_backed:
        model = _provider_model(mode, args.observations)
    else:
        model = IndependentAgentModel(args.observations)
    app.llm = model
    app.fast_llm = model
    route_name = mode if provider_backed else "fixture"
    app.provider_clients = {route_name: model}
    app.provider_router = build_provider_router(app.provider_clients)

    route_receipt = os.environ.get(
        "CODE_AGENT_INDEPENDENT_AGENTS_E2E_PROVIDER_ROUTE_RECEIPT", ""
    ).strip()
    eval_run_id = os.environ.get("CODE_AGENT_EVAL_RUN_ID", "").strip()
    if provider_backed:
        if not route_receipt or not eval_run_id:
            raise RuntimeError("provider lane requires route receipt and eval run id")
        route_path = Path(route_receipt).resolve()
        route_lock = threading.Lock()

        def record_route(event) -> None:
            if event.phase != "model_after":
                return
            metadata = event.metadata
            identity = _safe_identity(metadata.get("model_identity"))
            if not identity and isinstance(model, ObservedProviderModel):
                identity = model.take_model_identity()
            payload = {
                "schema_version": 1,
                "phase": event.phase,
                "eval_run_id": eval_run_id,
                "session_id": event.session_id,
                "turn": event.turn,
                "provider_backed": True,
                "provider": str(metadata.get("provider", "")),
                "model": str(metadata.get("model", "")),
                "generation": metadata.get("generation"),
                "model_identity": identity,
            }
            with (
                route_lock,
                route_path.open("a", encoding="utf-8", newline="\n") as handle,
            ):
                handle.write(json.dumps(payload, sort_keys=True) + "\n")

        app.loop_plugins.register("independent-agent-route-recorder", record_route)

    grpc_server = create_grpc_server(app)
    grpc_server.add_insecure_port(f"127.0.0.1:{args.port}")
    grpc_server.start()
    try:
        while not args.stop_file.exists():
            time.sleep(0.1)
    except KeyboardInterrupt:
        pass
    finally:
        # Managed stop closes the app and flushes the OTel provider.
        grpc_server.stop(grace=1)


if __name__ == "__main__":
    main()
