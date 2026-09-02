"""E2 gate: the provider's *self-reported* model identity must be captured.

The E2 model-identity gate requires:

    记录非敏感 provider/base URL 摘要以及响应中的 model/provider/revision/
    request ID。若 provider 不提供不可变 revision，状态必须为
    `MODEL_IDENTITY_UNVERIFIED`。

§20.4 records the concrete failure this gate exists to prevent: the Sol review
claimed "GPT-5.6 Sol 完成双轮复核", but the only evidence was the *requested*
model name (`gpt-5.6-sol`) echoed back from our own CLI argument, with
`revision="unknown"`.  The provider's own answer to "which model actually
served this request?" was never read, because
:meth:`OpenAIClient._parse_response` discarded ``payload["model"]``,
``payload["id"]`` and ``payload["system_fingerprint"]``.

A requested model name is an *input*, not evidence.  Only the response body
can testify to what served the request.  These tests pin that:

1. :class:`ChatResponse` must carry the provider-reported identity.
2. ``OpenAIClient`` must populate it from the raw payload.
3. A missing/blank provider identity must be representable and must NOT be
   silently backfilled with the requested model name.
4. The captured identity must never carry the API key.

All tests are offline: a local ``http.server`` stands in for the provider, so
no key, no network and no spend are involved.
"""

from __future__ import annotations

import asyncio
import json
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

PROJECT_ROOT = Path(__file__).resolve().parents[2]
if str(PROJECT_ROOT) not in sys.path:
    sys.path.insert(0, str(PROJECT_ROOT))

from orchestrator.llm.client import ChatMessage, ChatRequest, ChatResponse  # noqa: E402
from orchestrator.llm.providers import OpenAIClient  # noqa: E402


def _json_server(response_payload: dict[str, object]):
    """Minimal OpenAI-compatible stub that returns a fixed payload."""

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self) -> None:  # noqa: N802
            length = int(self.headers.get("Content-Length", "0"))
            self.rfile.read(length)
            encoded = json.dumps(response_payload).encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(encoded)))
            self.end_headers()
            self.wfile.write(encoded)

        def log_message(self, format: str, *args) -> None:  # noqa: A003
            return

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    return server


def _chat(payload: dict[str, object], requested_model: str) -> ChatResponse:
    server = _json_server(payload)
    try:
        port = server.server_address[1]
        client = OpenAIClient(
            api_key="test-key-not-a-real-secret",
            model=requested_model,
            base_url=f"http://127.0.0.1:{port}/v1",
        )
        return asyncio.run(
            client.chat(
                ChatRequest(
                    model=requested_model,
                    messages=[ChatMessage(role="user", content="ping")],
                )
            )
        )
    finally:
        server.shutdown()
        server.server_close()


def _payload(**identity: object) -> dict[str, object]:
    base: dict[str, object] = {
        "choices": [{"message": {"role": "assistant", "content": "pong"}}],
        "usage": {"prompt_tokens": 3, "completion_tokens": 1},
    }
    base.update(identity)
    return base


class TestChatResponseCarriesIdentity:
    """The transport object must have somewhere to put the evidence."""

    def test_chat_response_has_model_identity_field(self):
        resp = ChatResponse()
        assert hasattr(resp, "model_identity"), (
            "ChatResponse has no model_identity field, so the provider's "
            "self-reported model can never reach an artifact (E2 gate)"
        )

    def test_model_identity_defaults_to_empty_not_none(self):
        ident = ChatResponse().model_identity
        assert isinstance(ident, dict)
        assert ident == {}


class TestOpenAIClientCapturesProviderIdentity:
    """The response body — not our request — is the evidence."""

    def test_reported_model_is_read_from_payload(self):
        resp = _chat(
            _payload(model="deepseek-chat-v4-pro-0722", id="chatcmpl-abc123"),
            requested_model="gpt-5.6-sol",
        )
        assert resp.model_identity.get("reported_model") == "deepseek-chat-v4-pro-0722"

    def test_reported_model_differs_from_requested_model(self):
        """The exact §20.4 failure: requested name masquerading as evidence."""
        resp = _chat(
            _payload(model="deepseek-chat", id="chatcmpl-xyz"),
            requested_model="gpt-5.6-sol",
        )
        assert resp.model_identity.get("requested_model") == "gpt-5.6-sol"
        assert resp.model_identity.get("reported_model") == "deepseek-chat"
        assert resp.model_identity["reported_model"] != resp.model_identity["requested_model"]

    def test_request_id_is_captured(self):
        resp = _chat(
            _payload(model="deepseek-chat", id="chatcmpl-req-42"),
            requested_model="deepseek-chat",
        )
        assert resp.model_identity.get("response_id") == "chatcmpl-req-42"

    def test_system_fingerprint_is_captured_when_present(self):
        resp = _chat(
            _payload(
                model="deepseek-chat",
                id="chatcmpl-1",
                system_fingerprint="fp_abc123",
            ),
            requested_model="deepseek-chat",
        )
        assert resp.model_identity.get("system_fingerprint") == "fp_abc123"

    def test_created_timestamp_is_captured_when_present(self):
        resp = _chat(
            _payload(model="deepseek-chat", id="c1", created=1754700000),
            requested_model="deepseek-chat",
        )
        assert resp.model_identity.get("created") == 1754700000


class TestFailClosedOnMissingIdentity:
    """A provider that says nothing must produce an honest blank, not a guess."""

    def test_missing_model_field_does_not_fall_back_to_requested(self):
        resp = _chat(_payload(id="chatcmpl-no-model"), requested_model="gpt-5.6-sol")
        assert resp.model_identity.get("reported_model", "") == "", (
            "a missing provider model must stay blank; backfilling the "
            "requested name recreates the §20.4 false-identity claim"
        )

    def test_verified_flag_false_without_immutable_revision(self):
        """No system_fingerprint => identity is not immutably pinned."""
        resp = _chat(
            _payload(model="deepseek-chat", id="c1"),
            requested_model="deepseek-chat",
        )
        assert resp.model_identity.get("identity_verified") is False

    def test_verified_flag_true_with_fingerprint_and_model(self):
        resp = _chat(
            _payload(model="deepseek-chat", id="c1", system_fingerprint="fp_9"),
            requested_model="deepseek-chat",
        )
        assert resp.model_identity.get("identity_verified") is True


class TestIdentityNeverLeaksSecrets:
    """Identity is an artifact field; it must be safe to write to disk."""

    def test_api_key_absent_from_identity(self):
        resp = _chat(
            _payload(model="deepseek-chat", id="c1", system_fingerprint="fp_9"),
            requested_model="deepseek-chat",
        )
        blob = json.dumps(resp.model_identity)
        assert "test-key-not-a-real-secret" not in blob
        assert "api_key" not in blob
        assert "authorization" not in blob.lower()

    def test_identity_is_json_serializable(self):
        resp = _chat(
            _payload(model="deepseek-chat", id="c1"),
            requested_model="deepseek-chat",
        )
        json.dumps(resp.model_identity)
