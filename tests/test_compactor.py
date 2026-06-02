from __future__ import annotations

from orchestrator.context.compactor import Compactor
from orchestrator.llm.client import ChatMessage
from orchestrator.runtime.conversation import ConversationRunner
from orchestrator.graph.main_graph import build_graph
from orchestrator.memory.manager import MemoryManager
from orchestrator.skills.manager import SkillManager
from orchestrator.todo.manager import TodoManager
from orchestrator.runtime.tools import ToolRegistry


def test_compactor_preserves_goal_paths_errors_and_recent_messages() -> None:
    compactor = Compactor(max_chars=8_000, max_messages=2)
    messages = [
        {"role": "user", "content": "Implement Read ranges in internal/tools/read.go"},
        {"role": "assistant", "content": "Decision: keep deterministic compaction."},
        {"role": "tool", "content": "Error: failed reading tests/go/tools_test.go"},
        {"role": "assistant", "content": "Recent response mentions orchestrator/runtime/conversation.py"},
    ]

    summary = compactor.compact_history(messages, keep_recent=1)

    assert "[Compacted conversation history]" in summary
    assert "Implement Read ranges" in summary
    assert "internal/tools/read.go" in summary
    assert "tests/go/tools_test.go" in summary
    assert "failed reading" in summary
    assert "Recent response mentions orchestrator/runtime/conversation.py" in summary


def test_conversation_runner_compacts_before_chat(tmp_path) -> None:
    runner = ConversationRunner(
        graph=build_graph(),
        llm=None,
        tool_registry=ToolRegistry(),
        todo_manager=TodoManager(),
        memory_manager=MemoryManager(str(tmp_path / "memory")),
        skills=SkillManager(),
        project_root=str(tmp_path),
        working_dir=str(tmp_path),
        compactor=Compactor(max_chars=200, max_messages=4),
    )
    messages = [ChatMessage(role="system", content="system prompt")]
    for idx in range(10):
        messages.append(
            ChatMessage(
                role="user" if idx == 0 else "assistant",
                content=f"message {idx} touching internal/tools/read.go with error {idx}",
            )
        )

    compacted = runner._compact_messages(messages)

    assert compacted[0].content == "system prompt"
    assert compacted[1].role == "system"
    assert "[Compacted conversation history]" in compacted[1].content
    assert "internal/tools/read.go" in compacted[1].content
    assert len(compacted) < len(messages)
