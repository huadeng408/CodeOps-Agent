from __future__ import annotations

import json
import sys
from pathlib import Path

REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
if str(REPOSITORY_ROOT) not in sys.path:
    sys.path.insert(0, str(REPOSITORY_ROOT))

from codeagent import orchestrator_pb2
from orchestrator.graph.main_graph import build_graph
from orchestrator.llm.client import ChatResponse, ToolCall, Usage
from orchestrator.memory.manager import MemoryManager
from orchestrator.runtime.agent_loop import AgentLoopPluginRegistry
from orchestrator.runtime.conversation import ConversationRunner
from orchestrator.runtime.tools import ToolRegistry
from orchestrator.skills.manager import SkillManager
from orchestrator.todo.manager import TodoManager


class ProcessLLM:
    model = "local-agent-loop-e2e"

    def __init__(self) -> None:
        self.calls = 0

    async def chat(self, request):
        self.calls += 1
        if self.calls == 1:
            return ChatResponse(
                tool_calls=[
                    ToolCall(
                        id="process-read-1",
                        name="Read",
                        arguments={"path": "README.md"},
                        arguments_json='{"path":"README.md"}',
                    )
                ],
                usage=Usage(input_tokens=1, output_tokens=1),
            )
        return ChatResponse(text="AGENT_LOOP_PROCESS_E2E_OK", usage=Usage(input_tokens=1, output_tokens=1))


def main() -> int:
    root = Path(sys.argv[1]).resolve()
    llm = ProcessLLM()
    registry = AgentLoopPluginRegistry()
    phases: list[str] = []
    registry.register("process-recorder", lambda event: phases.append(event.phase) or None)
    runner = ConversationRunner(
        graph=build_graph(),
        llm=llm,
        tool_registry=ToolRegistry(),
        todo_manager=TodoManager(),
        memory_manager=MemoryManager(str(root / "memory")),
        skills=SkillManager(),
        project_root=str(root),
        working_dir=str(root),
        loop_plugins=registry,
    )
    messages = iter(
        [
            orchestrator_pb2.HarnessMessage(
                tool_result=orchestrator_pb2.ToolResult(
                    tool_name="Read",
                    tool_call_id="process-read-1",
                    output="README content",
                )
            )
        ]
    )
    output = list(runner.run("inspect README", messages, session_id="process-session"))
    done = output[-1].done
    print(
        json.dumps(
            {
                "status": "ok" if done.success else "failed",
                "message": done.message,
                "phases": phases,
                "calls": llm.calls,
                "plugin_errors": list(runner.loop_plugin_errors),
            },
            separators=(",", ":"),
        )
    )
    return 0 if done.success and phases[-1:] == ["loop_end"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
