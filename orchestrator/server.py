from __future__ import annotations

import argparse
import copy
import json
import logging
import os
import re
import threading
from collections import OrderedDict
from concurrent import futures
from dataclasses import dataclass
from pathlib import Path

import grpc
from google.protobuf.json_format import MessageToDict

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
from .llm.gateway import HarnessModelClient, ModelGatewayError
from .memory.manager import MemoryManager
from .memory.reflection import reflect_memory
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
_CONTEXT_ENVELOPE_VERSION = 1
_CONTEXT_SECTION_BYTES = (12_000, 8_000, 8_000, 3_000)
_EVAL_JOIN_ID = re.compile(r"[A-Za-z0-9][A-Za-z0-9._:-]{0,95}")


def _safe_rpc_status(exc: BaseException) -> str:
    """Return only a known gRPC status name, never exception text."""
    if not isinstance(exc, grpc.RpcError):
        return ""
    try:
        status = exc.code()
    except Exception:
        return ""
    return status.name if isinstance(status, grpc.StatusCode) else ""


def _rpc_failure_details(exc: BaseException) -> tuple[str, str, bool, str]:
    """Classify transport cancellation without exposing RpcError details."""
    if isinstance(exc, ModelGatewayError):
        return exc.code, "model call blocked; restore prerequisites or reconcile retained usage", False, ""
    status = _safe_rpc_status(exc)
    details = {
        "CANCELLED": (
            "rpc_cancelled",
            "orchestrator call canceled; retry this task",
        ),
        "UNAVAILABLE": (
            "rpc_unavailable",
            "orchestrator transport unavailable; retry this task",
        ),
        "DEADLINE_EXCEEDED": (
            "rpc_deadline_exceeded",
            "orchestrator call deadline exceeded; retry this task",
        ),
    }.get(status)
    if details is None:
        return "", "", True, status
    return details[0], details[1], True, status


def _admitted_grpc_trace_context(metadata):
    """Restore W3C parent plus the two safe evaluation baggage keys."""
    try:
        md = dict(metadata) if metadata else {}
        traceparent = md.get("traceparent", "").strip()
        if not traceparent:
            return None
        from opentelemetry import baggage, context as otel_context, trace
        from opentelemetry.baggage.propagation import W3CBaggagePropagator
        from opentelemetry.trace.propagation.tracecontext import (
            TraceContextTextMapPropagator,
        )

        trace_carrier = {"traceparent": traceparent}
        tracestate = md.get("tracestate", "").strip()
        if tracestate:
            trace_carrier["tracestate"] = tracestate
        parent_ctx = TraceContextTextMapPropagator().extract(
            carrier=trace_carrier,
            context=otel_context.Context(),
        )
        span_context = trace.get_current_span(parent_ctx).get_span_context()
        if not span_context.is_valid or not span_context.is_remote:
            return None
        baggage_header = md.get("baggage", "").strip()
        if not baggage_header:
            return parent_ctx
        extracted = W3CBaggagePropagator().extract(
            carrier={"baggage": baggage_header}, context=parent_ctx
        )
        for key in ("eval.run_id", "eval.instance_id"):
            value = baggage.get_baggage(key, context=extracted)
            if value is not None and _EVAL_JOIN_ID.fullmatch(str(value)):
                parent_ctx = baggage.set_baggage(key, str(value), context=parent_ctx)
        return parent_ctx
    except Exception:
        return None


def _render_context_envelope(envelope) -> str:
    if envelope.schema_version != _CONTEXT_ENVELOPE_VERSION:
        raise ValueError("unsupported context envelope version")
    p2_candidates = getattr(envelope, "p2_candidates", None)
    if p2_candidates is None:
        p2_candidates = getattr(envelope, "p3_candidates", ())
    if len(envelope.p0) > 256 or len(envelope.p1) > 64 or len(p2_candidates) > 64 or len(envelope.events) > 64:
        raise ValueError("context envelope exceeds item limits")

    def digest(value: str, *, optional: bool = False) -> str:
        value = value.strip().lower()
        if optional and not value:
            return ""
        if len(value) != 64 or any(char not in "0123456789abcdef" for char in value):
            raise ValueError("context envelope contains an invalid checksum")
        return value

    def relative_path(value: str) -> str:
        value = value.strip().replace("\\", "/")
        parts = value.split("/")
        if (
            not value
            or value.startswith("/")
            or len(value) > 1024
            or any(part in {"", ".", ".."} for part in parts)
            or any(char in value for char in "\x00\r\n:")
        ):
            raise ValueError("context envelope contains an invalid path")
        return value

    def file_lines(items) -> tuple[list[str], set[str]]:
        lines: list[str] = []
        paths: set[str] = set()
        for item in items:
            item_path = relative_path(item.path)
            if item_path in paths or item.size_bytes < 0 or item.line_count < 0:
                raise ValueError("context envelope contains an invalid file summary")
            paths.add(item_path)
            sha = digest(item.sha256, optional=True)
            details = f"{item.size_bytes} bytes"
            if sha:
                details += f", {item.line_count} lines, sha256={sha}"
            lines.append(f"- {item_path} ({details})")
        return lines, paths

    p0_lines, _ = file_lines(envelope.p0)
    p1_lines, p1_paths = file_lines(envelope.p1)
    p2_lines: list[str] = []
    p2_seen: set[str] = set()
    for value in p2_candidates:
        item_path = relative_path(value)
        if item_path in p2_seen or item_path not in p1_paths:
            raise ValueError("context envelope contains an invalid P2 candidate")
        p2_seen.add(item_path)
        p2_lines.append(f"- {item_path}")

    event_lines: list[str] = []
    previous_seq = -1
    for event in envelope.events:
        event_type = " ".join(event.type.split())
        detail = " ".join(event.detail.split())
        if event.seq < 0 or event.seq <= previous_seq or not event_type or len(event_type) > 128 or len(detail) > 256:
            raise ValueError("context envelope contains an invalid event summary")
        previous_seq = event.seq
        checksum = digest(event.checksum)
        suffix = f": {detail}" if detail else ""
        event_lines.append(f"- #{event.seq} {event_type}{suffix} [sha256={checksum}]")
    head_checksum = digest(envelope.ledger_checksum) if envelope.events else ""
    if envelope.events and (
        envelope.ledger_seq != envelope.events[-1].seq
        or head_checksum != envelope.events[-1].checksum.lower()
    ):
        raise ValueError("context envelope ledger head does not match events")

    def section(title: str, lines: list[str], maximum: int) -> str:
        kept = [title]
        used = len(title.encode("utf-8"))
        for line in lines:
            size = len(("\n" + line).encode("utf-8"))
            if used + size > maximum:
                kept.append("[additional metadata omitted by context budget]")
                break
            kept.append(line)
            used += size
        return "\n".join(kept)

    return "\n\n".join(
        (
            "Harness context envelope v1 (metadata only; use the authorized Read tool for P2 content).",
            section("P0 directory summary", p0_lines, _CONTEXT_SECTION_BYTES[0]),
            section("P1 node summaries", p1_lines, _CONTEXT_SECTION_BYTES[1]),
            section(
                f"Recent Session Ledger events (head #{envelope.ledger_seq} sha256={head_checksum})",
                event_lines,
                _CONTEXT_SECTION_BYTES[2],
            ),
            section("P2 candidates (raw content not injected)", p2_lines, _CONTEXT_SECTION_BYTES[3]),
            "P3 candidates (raw content not injected) [legacy alias of P2]",
        )
    )


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
        admitted = os.getenv("CODE_AGENT_MODEL_ADMISSION") == "required"
        if not admitted:
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
        self.llm = None if admitted else build_default_client()
        self.provider_clients = {} if admitted else _build_provider_clients(self.llm)
        self.provider_router = build_provider_router(self.provider_clients)
        self.loop_plugins = AgentLoopPluginRegistry()
        self.hooks = HookRegistry()
        self.commands = CommandRegistry()
        self.fast_llm = None if admitted else build_fast_client()
        self.tools = ToolRegistry(self.project_root)
        self.todos = TodoManager()
        self.memory = MemoryManager(self.config.memory_dir)
        self.context_store = build_context_store(
            self.config.context_backend,
            project_root=self.project_root,
        )
        self.layered_context = LayeredContext(self.context_store, self.project_root)
        self.skills = SkillManager(self.project_root)
        self._skill_catalogs: OrderedDict[str, SkillManager] = OrderedDict()
        self._skill_catalog_lock = threading.Lock()
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

    def session_skills(self, root: str) -> SkillManager:
        key = str(Path(root).resolve())
        with self._skill_catalog_lock:
            skills = self._skill_catalogs.pop(key, None) or SkillManager(key)
            user_root = Path.home()
            skills.discover(
                project_dsh_dir=Path(key) / ".dsh" / "skills",
                project_agents_dir=Path(key) / ".agents" / "skills",
                project_dir=Path(key) / ".agent" / "skills",
                user_dsh_dir=user_root / ".dsh" / "skills",
                user_agents_dir=user_root / ".agents" / "skills",
                global_dir=user_root / ".agent" / "skills",
            )
            self._skill_catalogs[key] = skills
            if len(self._skill_catalogs) > 128:
                self._skill_catalogs.popitem(last=False)
            # Bodies loaded by a runner must not mutate another session's view.
            return copy.deepcopy(skills)

    def session_extensions(self, root: str) -> ExtensionRegistry:
        """Resolve extension metadata within the request's project boundary.

        The process-level registry is valid for the configured project root.
        A managed child worktree gets a fresh metadata-only registry so a
        parent extension manifest cannot widen the child's model surface.
        """

        key = str(Path(root).resolve())
        if key == self.project_root:
            return self.extensions
        return ExtensionRegistry.from_manifest(Path(key) / ".agent" / "extensions.json")

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

    def ReflectMemory(self, request, context):
        self._authorize_actor(
            request.actor if request.HasField("actor") else None,
            request.session_id, context,
        )
        cancel = threading.Event()
        context.add_callback(cancel.set)

        if os.getenv("CODE_AGENT_MODEL_ADMISSION") == "required" and not request.HasField("model_gateway"):
            context.abort(grpc.StatusCode.FAILED_PRECONDITION, "model admission binding required")
        client = HarnessModelClient(request.model_gateway) if request.HasField("model_gateway") else self.app.llm
        try:
            return reflect_memory(client, request, cancel)
        finally:
            if isinstance(client, HarnessModelClient):
                client.close()

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

    def _new_runner(
        self,
        working_dir: str | None = None,
        *,
        project_root: str | None = None,
        model_gateway=None,
    ) -> ConversationRunner:
        # Auxiliary summaries use the configured provider but never inherit
        # executable tools. This keeps compaction on the same model boundary
        # as normal turns while making the path explicit in production.
        if os.getenv("CODE_AGENT_MODEL_ADMISSION") == "required" and model_gateway is None:
            raise ModelGatewayError("model_prerequisites_missing")
        client = HarnessModelClient(model_gateway) if model_gateway is not None else self.app.llm
        provider_clients = ({"default": client, model_gateway.protocol: client} if model_gateway is not None else self._provider_clients_for_request())
        compaction_summarizer = None
        if client is not None:
            compaction_summarizer = LLMCompactionSummarizer(
                client=client,
                provider="default",
                model=str(getattr(client, "model", "")),
            )
        runner_root = str(Path(project_root or self.app.project_root).resolve())
        runner_working_dir = (
            str(Path(working_dir).resolve()) if working_dir else self.app.working_dir
        )
        skill_root = working_dir or runner_root
        skills = self.app.session_skills(skill_root)
        return ConversationRunner(
            graph=self.app.graph,
            llm=client,
            tool_registry=ToolRegistry(runner_root, skills=skills),
            todo_manager=self.app.todos,
            memory_manager=self.app.memory,
            skills=skills,
            project_root=runner_root,
            working_dir=runner_working_dir,
            token_budget=self.app.token_budget,
            fast_llm=client if model_gateway is not None else self.app.fast_llm,
            main_llm=client,
            provider_clients=provider_clients,
            layered_context=self.app.layered_context,
            context_window=self.app.config.context_window,
            loop_plugins=self.app.loop_plugins,
            provider_router=build_provider_router(provider_clients) if model_gateway is not None else self._provider_router_for_request(),
            hooks=self.app.hooks,
            commands=self.app.commands,
            extensions=self.app.session_extensions(runner_root),
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
                **({"event_id": item.event_id, "event_checksum": item.event_checksum}
                   if item.event_id or item.event_checksum else {}),
            }
            for item in request.history
        ]
        try:
            lease = self.app.session_operations.try_acquire_compact(request.session_id)
        except SessionOperationBusy:
            context.abort(grpc.StatusCode.FAILED_PRECONDITION, "session is busy")
        runner = None
        try:
            runner = self._new_runner(**({"model_gateway": request.model_gateway} if request.HasField("model_gateway") else {}))
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
            if request.HasField("model_gateway") and runner is not None and isinstance(runner.llm, HarnessModelClient):
                runner.llm.close()
        return update or orchestrator_pb2.CompactionUpdate()

    def Converse(self, request_iterator, context):
        run_id, resume, surface_sha256, retry_of_run_id, retry_of_run_ids = self._continuation_metadata(context)
        user_text = ""
        session_id = ""
        actor = None
        history: list[dict[str, str]] = []
        plan_todo_snapshot = None
        working_dir = ""
        harness_managed = False
        memory_context = ""
        harness_context = ""
        ledger_seq = 0
        ledger_checksum = ""
        agent_task = None
        allowed_tools = ()
        model_gateway = None
        for message in request_iterator:
            payload = message.WhichOneof("payload")
            if payload == "user_input":
                user_input = message.user_input
                model_gateway = user_input.model_gateway if user_input.HasField("model_gateway") else None
                user_text = user_input.text
                session_id = user_input.session_id
                working_dir = self._validate_working_dir(user_input.working_dir, session_id, context)
                harness_managed = user_input.harness_managed
                memory_context = user_input.memory_context_json
                if user_input.HasField("context_envelope"):
                    if not harness_managed:
                        context.abort(grpc.StatusCode.INVALID_ARGUMENT, "context envelope requires a harness-managed request")
                    try:
                        harness_context = _render_context_envelope(user_input.context_envelope)
                        ledger_seq = max(0, int(user_input.context_envelope.ledger_seq))
                        ledger_checksum = str(user_input.context_envelope.ledger_checksum).strip().lower()
                    except ValueError as exc:
                        context.abort(grpc.StatusCode.INVALID_ARGUMENT, str(exc))
                agent_task = user_input.agent_task if user_input.HasField("agent_task") else None
                allowed_tools = tuple(user_input.allowed_tools)
                if len(memory_context) > 64000:
                    context.abort(grpc.StatusCode.INVALID_ARGUMENT,"memory context is too large")
                if agent_task is not None and (not harness_managed or agent_task.child_session_id != session_id or agent_task.schema_version != "agent.v2"):
                    context.abort(grpc.StatusCode.INVALID_ARGUMENT,"invalid independent agent task")
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
                        **({"event_id": item.event_id, "event_checksum": item.event_checksum}
                           if item.event_id or item.event_checksum else {}),
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
            parent_ctx = _admitted_grpc_trace_context(context.invocation_metadata())
            if parent_ctx is not None:
                from opentelemetry import context as otel_context

                otel_token = otel_context.attach(parent_ctx)
        except Exception:
            pass

        runner = None
        try:
            if agent_task is not None:
                # An independent child receives a separate project boundary.
                # Its working tree is the only root from which model-facing
                # manifests and instructions may be discovered.
                child_root = working_dir or self.app.project_root
                runner = self._new_runner(
                    working_dir=working_dir or None,
                    project_root=child_root,
                    **({"model_gateway": model_gateway} if model_gateway is not None else {}),
                )
            elif working_dir:
                runner = self._new_runner(working_dir=working_dir, **({"model_gateway": model_gateway} if model_gateway is not None else {}))
            else:
                runner = self._new_runner(**({"model_gateway": model_gateway} if model_gateway is not None else {}))
            if harness_managed:
                runner.harness_managed = True
                runner.harness_memory_context = memory_context
                runner.harness_context = harness_context
                runner.layered_context = None
                runner.todo_manager = TodoManager()
                runner.token_budget = (None if model_gateway is not None else
                                       TokenBudget(max_tokens=self.app.config.max_tokens, max_cost=self.app.config.max_cost))
                if agent_task is not None:
                    task_identity = orchestrator_pb2.AgentTask()
                    task_identity.CopyFrom(agent_task)
                    task_identity.ClearField("messages")
                    task_identity.ClearField("artifacts")
                    task_identity.ClearField("pending_approvals")
                    runner.agent_task_context = (
                        "You are an independent child agent. Work only on the assigned task and explicit materials. "
                        "Your history, plan and budget are separate from the parent. "
                        "Use PublishArtifact to deliver files/data; AskUser requests input from the parent.\n"+
                        json.dumps(MessageToDict(task_identity,preserving_proto_field_name=True),ensure_ascii=False)
                    )
                if agent_task is not None or allowed_tools:
                    # Always apply the Harness-provided allow-list to a child;
                    # an empty list must fail closed instead of restoring the
                    # parent's complete default catalog.
                    runner.tool_registry = ToolRegistry(
                        runner.project_root,
                        allowed_tools=allowed_tools,
                        skills=runner.skills,
                    )
            runner_actor = actor
            new_turn_options = {}
            if dict(context.invocation_metadata() or ()).get("x-code-agent-new-turn", "").lower() == "true":
                new_turn_options["new_turn"] = True
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
                ledger_seq=ledger_seq,
                ledger_checksum=ledger_checksum,
                **new_turn_options,
            )
        except Exception as exc:  # noqa: BLE001 - never terminate a gRPC stream without Done
            # Keep the stream contract explicit so the Go harness can persist a
            # retryable terminal state instead of misclassifying an abrupt EOF.
            # Do not expose provider URLs, request bodies, or credential-bearing
            # exception text across the RPC boundary.
            error_name = type(exc).__name__
            error_code, message, retryable, rpc_status = _rpc_failure_details(exc)
            if not error_code:
                lowered = str(exc).lower()
                if "timeout" in lowered or "timed out" in lowered:
                    error_code = "provider_timeout"
                    message = "agent provider timed out; retry this task"
                elif "auth" in lowered or "401" in lowered or "403" in lowered:
                    error_code = "provider_authentication_error"
                    message = "model authentication failed; check provider credentials"
                else:
                    error_code = "provider_runtime_error"
                    message = "agent provider failed; retry this task"
            try:
                logging.getLogger(__name__).error(
                    "conversation runner failed: type=%s code=%s rpc_status=%s",
                    error_name,
                    error_code,
                    rpc_status or "none",
                )
            except Exception:
                pass
            yield orchestrator_pb2.OrchestratorMessage(
                done=orchestrator_pb2.Done(
                    success=False,
                    message=message,
                    error_code=error_code,
                    retryable=retryable,
                )
            )
        finally:
            lease.release()
            if model_gateway is not None and runner is not None and isinstance(runner.llm, HarnessModelClient):
                runner.llm.close()
            # Detach the TraceContext parent so following calls on this
            # thread do not inherit it.
            if otel_token is not None:
                try:
                    from opentelemetry import context as _otel_context
                    _otel_context.detach(otel_token)
                except Exception:
                    pass

    def _validate_working_dir(self, value: str, session_id: str, context) -> str:
        raw = str(value or "").strip()
        if not raw:
            return ""
        candidate = Path(raw)
        if not candidate.is_absolute():
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, "working directory must be absolute")
        try:
            resolved = candidate.resolve(strict=True)
        except (FileNotFoundError, OSError):
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, "working directory must exist")
        if not resolved.is_dir():
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, "working directory must be a directory")
        try:
            resolved.relative_to(Path(self.app.project_root).resolve())
        except ValueError:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, "working directory must be inside project root")
        return str(resolved)

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
