from __future__ import annotations

import argparse
import json
import time
from pathlib import Path

from orchestrator.graph.nodes import GraphState
from orchestrator.llm.client import ChatResponse
from orchestrator.runtime.conversation import ConversationRunner
from orchestrator.server import OrchestratorServer, ServerConfig


class NoToolLLM:
    model = "fake-process-provider"

    async def chat(self, _request):
        return ChatResponse(text="resumed")


def _runner(app: OrchestratorServer) -> ConversationRunner:
    return ConversationRunner(
        graph=app.graph,
        llm=NoToolLLM(),
        tool_registry=app.tools,
        todo_manager=app.todos,
        memory_manager=app.memory,
        skills=app.skills,
        project_root=app.project_root,
        working_dir=app.working_dir,
        token_budget=app.token_budget,
        layered_context=app.layered_context,
    )


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", required=True)
    parser.add_argument("--marker", required=True)
    parser.add_argument("--resume", action="store_true")
    args = parser.parse_args()

    root = Path(args.root)
    app = OrchestratorServer(
        ServerConfig(project_root=str(root), memory_dir=str(root / "memory"))
    )
    try:
        if not args.resume:
            app.graph.write_checkpoint(
                GraphState(
                    metadata={
                        "session_id": "conversation-process-e2e",
                        "phase": "tool_after",
                        "turn": 2,
                    },
                    tool_rounds=2,
                    done=False,
                    next_node="route",
                ),
                thread_id="conversation-process-e2e",
            )
            Path(args.marker).write_text("checkpointed\n", encoding="utf-8")
            while True:
                time.sleep(1)

        runner = _runner(app)
        messages = list(
            runner.run(
                "continue",
                iter(()),
                session_id="conversation-process-e2e",
            )
        )
        print(
            json.dumps(
                {
                    "done": bool(messages[-1].done.success),
                    "turn": runner._loop_last_turn,
                    "checkpoint_phase": runner.load_checkpoint(
                        "conversation-process-e2e"
                    ).phase,
                },
                sort_keys=True,
            ),
            flush=True,
        )
        return 0
    finally:
        app.close()


if __name__ == "__main__":
    raise SystemExit(main())
