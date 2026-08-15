import httpx
import pytest

from orchestrator.rag.claude_mem import ClaudeMemClient


@pytest.mark.asyncio
async def test_claude_mem_is_disabled_for_benchmark_modes() -> None:
    client = ClaudeMemClient("http://127.0.0.1:37777")
    assert await client.context("localcode", memory_mode="disabled") == ""


@pytest.mark.asyncio
async def test_disabled_mode_returns_empty_without_calling_worker() -> None:
    requests: list[httpx.Request] = []

    def handler(request: httpx.Request) -> httpx.Response:
        requests.append(request)
        raise AssertionError("disabled mode must not request worker")

    client = ClaudeMemClient(
        "http://127.0.0.1:37777",
        transport=httpx.MockTransport(handler),
    )

    assert await client.context("localcode", memory_mode="disabled") == ""
    assert requests == []
