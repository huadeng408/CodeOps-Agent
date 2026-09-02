from __future__ import annotations

import asyncio
import base64
import io
import json
import threading
import time
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import pytest

from orchestrator.llm.client import (
    ChatMessage,
    ChatRequest,
    ChatResponse,
    LLMClient,
    RequestInterrupted,
    StreamDelta,
    ToolCall,
    Usage,
    http_call_with_retry,
)
from orchestrator.llm.providers import AnthropicClient, LocalClient, OpenAIClient, build_default_client


def _json_server(response_payload: dict[str, object]):
    captured: dict[str, object] = {}

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self) -> None:  # noqa: N802
            length = int(self.headers.get("Content-Length", "0"))
            body = self.rfile.read(length).decode("utf-8")
            captured["path"] = self.path
            captured["headers"] = {key.lower(): value for key, value in self.headers.items()}
            captured["body"] = body
            encoded = json.dumps(response_payload).encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(encoded)))
            self.end_headers()
            self.wfile.write(encoded)

        def log_message(self, format: str, *args) -> None:  # noqa: A003
            return

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    return server, captured


def _provider_messages() -> list[ChatMessage]:
    return [
        ChatMessage(role="system", content="system prompt"),
        ChatMessage(role="user", content="hello"),
        ChatMessage(
            role="assistant",
            content="",
            tool_calls=[
                ToolCall(
                    id="call-1",
                    name="Read",
                    arguments={"path": "README.md"},
                    arguments_json='{"path":"README.md"}',
                )
            ],
        ),
        ChatMessage(
            role="tool",
            name="Read",
            tool_call_id="call-1",
            content="README content",
            is_error=True,
        ),
    ]


def test_local_client_uses_openai_compatible_payload() -> None:
    server, captured = _json_server(
        {
            "choices": [
                {
                    "message": {
                        "role": "assistant",
                        "content": "done",
                        "tool_calls": [
                            {
                                "id": "call-2",
                                "type": "function",
                                "function": {
                                    "name": "Glob",
                                    "arguments": "{\"pattern\":\"**/*.py\"}",
                                },
                            }
                        ],
                    }
                }
            ],
            "usage": {
                "prompt_tokens": 11,
                "completion_tokens": 7,
                "prompt_tokens_details": {"cached_tokens": 3},
            },
        }
    )
    try:
        client = LocalClient(api_key="", base_url=f"http://127.0.0.1:{server.server_port}", model="local-model")
        response = asyncio.run(
            client.chat(
                ChatRequest(
                    model="local-model",
                    messages=_provider_messages(),
                    tools=[
                        {
                            "type": "function",
                            "function": {
                                "name": "Read",
                                "description": "Read a file.",
                                "parameters": {
                                    "type": "object",
                                    "properties": {"path": {"type": "string"}},
                                },
                            },
                        }
                    ],
                )
            )
        )

        assert response.text == "done"
        assert response.tool_calls[0].name == "Glob"
        assert response.usage.input_tokens == 11
        assert response.usage.output_tokens == 7
        assert response.usage.cached_input_tokens == 3

        body = json.loads(captured["body"])
        assert "authorization" not in captured["headers"]
        assert body["messages"][2]["role"] == "assistant"
        assert body["messages"][2]["tool_calls"][0]["function"]["name"] == "Read"
        assert body["messages"][3]["role"] == "tool"
        assert body["messages"][3]["tool_call_id"] == "call-1"
        assert body["tools"][0]["function"]["name"] == "Read"
    finally:
        server.shutdown()
        server.server_close()


def test_openai_reasoning_effort_only_for_reasoning_models() -> None:
    """reasoning_effort must NOT be sent for non-reasoning models (avoids HTTP 400)."""
    server, captured = _json_server(
        {
            "choices": [
                {"message": {"role": "assistant", "content": "ok"}}
            ],
            "usage": {"prompt_tokens": 5, "completion_tokens": 2},
        }
    )
    try:
        # Non-reasoning model: reasoning_effort must be omitted even when
        # thinking is requested (this is the P0-1 bug fix).
        client = OpenAIClient(
            api_key="test-key",
            base_url=f"http://127.0.0.1:{server.server_port}",
            model="gpt-4o",
        )
        asyncio.run(
            client.chat(
                ChatRequest(
                    model="gpt-4o",
                    messages=_provider_messages(),
                    thinking_enabled=True,
                    reasoning_effort="medium",
                )
            )
        )
        body = json.loads(captured["body"])
        assert "reasoning_effort" not in body, (
            f"reasoning_effort must not be sent to gpt-4o, got: {body}"
        )
    finally:
        server.shutdown()
        server.server_close()


def test_openai_client_sends_configured_output_token_budget(monkeypatch) -> None:
    server, captured = _json_server(
        {
            "id": "response-budget",
            "model": "locked-model",
            "system_fingerprint": "revision-1",
            "choices": [{"message": {"role": "assistant", "content": "ok"}}],
            "usage": {"prompt_tokens": 5, "completion_tokens": 2},
        }
    )
    try:
        monkeypatch.setenv("OPENAI_API_KEY", "test-key")
        monkeypatch.setenv(
            "OPENAI_BASE_URL", f"http://127.0.0.1:{server.server_port}"
        )
        monkeypatch.setenv("OPENAI_MODEL", "locked-model")
        monkeypatch.setenv("OPENAI_MAX_TOKENS", "128")

        client = OpenAIClient.from_env()
        assert client is not None
        asyncio.run(
            client.chat(
                ChatRequest(
                    model="locked-model",
                    messages=[ChatMessage(role="user", content="bounded response")],
                    temperature=0.0,
                )
            )
        )

        body = json.loads(captured["body"])
        assert body.get("max_tokens") == 128
    finally:
        server.shutdown()
        server.server_close()


def test_openai_stream_payload_sends_configured_output_token_budget() -> None:
    client = OpenAIClient(
        api_key="test-key",
        base_url="https://example.test",
        model="locked-model",
        max_tokens=128,
    )

    payload = client._stream_payload(
        ChatRequest(
            model="locked-model",
            messages=[ChatMessage(role="user", content="bounded stream")],
            temperature=0.0,
        )
    )

    assert payload.get("max_tokens") == 128


def test_openai_reasoning_effort_sent_for_reasoning_model() -> None:
    """reasoning_effort IS sent for reasoning models, and reasoning content is parsed back."""
    server, captured = _json_server(
        {
            "choices": [
                {
                    "message": {
                        "role": "assistant",
                        "content": "answer",
                        "reasoning_content": "step-by-step reasoning",
                    }
                }
            ],
            "usage": {"prompt_tokens": 5, "completion_tokens": 2},
        }
    )
    try:
        client = OpenAIClient(
            api_key="test-key",
            base_url=f"http://127.0.0.1:{server.server_port}",
            model="o3-mini",
        )
        response = asyncio.run(
            client.chat(
                ChatRequest(
                    model="o3-mini",
                    messages=_provider_messages(),
                    thinking_enabled=True,
                    reasoning_effort="high",
                )
            )
        )
        body = json.loads(captured["body"])
        assert body["reasoning_effort"] == "high"
        assert response.thinking_blocks == [
            {"type": "thinking", "thinking": "step-by-step reasoning"}
        ]
    finally:
        server.shutdown()
        server.server_close()


def test_anthropic_client_uses_tool_use_payload() -> None:
    server, captured = _json_server(
        {
            "id": "msg-provider-identity",
            "model": "gpt-5.6-sol",
            "content": [
                {"type": "text", "text": "anthropic reply"},
                {
                    "type": "tool_use",
                    "id": "toolu-2",
                    "name": "Glob",
                    "input": {"pattern": "**/*.py"},
                },
            ],
            "usage": {
                "input_tokens": 21,
                "output_tokens": 9,
                "cache_read_input_tokens": 4,
            },
        }
    )
    try:
        client = AnthropicClient(
            api_key="test-key",
            base_url=f"http://127.0.0.1:{server.server_port}",
            model="claude-test",
            timeout=5.0,
            max_tokens=1024,
        )
        response = asyncio.run(
            client.chat(
                ChatRequest(
                    model="claude-test",
                    messages=_provider_messages(),
                    tools=[
                        {
                            "type": "function",
                            "function": {
                                "name": "Read",
                                "description": "Read a file.",
                                "parameters": {
                                    "type": "object",
                                    "properties": {"path": {"type": "string"}},
                                },
                            },
                        }
                    ],
                )
            )
        )

        assert response.text == "anthropic reply"
        assert response.tool_calls[0].name == "Glob"
        assert response.usage.input_tokens == 21
        assert response.usage.output_tokens == 9
        assert response.usage.cached_input_tokens == 4
        assert response.model_identity == {
            "requested_model": "claude-test",
            "reported_model": "gpt-5.6-sol",
            "response_id": "msg-provider-identity",
            "system_fingerprint": "",
            "created": 0,
            "identity_verified": False,
        }

        body = json.loads(captured["body"])
        assert captured["headers"]["x-api-key"] == "test-key"
        assert captured["headers"]["anthropic-version"] == "2023-06-01"
        assert body["system"] == "system prompt"
        assert body["messages"][1]["content"][0]["type"] == "tool_use"
        assert body["messages"][2]["content"][0]["type"] == "tool_result"
        assert body["messages"][2]["content"][0]["is_error"] is True
        assert body["tools"][0]["input_schema"]["properties"]["path"]["type"] == "string"
    finally:
        server.shutdown()
        server.server_close()


def test_openai_compatible_client_retries_transient_url_errors(monkeypatch) -> None:
    calls = 0

    class FakeResponse:
        def __enter__(self):
            return self

        def __exit__(self, exc_type, exc, traceback):
            return False

        def read(self) -> bytes:
            return json.dumps({"choices": [{"message": {"content": "retried"}}]}).encode("utf-8")

    def fake_urlopen(request, timeout):  # noqa: ANN001
        nonlocal calls
        calls += 1
        assert timeout == 0.5
        if calls == 1:
            raise urllib.error.URLError("timed out")
        return FakeResponse()

    monkeypatch.setattr(
        "orchestrator.llm.providers.openai.urllib.request.urlopen",
        fake_urlopen,
    )

    client = OpenAIClient(
        api_key="test-key",
        base_url="http://127.0.0.1:9",
        model="gpt-test",
        timeout=0.5,
        max_retries=1,
    )
    response = asyncio.run(
        client.chat(
            ChatRequest(
                model="gpt-test",
                messages=[ChatMessage(role="user", content="hello")],
            )
        )
    )

    assert response.text == "retried"
    assert calls == 2


def test_default_client_prefers_explicit_provider(monkeypatch) -> None:
    monkeypatch.setenv("LLM_PROVIDER", "local")
    monkeypatch.setenv("LOCAL_LLM_BASE_URL", "http://127.0.0.1:1234")
    monkeypatch.delenv("OPENAI_API_KEY", raising=False)
    monkeypatch.delenv("ANTHROPIC_API_KEY", raising=False)

    client = build_default_client()
    assert isinstance(client, LocalClient)


def test_http_call_raises_when_already_cancelled(monkeypatch) -> None:
    """A pre-set cancel event aborts before any urlopen attempt is made."""
    def fake_urlopen(request, timeout):  # noqa: ANN001
        raise AssertionError("urlopen must not run when already cancelled")

    monkeypatch.setattr(
        "orchestrator.llm.client.urllib.request.urlopen", fake_urlopen
    )

    cancel = threading.Event()
    cancel.set()
    req = urllib.request.Request(
        "http://127.0.0.1:1/", data=b"{}", method="POST"
    )
    with pytest.raises(RequestInterrupted):
        http_call_with_retry(
            req,
            timeout=1.0,
            max_retries=2,
            provider_name="Test",
            cancel_event=cancel,
        )


def test_http_call_aborts_in_flight_request() -> None:
    """An interrupt during a slow request aborts the caller well before the
    server responds (design 22.8: the in-flight call actually stops)."""
    release = threading.Event()

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self) -> None:  # noqa: N802
            length = int(self.headers.get("Content-Length", "0"))
            self.rfile.read(length)
            # Hang until teardown so the in-flight urlopen blocks.
            release.wait(timeout=5)
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", "2")
            self.end_headers()
            self.wfile.write(b"{}")

        def log_message(self, format: str, *args) -> None:  # noqa: A003
            return

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    port = server.server_port
    cancel = threading.Event()

    def canceller() -> None:
        time.sleep(0.3)
        cancel.set()

    threading.Thread(target=canceller, daemon=True).start()

    req = urllib.request.Request(
        f"http://127.0.0.1:{port}/",
        data=b"{}",
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    start = time.monotonic()
    try:
        with pytest.raises(RequestInterrupted):
            http_call_with_retry(
                req,
                timeout=10.0,
                max_retries=0,
                provider_name="Test",
                cancel_event=cancel,
            )
        elapsed = time.monotonic() - start
        # The server would hang for ~5s; the abort must return within ~2s.
        assert elapsed < 2.0, f"abort took too long: {elapsed:.2f}s"
    finally:
        release.set()
        server.shutdown()
        server.server_close()


def test_http_call_retries_when_not_cancelled(monkeypatch) -> None:
    """The cancel_event=None path keeps the original retry/backoff behavior."""
    calls = 0

    class FakeResponse:
        def __enter__(self):
            return self

        def __exit__(self, exc_type, exc, traceback):
            return False

        def read(self) -> bytes:
            return b'{"ok": true}'

    def fake_urlopen(request, timeout):  # noqa: ANN001
        nonlocal calls
        calls += 1
        if calls == 1:
            raise urllib.error.URLError("timed out")
        return FakeResponse()

    monkeypatch.setattr(
        "orchestrator.llm.client.urllib.request.urlopen", fake_urlopen
    )

    req = urllib.request.Request(
        "http://127.0.0.1:1/", data=b"{}", method="POST"
    )
    body = http_call_with_retry(
        req, timeout=0.5, max_retries=1, provider_name="Test"
    )
    assert body == b'{"ok": true}'
    assert calls == 2


# ---------------------------------------------------------------------------
# Streaming (design 22.6): SSE producers emit incremental text deltas.
# ---------------------------------------------------------------------------


def _sse_server(frames, delays=None, capture=None):
    """Spawn a local HTTP server that writes the given SSE *frames* (bytes).

    Each frame is written and flushed so the client can read it line by line.
    When *delays* is given, the server sleeps before the next frame -- used by
    the cancel test so an interrupt can land mid-stream. Captures the request
    body/path when *capture* is a dict.
    """
    captured = {} if capture is None else capture

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self) -> None:  # noqa: N802
            length = int(self.headers.get("Content-Length", "0"))
            captured["body"] = self.rfile.read(length).decode("utf-8")
            captured["path"] = self.path
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.end_headers()
            try:
                for index, frame in enumerate(frames):
                    self.wfile.write(frame)
                    self.wfile.flush()
                    if delays:
                        time.sleep(delays[min(index, len(delays) - 1)])
            except (BrokenPipeError, ConnectionResetError):
                # Client disconnected (e.g. after a mid-stream cancel).
                pass

        def log_message(self, format: str, *args) -> None:  # noqa: A003
            return

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    return server, captured


def _openai_frame(obj: dict[str, object]) -> bytes:
    return ("data: " + json.dumps(obj) + "\n\n").encode("utf-8")


def _anthropic_frame(event: str, obj: dict[str, object]) -> bytes:
    return ("event: " + event + "\ndata: " + json.dumps(obj) + "\n\n").encode("utf-8")


async def _collect(async_gen):
    out: list[StreamDelta] = []
    async for delta in async_gen:
        out.append(delta)
    return out


def test_default_stream_wraps_chat_into_deltas() -> None:
    """LLMClient.stream() default impl wraps chat() into the delta contract."""

    class StaticClient(LLMClient):
        async def chat(self, request):  # noqa: ANN001
            return ChatResponse(
                text="hi",
                tool_calls=[
                    ToolCall(
                        id="c1",
                        name="Read",
                        arguments={"path": "a"},
                        arguments_json='{"path": "a"}',
                    )
                ],
                usage=Usage(input_tokens=2, output_tokens=1),
            )

    deltas = asyncio.run(
        _collect(
            StaticClient().stream(
                ChatRequest(
                    model="m",
                    messages=[ChatMessage(role="user", content="hi")],
                )
            )
        )
    )
    assert [d.kind for d in deltas] == ["text", "tool_calls", "usage", "done"]
    assert deltas[0].text == "hi"
    assert deltas[1].tool_calls[0].name == "Read"
    assert deltas[2].usage.input_tokens == 2


def test_openai_stream_emits_incremental_text_and_tool_calls() -> None:
    """OpenAIClient.stream() yields many text deltas, merges tool-call
    argument fragments, and captures usage from the final chunk."""
    frames = [
        _openai_frame({"choices": [{"delta": {"content": "Hello"}}]}),
        _openai_frame({"choices": [{"delta": {"content": " world"}}]}),
        _openai_frame(
            {
                "choices": [
                    {
                        "delta": {
                            "tool_calls": [
                                {
                                    "index": 0,
                                    "id": "call-9",
                                    "function": {"name": "Glob", "arguments": '{"pattern":'},
                                }
                            ]
                        }
                    }
                ]
            }
        ),
        _openai_frame(
            {
                "choices": [
                    {
                        "delta": {
                            "tool_calls": [
                                {"index": 0, "function": {"arguments": '"**/*.py"}'}}
                            ]
                        }
                    }
                ]
            }
        ),
        _openai_frame(
            {
                "choices": [],
                "usage": {
                    "prompt_tokens": 12,
                    "completion_tokens": 5,
                    "prompt_tokens_details": {"cached_tokens": 2},
                },
            }
        ),
        b"data: [DONE]\n\n",
    ]
    server, captured = _sse_server(frames, capture={})
    try:
        client = OpenAIClient(
            api_key="test-key",
            base_url=f"http://127.0.0.1:{server.server_port}",
            model="gpt-4o",
            timeout=5.0,
        )
        deltas = asyncio.run(
            _collect(
                client.stream(
                    ChatRequest(
                        model="gpt-4o",
                        messages=_provider_messages(),
                    )
                )
            )
        )

        text = "".join(d.text for d in deltas if d.kind == "text")
        assert text == "Hello world"
        # Incremental: at least two separate text deltas, not one.
        assert sum(1 for d in deltas if d.kind == "text") >= 2

        tool_delta = next(d for d in deltas if d.kind == "tool_calls")
        assert len(tool_delta.tool_calls) == 1
        call = tool_delta.tool_calls[0]
        assert call.id == "call-9"
        assert call.name == "Glob"
        assert call.arguments == {"pattern": "**/*.py"}

        usage_delta = next(d for d in deltas if d.kind == "usage")
        assert usage_delta.usage.input_tokens == 12
        assert usage_delta.usage.output_tokens == 5
        assert usage_delta.usage.cached_input_tokens == 2
        assert any(d.kind == "done" for d in deltas)

        body = json.loads(captured["body"])
        assert body["stream"] is True
        assert body["stream_options"]["include_usage"] is True
    finally:
        server.shutdown()
        server.server_close()


def test_openai_stream_preserves_reasoning_content_for_tool_followup() -> None:
    frames = [
        _openai_frame(
            {"choices": [{"delta": {"reasoning_content": "inspect "}}]}
        ),
        _openai_frame(
            {
                "choices": [
                    {
                        "delta": {
                            "reasoning_content": "fixture",
                            "tool_calls": [
                                {
                                    "index": 0,
                                    "id": "call-reasoning",
                                    "function": {
                                        "name": "Read",
                                        "arguments": '{"file_path":"fixture.txt"}',
                                    },
                                }
                            ],
                        }
                    }
                ]
            }
        ),
        b"data: [DONE]\n\n",
    ]
    server, _ = _sse_server(frames)
    try:
        client = OpenAIClient(
            api_key="test-key",
            base_url=f"http://127.0.0.1:{server.server_port}",
            model="deepseek-v4-pro",
            timeout=5.0,
        )

        deltas = asyncio.run(
            _collect(
                client.stream(
                    ChatRequest(
                        model="deepseek-v4-pro",
                        messages=[ChatMessage(role="user", content="read fixture")],
                    )
                )
            )
        )

        done_delta = next(delta for delta in deltas if delta.kind == "done")
        assert done_delta.thinking_blocks == [
            {"type": "thinking", "thinking": "inspect fixture"}
        ]
    finally:
        server.shutdown()
        server.server_close()


def test_openai_stream_http_error_includes_redacted_response_body(monkeypatch) -> None:
    def reject_request(*args, **kwargs):  # noqa: ANN002, ANN003
        raise urllib.error.HTTPError(
            url="https://example.test/v1/chat/completions",
            code=400,
            msg="Bad Request",
            hdrs=None,
            fp=io.BytesIO(b'{"error":{"message":"bad test-key"}}'),
        )

    monkeypatch.setattr(
        "orchestrator.llm.providers.openai.urllib.request.urlopen",
        reject_request,
    )
    client = OpenAIClient(
        api_key="test-key",
        base_url="https://example.test",
        model="deepseek-v4-pro",
    )

    with pytest.raises(RuntimeError) as exc_info:
        asyncio.run(
            _collect(
                client.stream(
                    ChatRequest(
                        model="deepseek-v4-pro",
                        messages=[ChatMessage(role="user", content="hello")],
                    )
                )
            )
        )

    message = str(exc_info.value)
    assert "OpenAI HTTP 400" in message
    assert "<redacted>" in message
    assert "test-key" not in message


def test_openai_assistant_message_returns_reasoning_content_with_tool_call() -> None:
    payload = OpenAIClient._message_payload(
        ChatMessage(
            role="assistant",
            content="",
            thinking_blocks=[
                {"type": "thinking", "thinking": "inspect fixture"}
            ],
            tool_calls=[
                ToolCall(
                    id="call-reasoning",
                    name="Read",
                    arguments={"file_path": "fixture.txt"},
                )
            ],
        )
    )

    assert payload["reasoning_content"] == "inspect fixture"


def test_anthropic_stream_emits_incremental_text_and_tool_use() -> None:
    """AnthropicClient.stream() yields text deltas from text blocks and
    accumulates tool-use input JSON from partial_json deltas."""
    frames = [
        _anthropic_frame(
            "message_start",
            {"type": "message_start", "message": {"usage": {"input_tokens": 21, "cache_read_input_tokens": 4}}},
        ),
        _anthropic_frame(
            "content_block_start",
            {"type": "content_block_start", "index": 0, "content_block": {"type": "text", "text": ""}},
        ),
        _anthropic_frame(
            "content_block_delta",
            {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "Hello "}},
        ),
        _anthropic_frame(
            "content_block_delta",
            {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "world"}},
        ),
        _anthropic_frame("content_block_stop", {"type": "content_block_stop", "index": 0}),
        _anthropic_frame(
            "content_block_start",
            {"type": "content_block_start", "index": 1, "content_block": {"type": "tool_use", "id": "toolu-1", "name": "Glob"}},
        ),
        _anthropic_frame(
            "content_block_delta",
            {"type": "content_block_delta", "index": 1, "delta": {"type": "input_json_delta", "partial_json": '{"pattern":'}},
        ),
        _anthropic_frame(
            "content_block_delta",
            {"type": "content_block_delta", "index": 1, "delta": {"type": "input_json_delta", "partial_json": '"**/*.go"}'}},
        ),
        _anthropic_frame("content_block_stop", {"type": "content_block_stop", "index": 1}),
        _anthropic_frame(
            "message_delta",
            {"type": "message_delta", "usage": {"output_tokens": 9}},
        ),
        _anthropic_frame("message_stop", {"type": "message_stop"}),
    ]
    server, captured = _sse_server(frames, capture={})
    try:
        client = AnthropicClient(
            api_key="test-key",
            base_url=f"http://127.0.0.1:{server.server_port}",
            model="claude-test",
            timeout=5.0,
            max_tokens=1024,
        )
        deltas = asyncio.run(
            _collect(
                client.stream(
                    ChatRequest(
                        model="claude-test",
                        messages=_provider_messages(),
                    )
                )
            )
        )

        text = "".join(d.text for d in deltas if d.kind == "text")
        assert text == "Hello world"
        assert sum(1 for d in deltas if d.kind == "text") >= 2

        tool_delta = next(d for d in deltas if d.kind == "tool_calls")
        assert len(tool_delta.tool_calls) == 1
        call = tool_delta.tool_calls[0]
        assert call.id == "toolu-1"
        assert call.name == "Glob"
        assert call.arguments == {"pattern": "**/*.go"}

        usage_delta = next(d for d in deltas if d.kind == "usage")
        assert usage_delta.usage.input_tokens == 21
        assert usage_delta.usage.output_tokens == 9
        assert usage_delta.usage.cached_input_tokens == 4

        done_delta = next(d for d in deltas if d.kind == "done")
        assert done_delta.thinking_blocks == []
        body = json.loads(captured["body"])
        assert body["stream"] is True
    finally:
        server.shutdown()
        server.server_close()


def test_anthropic_stream_uses_final_message_delta_input_usage() -> None:
    """A compatible endpoint may replace provisional zero usage at stream end."""
    frames = [
        _anthropic_frame(
            "message_start",
            {
                "type": "message_start",
                "message": {
                    "usage": {
                        "input_tokens": 0,
                        "output_tokens": 0,
                        "cache_read_input_tokens": 0,
                    }
                },
            },
        ),
        _anthropic_frame(
            "message_delta",
            {
                "type": "message_delta",
                "usage": {
                    "input_tokens": 550,
                    "output_tokens": 5,
                    "cache_read_input_tokens": 3840,
                },
            },
        ),
        _anthropic_frame("message_stop", {"type": "message_stop"}),
    ]
    server, _ = _sse_server(frames, capture={})
    try:
        client = AnthropicClient(
            api_key="test-key",
            base_url=f"http://127.0.0.1:{server.server_port}",
            model="claude-test",
            timeout=5.0,
        )
        deltas = asyncio.run(
            _collect(
                client.stream(
                    ChatRequest(
                        model="claude-test",
                        messages=[ChatMessage(role="user", content="hello")],
                    )
                )
            )
        )

        usage = next(delta.usage for delta in deltas if delta.kind == "usage")
        assert usage.input_tokens == 550
        assert usage.output_tokens == 5
        assert usage.cached_input_tokens == 3840
    finally:
        server.shutdown()
        server.server_close()


def test_openai_stream_aborts_on_cancel() -> None:
    """A cancel_event set mid-stream makes OpenAIClient.stream() raise
    RequestInterrupted cooperatively (design 22.8 stays intact on the
    streaming path)."""
    # Many small text frames with delays so the cancel lands between chunks.
    frames = [
        _openai_frame({"choices": [{"delta": {"content": "x"}}]}) for _ in range(12)
    ]
    server, _ = _sse_server(frames, delays=[0.05] * 12)
    try:
        client = OpenAIClient(
            api_key="test-key",
            base_url=f"http://127.0.0.1:{server.server_port}",
            model="gpt-4o",
            timeout=5.0,
        )
        cancel = threading.Event()

        async def collect() -> list[StreamDelta]:
            out: list[StreamDelta] = []
            async for delta in client.stream(
                ChatRequest(
                    model="gpt-4o",
                    messages=_provider_messages(),
                    cancel_event=cancel,
                )
            ):
                out.append(delta)
                if len(out) == 1:
                    cancel.set()
            return out

        with pytest.raises(RequestInterrupted):
            asyncio.run(collect())
    finally:
        server.shutdown()
        server.server_close()


# ---------------------------------------------------------------------------
# Multi-model routing (design 22.10): assess_complexity scoring + the fast
# client factory. These cover the routing path that previously had no tests.
# ---------------------------------------------------------------------------

from orchestrator.llm.client import (  # noqa: E402
    COMPLEXITY_FAST_THRESHOLD,
    assess_complexity,
)
from orchestrator.config.env import MODEL_FAST_DEFAULT  # noqa: E402
from orchestrator.llm.providers import build_fast_client  # noqa: E402


def test_assess_complexity_routes_simple_query_to_fast_model() -> None:
    """A short, lookup-style query scores at/below the fast threshold."""
    score = assess_complexity("list files")
    # "list" simple signal + short length -> clamped to the 0.0 floor.
    assert score.score == 0.0
    assert score.score <= COMPLEXITY_FAST_THRESHOLD


def test_assess_complexity_routes_complex_task_to_main_model() -> None:
    """A refactor spanning multiple files scores above the fast threshold."""
    score = assess_complexity(
        "please refactor the authentication module because it spans multiple files"
    )
    # "refactor" (+0.40) + "multiple files" (+0.30) = 0.70, well above threshold.
    assert score.score > COMPLEXITY_FAST_THRESHOLD
    assert score.score == 0.70


def test_assess_complexity_neutral_input_defaults_to_floor() -> None:
    """An input with no complexity signals scores 0.0 and reports 'default'."""
    score = assess_complexity("the quick brown fox jumps over the lazy dog")
    assert score.score == 0.0
    assert score.reason == "default"


def test_assess_complexity_clamps_to_unit_range() -> None:
    """A maximally complex input saturates at 1.0, never above."""
    score = assess_complexity(
        "[plan mode] refactor rewrite restructure across multiple files "
        "implement a new feature agent migrate review"
    )
    assert 0.0 <= score.score <= 1.0
    assert score.score == 1.0


def test_assess_complexity_threshold_boundary_value() -> None:
    """The fast/main boundary is exactly 0.40, and scores below it route fast."""
    assert COMPLEXITY_FAST_THRESHOLD == 0.40
    # A single "migrate" signal on a short prompt stays under the boundary.
    under = assess_complexity("migrate the database now")
    assert under.score < COMPLEXITY_FAST_THRESHOLD
    # Adding a strong "refactor" signal without length penalty lands at/above.
    over = assess_complexity(
        "refactor this please because it really needs to be done carefully "
        "across multiple files"
    )
    assert over.score >= COMPLEXITY_FAST_THRESHOLD


def test_build_fast_client_returns_none_without_api_key(monkeypatch) -> None:
    """No credential -> no fast client (graceful fallback to main model)."""
    monkeypatch.setenv("MODEL_FAST", "gpt-4o-mini")
    monkeypatch.delenv("OPENAI_API_KEY", raising=False)
    monkeypatch.delenv("ANTHROPIC_API_KEY", raising=False)
    assert build_fast_client() is None


def test_build_fast_client_returns_none_for_placeholder_key(monkeypatch) -> None:
    """A still-templated key (e.g. '<your-openai-key>') is treated as missing."""
    monkeypatch.setenv("MODEL_FAST", "gpt-4o-mini")
    monkeypatch.setenv("OPENAI_API_KEY", "<your-openai-key>")
    assert build_fast_client() is None


def test_build_fast_client_returns_none_when_model_disabled(monkeypatch) -> None:
    """MODEL_FAST='' explicitly disables the fast model."""
    monkeypatch.setenv("MODEL_FAST", "")
    monkeypatch.setenv("OPENAI_API_KEY", "sk-test")
    assert build_fast_client() is None


def test_build_fast_client_builds_openai_client_for_gpt_model(monkeypatch) -> None:
    """A gpt-* MODEL_FAST + OPENAI_API_KEY yields a configured OpenAIClient."""
    monkeypatch.setenv("MODEL_FAST", "gpt-4o-mini")
    monkeypatch.setenv("OPENAI_API_KEY", "sk-test-123")
    client = build_fast_client()
    assert isinstance(client, OpenAIClient)
    assert client.model == "gpt-4o-mini"
    assert client.api_key == "sk-test-123"


def test_fast_model_default_matches_supported_relay() -> None:
    """The default fast route must be a model accepted by the configured relay."""
    assert MODEL_FAST_DEFAULT == "gpt-5.5-openai-compact"


def test_build_fast_client_builds_openai_client_for_relay_compact_model(monkeypatch) -> None:
    """Relay compact models use the OpenAI-compatible client path."""
    monkeypatch.setenv("MODEL_FAST", "gpt-5.5-openai-compact")
    monkeypatch.setenv("OPENAI_API_KEY", "relay-test-key")
    monkeypatch.setenv("OPENAI_BASE_URL", "https://relay.example/v1")
    client = build_fast_client()
    assert isinstance(client, OpenAIClient)
    assert client.model == "gpt-5.5-openai-compact"
    assert client.base_url == "https://relay.example/v1"


def test_build_fast_client_builds_anthropic_client_for_claude_model(monkeypatch) -> None:
    """A claude-* MODEL_FAST + ANTHROPIC_API_KEY yields an AnthropicClient."""
    monkeypatch.setenv("MODEL_FAST", "claude-haiku-4-5")
    monkeypatch.setenv("ANTHROPIC_API_KEY", "sk-ant-test")
    monkeypatch.delenv("OPENAI_API_KEY", raising=False)
    client = build_fast_client()
    assert isinstance(client, AnthropicClient)
    assert client.model == "claude-haiku-4-5"


# ---------------------------------------------------------------------------
# Multimodal (design 22.10): image data-URIs in user/assistant messages become
# native provider image blocks. Tool results stay plain text.
# ---------------------------------------------------------------------------

# A tiny but valid base64 payload; the providers only split + forward it.
_PNG_B64 = (
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+M8AAAMBAQDJ/pLvAAAAAElFTkSuQmCC"
)
_PNG_DATA_URI = f"data:image/png;base64,{_PNG_B64}"


def test_anthropic_converts_user_image_data_uri_to_image_block() -> None:
    """An image data-URI in a user message becomes an Anthropic image source block."""
    server, captured = _json_server(
        {"content": [{"type": "text", "text": "ok"}], "usage": {"input_tokens": 1, "output_tokens": 1}}
    )
    try:
        client = AnthropicClient(
            api_key="test-key",
            base_url=f"http://127.0.0.1:{server.server_port}",
            model="claude-test",
            timeout=5.0,
            max_tokens=1024,
        )
        asyncio.run(
            client.chat(
                ChatRequest(
                    model="claude-test",
                    messages=[
                        ChatMessage(
                            role="user",
                            content=f"what is in this picture?\n{_PNG_DATA_URI}",
                        )
                    ],
                )
            )
        )
        body = json.loads(captured["body"])
        user_msg = body["messages"][0]
        assert user_msg["role"] == "user"
        assert isinstance(user_msg["content"], list)
        image_blocks = [b for b in user_msg["content"] if b.get("type") == "image"]
        assert len(image_blocks) == 1, f"expected one image block, got: {user_msg['content']}"
        assert image_blocks[0]["source"] == {
            "type": "base64",
            "media_type": "image/png",
            "data": _PNG_B64,
        }
        # Surrounding text is preserved as a text block.
        text_blocks = [b for b in user_msg["content"] if b.get("type") == "text"]
        assert text_blocks and "what is in this picture" in text_blocks[0]["text"]
    finally:
        server.shutdown()
        server.server_close()


def test_openai_converts_user_image_data_uri_to_image_url_block() -> None:
    """An image data-URI in a user message becomes an OpenAI image_url block."""
    server, captured = _json_server(
        {"choices": [{"message": {"role": "assistant", "content": "ok"}}],
         "usage": {"prompt_tokens": 1, "completion_tokens": 1}}
    )
    try:
        client = OpenAIClient(
            api_key="test-key",
            base_url=f"http://127.0.0.1:{server.server_port}",
            model="gpt-4o",
            timeout=5.0,
        )
        asyncio.run(
            client.chat(
                ChatRequest(
                    model="gpt-4o",
                    messages=[
                        ChatMessage(
                            role="user",
                            content=f"describe this:\n{_PNG_DATA_URI}",
                        )
                    ],
                )
            )
        )
        body = json.loads(captured["body"])
        user_msg = body["messages"][0]
        assert user_msg["role"] == "user"
        assert isinstance(user_msg["content"], list)
        image_blocks = [b for b in user_msg["content"] if b.get("type") == "image_url"]
        assert len(image_blocks) == 1, f"expected one image_url block, got: {user_msg['content']}"
        assert image_blocks[0]["image_url"]["url"] == _PNG_DATA_URI
        text_blocks = [b for b in user_msg["content"] if b.get("type") == "text"]
        assert text_blocks and "describe this" in text_blocks[0]["text"]
    finally:
        server.shutdown()
        server.server_close()


def test_openai_delivers_structured_tool_image_as_followup_user_message() -> None:
    server, captured = _json_server(
        {"choices": [{"message": {"role": "assistant", "content": "ok"}}],
         "usage": {"prompt_tokens": 1, "completion_tokens": 1}}
    )
    try:
        client = OpenAIClient(
            api_key="test-key",
            base_url=f"http://127.0.0.1:{server.server_port}",
            model="gpt-4o",
            timeout=5.0,
        )
        asyncio.run(
            client.chat(
                ChatRequest(
                    model="gpt-4o",
                    messages=[
                        ChatMessage(role="user", content="look"),
                        ChatMessage(
                            role="tool",
                            tool_call_id="c1",
                            name="Read",
                            content=[
                                {"type": "text", "text": "[Image a.png]"},
                                {
                                    "type": "image",
                                    "mime": "image/png",
                                    "data": base64.b64decode(_PNG_B64),
                                },
                            ],
                        ),
                    ],
                )
            )
        )
        body = json.loads(captured["body"])
        tool_msg = body["messages"][1]
        assert tool_msg["role"] == "tool"
        assert tool_msg["content"] == "[Image a.png]"
        visual_msg = body["messages"][2]
        assert visual_msg["role"] == "user"
        image_blocks = [b for b in visual_msg["content"] if b.get("type") == "image_url"]
        assert image_blocks == [{"type": "image_url", "image_url": {"url": _PNG_DATA_URI}}]
    finally:
        server.shutdown()
        server.server_close()


def test_anthropic_delivers_structured_image_inside_tool_result() -> None:
    server, captured = _json_server(
        {"content": [{"type": "text", "text": "ok"}], "usage": {"input_tokens": 1, "output_tokens": 1}}
    )
    try:
        client = AnthropicClient(
            api_key="test-key",
            base_url=f"http://127.0.0.1:{server.server_port}",
            model="claude-test",
            timeout=5.0,
            max_tokens=1024,
        )
        asyncio.run(
            client.chat(
                ChatRequest(
                    model="claude-test",
                    messages=[
                        ChatMessage(role="user", content="look"),
                        ChatMessage(
                            role="tool",
                            tool_call_id="c1",
                            name="Read",
                            content=[
                                {"type": "text", "text": "[Image a.png]"},
                                {
                                    "type": "image",
                                    "mime": "image/png",
                                    "data": base64.b64decode(_PNG_B64),
                                },
                            ],
                        ),
                    ],
                )
            )
        )
        body = json.loads(captured["body"])
        tool_result = body["messages"][1]["content"][0]
        assert tool_result["type"] == "tool_result"
        assert tool_result["tool_use_id"] == "c1"
        assert tool_result["content"] == [
            {"type": "text", "text": "[Image a.png]"},
            {
                "type": "image",
                "source": {
                    "type": "base64",
                    "media_type": "image/png",
                    "data": _PNG_B64,
                },
            },
        ]
    finally:
        server.shutdown()
        server.server_close()


def test_openai_places_all_parallel_tool_results_before_visual_followup() -> None:
    messages = [
        ChatMessage(
            role="assistant",
            content="",
            tool_calls=[
                ToolCall(name="Read", id="c1"),
                ToolCall(name="Read", id="c2"),
            ],
        ),
        ChatMessage(
            role="tool",
            tool_call_id="c1",
            name="Read",
            content=[
                {"type": "text", "text": "first"},
                {"type": "image", "mime": "image/png", "data": base64.b64decode(_PNG_B64)},
            ],
        ),
        ChatMessage(role="tool", tool_call_id="c2", name="Read", content="second"),
    ]

    payloads = OpenAIClient._request_messages(messages)

    assert [payload["role"] for payload in payloads] == ["assistant", "tool", "tool", "user"]
    assert payloads[1]["tool_call_id"] == "c1"
    assert payloads[2]["tool_call_id"] == "c2"
    assert any(block.get("type") == "image_url" for block in payloads[3]["content"])
