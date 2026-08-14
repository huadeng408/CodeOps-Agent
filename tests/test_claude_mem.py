import pytest

from orchestrator.rag.claude_mem import ClaudeMemClient


@pytest.mark.asyncio
async def test_claude_mem_is_disabled_for_benchmark_modes() -> None:
    client = ClaudeMemClient("http://127.0.0.1:37777")
    assert await client.context("localcode", memory_mode="disabled") == ""
