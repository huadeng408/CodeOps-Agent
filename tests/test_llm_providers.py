from __future__ import annotations

import asyncio
import json
import threading
import urllib.error
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from orchestrator.llm.client import ChatMessage, ChatRequest, ToolCall
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


def test_anthropic_client_uses_tool_use_payload() -> None:
    server, captured = _json_server(
        {
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
