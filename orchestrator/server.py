from __future__ import annotations

import argparse
import os
import threading
from concurrent import futures
from dataclasses import dataclass
from pathlib import Path

import grpc

from codeagent import orchestrator_pb2, orchestrator_pb2_grpc

from .config import configure_otel, load_dotenv, read_env
from .context import LayeredContext, TokenBudget, build_context_store
from .context.compaction import LLMCompactionSummarizer
from .graph.main_graph import build_graph
from .identity import ActorIdentity, ActorIdentityError, ActorSessionRegistry
from .llm.providers import (
    AnthropicClient,
    LocalClient,
    OpenAIClient,
    build_default_client,
    build_fast_client,
    build_provider_router,
)
from .memory.manager import MemoryManager
from .runtime import (
    AgentLoopPluginRegistry,
    CommandRegistry,
    ConversationRunner,
    ExtensionRegistry,
    HookRegistry,
    ToolRegistry,
)
from .runtime.session_ops import (
    SessionOperationBusy,
    SessionOperationCancelled,
    SessionOperationCoordinator,
)
from .skills.manager import SkillManager
from .todo.manager import TodoManager

MAX_GRPC_MESSAGE_BYTES = 32 * 1024 * 1024


@dataclass(slots=True)
class ServerConfig:
    host: str = "127.0.0.1"
    port: int = 50051
    memory_dir: str = ".agent/memory"
    project_root: str = "."
    working_dir: str = "."
    max_tokens: int = 1_000_000
    max_cost: float = 5.0
    context_window: int = 256_000
    # Empty means use CODE_AGENT_CONTEXT_BACKEND/CONTEXT_BACKEND, then SQLite.
    # Remote URLs and DSNs are intentionally not part of this config object.
    context_backend: str = ""


class OrchestratorServer:
    def __init__(self, config: ServerConfig | None = None) -> None:
        load_dotenv()
        from .config.provider_file import apply_provider_file
        if read_env('CODE_AGENT_PROVIDER_CONFIG'):
            apply_provider_file(read_env('CODE_AGENT_PROVIDER_CONFIG'), read_env('CODE_AGENT_PROVIDER_PROFILE'))
        self.config = config or ServerConfig()
        self._otel_shutdown = configure_otel()
        self.project_root = str(Path(self.config.project_root).resolve())
        self.working_dir = str(Path(self.config.working_dir).resolve())
        checkpoint_path = Path(self.project_root) / ".agent" / "checkpoints.sqlite"
        self.graph = build_graph(checkpoint_path=checkpoint_path)
        self.llm = build_default_client()
        self.provider_clients = _build_provider_clients(self.llm)
        self.provider_router = build_provider_router(self.provider_clients)
        self.loop_plugins = AgentLoopPluginRegistry()
        self.hooks = HookRegistry()
        self.commands = CommandRegistry()
        self.fast_llm = build_fast_client()
        self.tools = ToolRegistry(self.project_root)
        self.todos = TodoManager()
        self.memory = MemoryManager(self.config.memory_dir)
        self.context_store = build_context_store(
            self.config.context_backend,
            project_root=self.project_root,
        )
        self.layered_context = LayeredContext(self.context_store, self.project_root)
        self.skills = SkillManager(self.project_root)
        self.extensions = ExtensionRegistry.from_manifest(
            Path(self.project_root) / ".agent" / "extensions.json"
        )
        self.actor_registry = ActorSessionRegistry()
        self.session_operations = SessionOperationCoordinator()
        self.token_budget = TokenBudget(
            max_tokens=self.config.max_tokens,
            max_cost=self.config.max_cost,
        )
        self._close_lock = threading.Lock()
        self._closed = False

    def close(self) -> None:
        with self._close_lock:
            if self._closed:
                return
            self._closed = True
            self.graph.close()
            self.context_store.close()
            self._otel_shutdown()

    def serve(self) -> None:
        server = create_grpc_server(self)
        address = f"{self.config.host}:{self.config.port}"
        server.add_insecure_port(address)
        server.start()
        print(
            f"[orchestrator] gRPC server skeleton on "
            f"{address}"
        )
        print(f"[orchestrator] graph: {self.graph.name}")
        try:
            server.wait_for_termination()
        except KeyboardInterrupt:
            server.stop(grace=1)
        finally:
            self.close()


class OrchestratorService(orchestrator_pb2_grpc.OrchestratorServicer):
    def __init__(self, app: OrchestratorServer) -> None:
        self.app = app

    def Health(self, request, context):
        return orchestrator_pb2.HealthResponse(
            status="ok",
            version="0.1.0",
        )

    def _authorize_actor(self, actor_wire, session_id: str, context) -> ActorIdentity | None:
        # Keep the pre-Actor API usable for ephemeral, session-less callers.
        # Durable sessions must always carry an explicit identity; this is the
        # one-way compatibility adapter for older integrations.
        if actor_wire is None and not str(session_id).strip():
            return None
        try:
            actor = ActorIdentity.from_proto(actor_wire)
            self.app.actor_registry.authorize(session_id, actor)
            return actor
        except ActorIdentityError as exc:
            code = grpc.StatusCode.PERMISSION_DENIED
            if "required" in str(exc) or "unsupported" in str(exc):
                code = grpc.StatusCode.UNAUTHENTICATED
            context.abort(code, str(exc))
        raise AssertionError("context.abort must terminate the RPC")

    @staticmethod
    def _continuation_metadata(context) -> tuple[str, bool, str, str, tuple[str, ...]]:
        """Read the per-RPC continuation identity from the Harness seam."""

        values = {
            str(key).lower(): value
            for key, value in (context.invocation_metadata() or ())
        }
        raw_run_id = values.get("x-code-agent-run-id", "")
        if isinstance(raw_run_id, bytes):
            raw_run_id = raw_run_id.decode("utf-8", errors="replace")
        run_id = str(raw_run_id).strip()
        raw_resume = values.get("x-code-agent-resume", "")
        if isinstance(raw_resume, bytes):
            raw_resume = raw_resume.decode("utf-8", errors="replace")
        resume = str(raw_resume).strip().lower() in {"1", "true", "yes", "on"}
        raw_surface_sha256 = values.get("x-code-agent-surface-sha256", "")
        if isinstance(raw_surface_sha256, bytes):
            raw_surface_sha256 = raw_surface_sha256.decode("utf-8", errors="replace")
        surface_sha256 = str(raw_surface_sha256).strip()
        raw_retry_of_run_id = values.get("x-code-agent-retry-of-run-id", "")
        if isinstance(raw_retry_of_run_id, bytes):
            raw_retry_of_run_id = raw_retry_of_run_id.decode("utf-8", errors="replace")
        retry_of_run_id = str(raw_retry_of_run_id).strip()
        if resume and not run_id:
            context.abort(
                grpc.StatusCode.INVALID_ARGUMENT,
                "continuation run id is required when resume is true",
            )
        if resume and not surface_sha256:
            context.abort(
                grpc.StatusCode.INVALID_ARGUMENT,
                "continuation surface sha256 is required when resume is true",
            )
        if len(run_id) > 256:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, "continuation run id is too long")
        if len(surface_sha256) > 256:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, "continuation surface sha256 is too long")
        if len(retry_of_run_id) > 256:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, "continuation retry run id is too long")
        if retry_of_run_id and not resume:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, "continuation retry run id requires resume")
        raw_retry_of_run_ids = values.get("x-code-agent-retry-of-run-ids", "")
        if isinstance(raw_retry_of_run_ids, bytes):
            raw_retry_of_run_ids = raw_retry_of_run_ids.decode("utf-8", errors="replace")
        retry_of_run_ids: list[str] = []
        for raw_id in str(raw_retry_of_run_ids).split(","):
            candidate = raw_id.strip()
            if not candidate or candidate in retry_of_run_ids:
                continue
            if len(candidate) > 256:
                context.abort(grpc.StatusCode.INVALID_ARGUMENT, "continuation retry run id is too long")
            retry_of_run_ids.append(candidate)
        if retry_of_run_id and retry_of_run_id not in retry_of_run_ids:
            retry_of_run_ids.insert(0, retry_of_run_id)
        if retry_of_run_ids and not resume:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, "continuation retry run id requires resume")
        return run_id, resume, surface_sha256, retry_of_run_id, tuple(retry_of_run_ids)

    def _new_runner(self) -> ConversationRunner:
        # Auxiliary summaries use the configured provider but never inherit
        # executable tools. This keeps compaction on the same model boundary
        # as normal turns while making the path explicit in production.
        compaction_summarizer = None
        if self.app.llm is not None:
            compaction_summarizer = LLMCompactionSummarizer(
                client=self.app.llm,
                provider="default",
                model=str(getattr(self.app.llm, "model", "")),
            )
        return ConversationRunner(
            graph=self.app.graph,
            llm=self.app.llm,
            tool_registry=self.app.tools,
            todo_manager=self.app.todos,
            memory_manager=self.app.memory,
            skills=self.app.skills,
            project_root=self.app.project_root,
            working_dir=self.app.working_dir,
            token_budget=self.app.token_budget,
            fast_llm=self.app.fast_llm,
            main_llm=self.app.llm,
            provider_clients=self._provider_clients_for_request(),
            layered_context=self.app.layered_context,
            context_window=self.app.config.context_window,
            loop_plugins=self.app.loop_plugins,
            provider_router=self._provider_router_for_request(),
            hooks=self.app.hooks,
            commands=self.app.commands,
            extensions=self.app.extensions,
            compaction_summarizer=compaction_summarizer,
            require_harness_worktree=os.getenv("CODE_AGENT_REQUIRE_HARNESS_WORKTREE", "").strip().lower()
            in {"1", "true", "yes", "on"},
        )

    def Compact(self, request, context):
        actor = self._authorize_actor(
            request.actor if request.HasField("actor") else None,
            request.session_id,
            context,
        )
        history = [
            {
                "role": item.role,
                "content": item.content,
                "created_at": item.created_at,
                "schema_version": item.schema_version,
                "name": item.name,
                "tool_call_id": item.tool_call_id,
                "tool_calls": [
                    {
                        "id": call.id,
                        "name": call.name,
                        "arguments_json": call.arguments_json,
                    }
                    for call in item.tool_calls
                ],
                "is_error": item.is_error,
            }
            for item in request.history
        ]
        try:
            lease = self.app.session_operations.try_acquire_compact(request.session_id)
        except SessionOperationBusy:
            context.abort(grpc.StatusCode.FAILED_PRECONDITION, "session is busy")
        try:
            runner = self._new_runner()
            update = runner.compact_now(
                session_id=request.session_id,
                history=history,
                actor=actor,
            )
        except Exception as exc:
            context.abort(
                grpc.StatusCode.INTERNAL,
                f"compaction failed: {type(exc).__name__}",
            )
        finally:
            lease.release()
        return update or orchestrator_pb2.CompactionUpdate()

    def Converse(self, request_iterator, context):
        run_id, resume, surface_sha256, retry_of_run_id, retry_of_run_ids = self._continuation_metadata(context)
        user_text = ""
        session_id = ""
        actor = None
        history: list[dict[str, str]] = []
        plan_todo_snapshot = None
        for message in request_iterator:
            payload = message.WhichOneof("payload")
            if payload == "user_input":
                user_input = message.user_input
                user_text = user_input.text
                session_id = user_input.session_id
                actor = self._authorize_actor(
                    user_input.actor if user_input.HasField("actor") else None,
                    session_id,
                    context,
                )
                if user_input.HasField("plan_todo_state"):
                    plan_todo_snapshot = self._snapshot_from_proto(user_input.plan_todo_state)
                history = [
                    {
                        "role": item.role,
                        "content": item.content,
                        "created_at": item.created_at,
                        "schema_version": item.schema_version,
                        "name": item.name,
                        "tool_call_id": item.tool_call_id,
                        "tool_calls": [
                            {"id": call.id, "name": call.name, "arguments_json": call.arguments_json}
                            for call in item.tool_calls
                        ],
                        "is_error": item.is_error,
                    }
                    for item in user_input.history
                ]
                break

        if actor is None and session_id.strip():
            context.abort(grpc.StatusCode.UNAUTHENTICATED, "user input with actor context is required")

        cancel_event = threading.Event()
        context.add_callback(cancel_event.set)
        try:
            lease = self.app.session_operations.acquire_converse(
                session_id,
                cancel_event,
            )
        except SessionOperationCancelled:
            context.abort(
                grpc.StatusCode.CANCELLED,
                "conversation canceled while waiting for session",
            )

        # ── W3C TraceContext recovery ─────────────────────────────────
        # The Go harness injects the active span context via gRPC metadata
        # (per-RPC, not per-process) so the gen_ai inference spans inside
        # the runner become correct children of the Go invoke_agent span.
        otel_token = None
        try:
            md = dict(context.invocation_metadata()) if context.invocation_metadata() else {}
            traceparent = md.get("traceparent", "").strip()
            if traceparent:
                from opentelemetry import context as otel_context
                from opentelemetry.trace.propagation.tracecontext import (
                    TraceContextTextMapPropagator,
                )

                propagator = TraceContextTextMapPropagator()
                parent_ctx = propagator.extract(carrier={"traceparent": traceparent})
                otel_token = otel_context.attach(parent_ctx)
        except Exception:
            pass

        try:
            runner = self._new_runner()
            runner_actor = actor
            yield from runner.run(
                user_text,
                request_iterator,
                session_id=session_id,
                history=history,
                cancel_event=cancel_event,
                plan_todo_snapshot=plan_todo_snapshot,
                actor=runner_actor,
                run_id=run_id,
                resume=resume,
                surface_sha256=surface_sha256,
                retry_of_run_id=retry_of_run_id,
                retry_of_run_ids=retry_of_run_ids,
            )
        finally:
            lease.release()
            # Detach the TraceContext parent so following calls on this
            # thread do not inherit it.
            if otel_token is not None:
                try:
                    from opentelemetry import context as _otel_context
                    _otel_context.detach(otel_token)
                except Exception:
                    pass

    def _provider_clients_for_request(self):
        clients = dict(self.app.provider_clients)
        if self.app.llm is not None:
            clients["default"] = self.app.llm
            configured = read_env("LLM_PROVIDER").lower()
            if configured in {"openai", "anthropic", "local"}:
                clients[configured] = self.app.llm
        return clients

    def _provider_router_for_request(self):
        """Snapshot current clients into one request-scoped route directory."""
        return build_provider_router(self._provider_clients_for_request())

    @staticmethod
    def _snapshot_from_proto(value) -> dict[str, object]:
        """Convert the wire snapshot to the runner's validated mapping shape."""
        plan = value.plan if value.HasField("plan") else None
        return {
            "schema_version": int(value.schema_version),
            "revision": int(value.revision),
            "plan": {
                "steps": list(plan.steps) if plan is not None else [],
                "current_index": int(plan.current_index) if plan is not None else 0,
                "mode": plan.mode if plan is not None else "chat",
            },
            "todos": [
                {
                    "content": item.content,
                    "active_form": item.active_form,
                    "status": item.status,
                }
                for item in value.todos
            ],
        }


def _build_provider_clients(default_client):
    clients = {}
    configured = read_env("LLM_PROVIDER").lower()
    if default_client is not None:
        clients["default"] = default_client
        provider_alias = configured if configured in {"openai", "anthropic", "local"} else _infer_provider_alias(default_client)
        if provider_alias:
            clients[provider_alias] = default_client
    for name, factory in (
        ("openai", OpenAIClient.from_env),
        ("anthropic", AnthropicClient.from_env),
        ("local", LocalClient.from_env),
    ):
        client = factory()
        if client is not None:
            clients.setdefault(name, client)
    return clients


def _infer_provider_alias(client) -> str:
    """Map a concrete adapter to its canonical provider route name."""
    if isinstance(client, LocalClient):
        return "local"
    if isinstance(client, AnthropicClient):
        return "anthropic"
    if isinstance(client, OpenAIClient):
        return "openai"
    return ""


class _ManagedGrpcServer:
    """Delegate to gRPC while tying application resources to server.stop()."""

    def __init__(self, server: grpc.Server, app: OrchestratorServer) -> None:
        self._server = server
        self._app = app

    def __getattr__(self, name):
        return getattr(self._server, name)

    def stop(self, grace):
        stopped = self._server.stop(grace)
        stopped.wait()
        self._app.close()
        return stopped


def create_grpc_server(app: OrchestratorServer | None = None) -> grpc.Server:
    managed_app = app or OrchestratorServer()
    server = grpc.server(
        futures.ThreadPoolExecutor(max_workers=10),
        options=[
            ("grpc.max_receive_message_length", MAX_GRPC_MESSAGE_BYTES),
            ("grpc.max_send_message_length", MAX_GRPC_MESSAGE_BYTES),
        ],
    )
    orchestrator_pb2_grpc.add_OrchestratorServicer_to_server(
        OrchestratorService(managed_app),
        server,
    )
    return _ManagedGrpcServer(server, managed_app)


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="orchestrator")
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=50051)
    parser.add_argument("--memory-dir", default=".agent/memory")
    parser.add_argument("--project-root", default=".")
    parser.add_argument("--working-dir", default=".")
    parser.add_argument("--max-tokens", type=int, default=1_000_000)
    parser.add_argument("--max-cost", type=float, default=5.0)
    parser.add_argument("--context-window", type=int, default=256_000)
    parser.add_argument(
        "--context-backend",
        choices=("sqlite", "redis", "mysql"),
        default="",
        help="context persistence backend; remote backends require their environment URL/DSN",
    )
    return parser


def main(argv: list[str] | None = None) -> None:
    args = build_parser().parse_args(argv)
    server = OrchestratorServer(
        ServerConfig(
            host=args.host,
            port=args.port,
            memory_dir=args.memory_dir,
            project_root=args.project_root,
            working_dir=args.working_dir,
            max_tokens=args.max_tokens,
            max_cost=args.max_cost,
            context_window=args.context_window,
            context_backend=args.context_backend,
        )
    )
    server.serve()


if __name__ == "__main__":
    main()
