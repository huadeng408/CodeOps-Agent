import asyncio
import json
import time

import httpx
import pytest

from orchestrator.rag.claude_mem import ClaudeMemClient, MemoryReadOutcome


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
async def test_sensitive_query_fails_open_without_calling_worker() -> None:
    transport = httpx.MockTransport(
        lambda _: (_ for _ in ()).throw(AssertionError("must not request"))
    )
    client = ClaudeMemClient("http://127.0.0.1:37777", transport=transport)

    assert (
        await client.context(
            "localcode",
            query="OPENAI_API_KEY=fixture-placeholder",
            memory_mode="enabled",
        )
        == ""
    )
    assert client.last_outcome.failure_category == "privacy_rejected"


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


@pytest.mark.asyncio
async def test_enabled_mode_accepts_worker_batch_list_for_multiple_ids() -> None:
    def handler(request: httpx.Request) -> httpx.Response:
        if request.url.path == "/api/search":
            return httpx.Response(
                200,
                json={"content": [{"type": "text", "text": "| #385 | a | #386 | b |"}]},
            )
        return httpx.Response(
            200,
            json=[
                {"id": 385, "project": "localcode", "title": "first"},
                {"id": 386, "project": "localcode", "title": "second"},
            ],
        )

    client = ClaudeMemClient(
        "http://127.0.0.1:37777",
        transport=httpx.MockTransport(handler),
    )

    assert await client.context("localcode", memory_mode="enabled") == (
        "[claude-mem:385] first\n[claude-mem:386] second"
    )
    assert client.last_outcome.citation_ids == (385, 386)


def _client_returning_observation(title: str) -> ClaudeMemClient:
    def handler(request: httpx.Request) -> httpx.Response:
        if request.url.path == "/api/search":
            return httpx.Response(
                200,
                json={"content": [{"type": "text", "text": "| #385 | result |"}]},
            )
        return httpx.Response(
            200,
            json={"id": 385, "project": "localcode", "title": title},
        )

    return ClaudeMemClient(
        "http://127.0.0.1:37777",
        transport=httpx.MockTransport(handler),
    )


@pytest.mark.asyncio
@pytest.mark.parametrize(
    "unsafe_text",
    [
        "OPENAI_API_KEY=fixture-placeholder",
        "Authorization: Bearer fixture-bearer-token",
        r"C:\Users\private\notes.txt",
        "-----BEGIN PRIVATE KEY-----",
    ],
)
async def test_sensitive_worker_result_fails_open(unsafe_text: str) -> None:
    client = _client_returning_observation(unsafe_text)

    assert await client.context("localcode", memory_mode="enabled") == ""
    assert client.last_outcome.failure_category == "privacy_rejected"


@pytest.mark.asyncio
async def test_context_ends_at_utf8_boundary_under_4096_bytes() -> None:
    client = _client_returning_observation("中" * 3000)

    context = await client.context("localcode", memory_mode="enabled")

    assert len(context.encode("utf-8")) <= 4096
    assert context.encode("utf-8").decode("utf-8") == context


@pytest.mark.asyncio
async def test_worker_timeout_fails_open() -> None:
    transport = httpx.MockTransport(
        lambda _: (_ for _ in ()).throw(httpx.ReadTimeout("worker timeout"))
    )
    client = ClaudeMemClient("http://127.0.0.1:37777", transport=transport)

    assert await client.context("localcode", memory_mode="enabled") == ""


def test_memory_outcome_attributes_exclude_content_query_and_path() -> None:
    outcome = MemoryReadOutcome("claude_mem", 17, (385, 404), None)

    assert outcome.telemetry_attributes() == {
        "memory.backend": "claude_mem",
        "memory.latency_ms": 17,
        "memory.result_count": 2,
        "memory.citation_ids": "385,404",
    }


@pytest.mark.asyncio
async def test_enabled_read_exposes_only_allowlisted_outcome() -> None:
    client = _client_returning_observation("L3 design")

    assert await client.context("localcode", memory_mode="enabled") == "[claude-mem:385] L3 design"
    assert client.last_outcome.telemetry_attributes().keys() == {
        "memory.backend",
        "memory.latency_ms",
        "memory.result_count",
        "memory.citation_ids",
    }
    assert client.last_outcome.citation_ids == (385,)


@pytest.mark.asyncio
async def test_total_worker_budget_fails_open_before_slow_transport_completes() -> None:
    async def handler(request: httpx.Request) -> httpx.Response:
        await asyncio.sleep(0.05)
        return httpx.Response(
            200,
            json={"content": [{"type": "text", "text": "| #385 | result |"}]},
        )

    client = ClaudeMemClient(
        "http://127.0.0.1:37777",
        transport=httpx.MockTransport(handler),
        timeout_seconds=0.01,
    )
    started = time.monotonic()

    assert await client.context("localcode", memory_mode="enabled") == ""
    assert time.monotonic() - started < 0.04
    assert client.last_outcome.failure_category == "timeout"
