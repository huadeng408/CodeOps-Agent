from __future__ import annotations

import os
from concurrent import futures
from pathlib import Path

import grpc

from codeagent import orchestrator_pb2, orchestrator_pb2_grpc
from orchestrator.agents.process import ProcessAgentExecutor
from orchestrator.server import build_parser


class SpawnWorktreeService(orchestrator_pb2_grpc.OrchestratorServicer):
    def __init__(self, project_root: str, working_dir: str) -> None:
        self.project_root = str(Path(project_root).resolve())
        self.working_dir = str(Path(working_dir).resolve())

    def Health(self, request, context):
        return orchestrator_pb2.HealthResponse(status="ok", version="spawn-worktree-e2e")

    def Converse(self, request_iterator, context):
        first = next(request_iterator, None)
        if first is None or first.WhichOneof("payload") != "user_input":
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, "user input required")
        session_id = first.user_input.session_id or "spawn-worktree-e2e-session"
        request_id = "spawn-worktree-e2e"
        child_session_id = f"{session_id}:subagent:{request_id}"[:96]
        worktree_name = "agent-" + request_id
        yield orchestrator_pb2.OrchestratorMessage(
            agent_spawn=orchestrator_pb2.AgentSpawn(
                kind="review",
                task="Inspect isolated checkout",
                context_json='{"files":["tracked.txt"]}',
                protocol_version="agent.v1",
                request_id=request_id,
                parent_session_id=session_id,
                child_session_id=child_session_id,
                worktree_name=worktree_name,
                isolation="worktree",
            )
        )
        decision = next(request_iterator, None)
        if (
            decision is None
            or decision.WhichOneof("payload") != "agent_spawn_decision"
            or decision.agent_spawn_decision.request_id != request_id
            or not decision.agent_spawn_decision.accepted
        ):
            context.abort(grpc.StatusCode.FAILED_PRECONDITION, "Harness did not acknowledge worktree")
        executor = ProcessAgentExecutor(
            project_root=self.project_root,
            working_dir=self.working_dir,
        )
        child_path = Path(self.project_root) / ".agent" / "worktrees" / worktree_name
        try:
            result = executor.run(
                kind="review",
                title="Inspect isolated checkout",
                objective="inspect the isolated checkout",
                context={"files": ["tracked.txt"]},
                request_id=request_id,
                parent_session_id=session_id,
                child_session_id=child_session_id,
                worktree_path=child_path,
                require_worktree=True,
            )
        except Exception as exc:
            context.abort(grpc.StatusCode.FAILED_PRECONDITION, f"child isolation failed: {type(exc).__name__}")
        yield orchestrator_pb2.OrchestratorMessage(
            text=orchestrator_pb2.TextChunk(
                text="SPAWN_WORKTREE_OK:" + result.summary + ":" + ";".join(result.artifacts)
            )
        )
        yield orchestrator_pb2.OrchestratorMessage(
            agent_lifecycle=orchestrator_pb2.AgentLifecycle(
                request_id=request_id,
                child_session_id=child_session_id,
                status="completed",
                reason="child completed",
            )
        )
        yield orchestrator_pb2.OrchestratorMessage(done=orchestrator_pb2.Done(success=True))


def main() -> None:
    args = build_parser().parse_args()
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=4))
    orchestrator_pb2_grpc.add_OrchestratorServicer_to_server(
        SpawnWorktreeService(args.project_root, args.working_dir), server
    )
    server.add_insecure_port(f"{args.host}:{args.port}")
    server.start()
    print(f"[spawn-worktree-e2e] pid={os.getpid()}", flush=True)
    try:
        server.wait_for_termination()
    except KeyboardInterrupt:
        server.stop(grace=1)


if __name__ == "__main__":
    main()
