import json

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


@pytest.mark.asyncio
@pytest.mark.parametrize(
    ("worker_url", "project", "query"),
    [
        ("http://10.0.0.8:37777", "localcode", "*"),
        ("https://example.test", "localcode", "*"),
        ("http://127.0.0.1:37777", "../../other", "*"),
        ("http://127.0.0.1:37777", "localcode", "x" * 257),
    ],
)
async def test_invalid_boundary_returns_empty_without_request(
    worker_url: str,
    project: str,
    query: str,
) -> None:
    transport = httpx.MockTransport(
        lambda _: (_ for _ in ()).throw(AssertionError("must not request"))
    )
    client = ClaudeMemClient(worker_url, transport=transport)

    assert await client.context(project, query=query, memory_mode="enabled") == ""


@pytest.mark.asyncio
async def test_enabled_mode_searches_then_fetches_selected_project_ids() -> None:
    requests: list[httpx.Request] = []

    def handler(request: httpx.Request) -> httpx.Response:
        requests.append(request)
        if request.url.path == "/api/search":
            assert dict(request.url.params) == {
                "query": "adapter design",
                "project": "localcode",
                "limit": "3",
            }
            return httpx.Response(
                200,
                json={"content": [{"type": "text", "text": "| #385 | design |"}]},
            )

        assert request.url.path == "/api/observations/batch"
        assert json.loads(request.content) == {"ids": [385], "project": "localcode"}
        return httpx.Response(
            200,
            json={"id": 385, "project": "localcode", "title": "L3 design"},
        )

    client = ClaudeMemClient(
        "http://127.0.0.1:37777",
        transport=httpx.MockTransport(handler),
    )

    assert (
        await client.context(
            "localcode",
            query="adapter design",
            memory_mode="enabled",
        )
        == "[claude-mem:385] L3 design"
    )
    assert [request.url.path for request in requests] == [
        "/api/search",
        "/api/observations/batch",
    ]
