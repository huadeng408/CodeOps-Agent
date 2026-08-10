"""Compaction must never orphan a tool message.

Regression tests for HTTP 400 "Messages with role 'tool' must be a response to a
preceding message with 'tool_calls'", which killed SWE-bench instances mid-solve.

The cause was an inverted guard in ``_compact_messages``: the repair walked back
for the anchoring assistant message only when ``orphan_start > 0``, i.e. only in
the cases that were already fine, and skipped ``orphan_start == 0`` — a window
whose *first* message is a tool result and which therefore has no anchor at all.
"""

from __future__ import annotations

from orchestrator.llm.client import ChatMessage, ToolCall
from orchestrator.runtime.conversation import (
    ConversationRunner,
    _strip_leading_tool_messages,
)


def _assistant_with_calls(name: str = "Grep") -> ChatMessage:
    return ChatMessage(
        role="assistant",
        content="",
        tool_calls=[ToolCall(id="call_1", name=name, arguments={}, arguments_json="{}")],
    )


def _tool_result(content: str = "hit") -> ChatMessage:
    return ChatMessage(role="tool", content=content, name="Grep", tool_call_id="call_1")


class _AlwaysCompact:
    """Compactor that always fires, keeping a small recent window."""

    max_messages = 4

    def should_compact(self, messages):
        return True

    def compact_history(self, messages):
        return "summary of earlier turns"


def _runner() -> ConversationRunner:
    runner = ConversationRunner.__new__(ConversationRunner)
    runner.compactor = _AlwaysCompact()
    return runner


def _is_valid_order(messages: list[ChatMessage]) -> bool:
    """Every tool message must be preceded by an assistant carrying tool_calls."""
    for index, message in enumerate(messages):
        if message.role != "tool":
            continue
        anchor_found = False
        for earlier in reversed(messages[:index]):
            if earlier.role == "tool":
                continue
            anchor_found = earlier.role == "assistant" and bool(earlier.tool_calls)
            break
        if not anchor_found:
            return False
    return True


# ------------------------------------------------------------------ the helper


def test_strip_removes_leading_tool_messages():
    window = [_tool_result(), _tool_result(), ChatMessage(role="user", content="hi")]
    assert [m.role for m in _strip_leading_tool_messages(window)] == ["user"]


def test_strip_keeps_an_anchored_window_intact():
    window = [_assistant_with_calls(), _tool_result()]
    assert _strip_leading_tool_messages(window) == window


def test_strip_handles_an_empty_window():
    assert _strip_leading_tool_messages([]) == []


def test_strip_handles_an_all_tool_window():
    assert _strip_leading_tool_messages([_tool_result(), _tool_result()]) == []


# -------------------------------------------------------------- the real defect


def test_compaction_never_starts_the_window_with_a_tool_message():
    """The exact shape that produced HTTP 400."""
    messages = [
        ChatMessage(role="system", content="sys"),
        ChatMessage(role="user", content="fix the bug"),
        _assistant_with_calls(),
        _tool_result("first"),
        _assistant_with_calls(),
        _tool_result("second"),
        _assistant_with_calls(),
        _tool_result("third"),
    ]
    out = _runner()._compact_messages(messages)
    tail = out[2:]  # past the preserved system message and the summary
    assert not (tail and tail[0].role == "tool")
    assert _is_valid_order(out)


def test_compaction_output_is_always_orderable():
    """Sweep window boundaries; every one must produce a sendable history."""
    base = [ChatMessage(role="system", content="sys"), ChatMessage(role="user", content="go")]
    for pairs in range(1, 7):
        messages = list(base)
        for i in range(pairs):
            messages.append(_assistant_with_calls())
            messages.append(_tool_result(f"result {i}"))
        out = _runner()._compact_messages(messages)
        assert _is_valid_order(out), f"invalid order with {pairs} tool pairs"


def test_compaction_preserves_the_system_message_first():
    messages = [
        ChatMessage(role="system", content="sys"),
        ChatMessage(role="user", content="go"),
        _assistant_with_calls(),
        _tool_result(),
        _assistant_with_calls(),
        _tool_result(),
    ]
    out = _runner()._compact_messages(messages)
    assert out[0].role == "system"
    assert out[0].content == "sys"
    assert out[1].role == "system"  # the summary


def test_compaction_walks_back_to_include_the_anchor():
    """When an anchor exists outside the window it is pulled in, not dropped."""
    messages = [
        ChatMessage(role="system", content="sys"),
        ChatMessage(role="user", content="go"),
        _assistant_with_calls("Read"),
        _tool_result("a"),
        _tool_result("b"),
        _tool_result("c"),
    ]
    out = _runner()._compact_messages(messages)
    tool_indexes = [i for i, m in enumerate(out) if m.role == "tool"]
    assert tool_indexes, "the tool results should survive when their anchor can"
    assert _is_valid_order(out)


def test_no_compaction_when_the_compactor_declines():
    class _Never(_AlwaysCompact):
        def should_compact(self, messages):
            return False

    runner = ConversationRunner.__new__(ConversationRunner)
    runner.compactor = _Never()
    messages = [ChatMessage(role="system", content="s"), _assistant_with_calls(), _tool_result()]
    assert runner._compact_messages(messages) == messages


def test_no_compaction_without_a_compactor():
    runner = ConversationRunner.__new__(ConversationRunner)
    runner.compactor = None
    messages = [ChatMessage(role="system", content="s"), _tool_result()]
    assert runner._compact_messages(messages) == messages


def test_short_histories_are_left_alone():
    runner = _runner()
    messages = [ChatMessage(role="system", content="s"), ChatMessage(role="user", content="u")]
    assert runner._compact_messages(messages) == messages
