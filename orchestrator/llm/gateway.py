"""One-way LLM adapter: provider strategy in Python, paid transport in Go."""
from __future__ import annotations

import asyncio
import json
import uuid

import grpc

from codeagent import orchestrator_pb2 as pb
from codeagent import orchestrator_pb2_grpc as rpc
from .client import ChatRequest, ChatResponse, LLMClient, RequestInterrupted, Usage
from .providers.anthropic import AnthropicClient
from .providers.openai import OpenAIClient


class ModelGatewayError(RuntimeError):
    def __init__(self, code: str):
        if code not in {
            "model_prerequisites_missing", "model_gateway_binding_invalid", "model_gateway_unavailable",
            "model_gateway_response_invalid", "provider_request_invalid", "provider_transport_unknown",
            "provider_response_unknown", "provider_http_unknown", "provider_rate_limited_unknown",
            "provider_transient_unknown", "provider_authentication_unknown", "provider_usage_unknown",
            "provider_bound_violated", "provider_settlement_unknown", "token_budget_exhausted",
            "token_usage_unknown", "code_task_busy", "model_concurrency_exhausted",
            "model_call_already_reserved", "code_task_closed", "token_admission_unavailable",
        }:
            code = "model_gateway_unavailable"
        self.code = code
        super().__init__(code)


class HarnessModelClient(LLMClient):
    # shortcut: the admitted transport emits one chunk; add metered SSE when
    # this migration needs incremental provider output.
    max_retries = 0  # Each upstream attempt is admitted and accounted by Go.
    cost_status = "unknown"
    harness_admitted = True

    def __init__(self, binding: pb.ModelGatewayBinding):
        host, _, port = binding.address.rpartition(":")
        if (binding.schema_version != 1 or host != "127.0.0.1"
                or not port.isdigit() or not 0 < int(port) < 65536
                or len(binding.capability) != 32 or not binding.model
                or binding.protocol not in {"openai", "anthropic"}):
            raise ModelGatewayError("model_gateway_binding_invalid")
        self.model = binding.model
        self.protocol = binding.protocol
        self._capability = bytes(binding.capability)
        self._channel = grpc.insecure_channel(binding.address, options=[
            ("grpc.max_send_message_length", 8 << 20),
            ("grpc.max_receive_message_length", 16 << 20),
        ])
        self._stub = rpc.ModelGatewayStub(self._channel)
        factory = AnthropicClient if binding.protocol == "anthropic" else OpenAIClient
        self._adapter = factory(api_key="", model=self.model, max_retries=0)
        if binding.output_limit > 0:
            self._adapter.max_tokens = binding.output_limit

    def close(self):
        self._channel.close()
        self._capability = b""

    async def chat(self, request: ChatRequest) -> ChatResponse:
        return await asyncio.to_thread(self._chat_sync, request)

    def _chat_sync(self, request: ChatRequest) -> ChatResponse:
        cancel = request.cancel_event
        if cancel is not None and cancel.is_set():
            raise RequestInterrupted("model request canceled before admission")
        payload = self._adapter._stream_payload(request)
        payload["stream"] = False
        payload.pop("stream_options", None)
        call = pb.ModelCallRequest(capability=self._capability, call_id=uuid.uuid4().hex,
                                   payload_json=json.dumps(payload).encode(),
                                   purpose=request.purpose or "foreground")
        future = self._stub.Invoke.future(call, timeout=65)
        try:
            while True:
                if cancel is not None and cancel.is_set():
                    future.cancel()
                    raise RequestInterrupted("model request canceled; Go retains unknown usage")
                try:
                    result = future.result(timeout=0.1)
                    break
                except grpc.FutureTimeoutError:
                    continue
        except grpc.RpcError:
            raise ModelGatewayError("model_gateway_unavailable") from None
        if result.error_code:
            raise ModelGatewayError(result.error_code)
        try:
            response = self._adapter._parse_response(json.loads(result.payload_json), requested_model=request.model or self.model)
        except Exception:
            raise ModelGatewayError("model_gateway_response_invalid") from None
        response.usage = Usage(input_tokens=result.input_tokens, output_tokens=result.output_tokens,
                               cached_input_tokens=result.cached_input_tokens)
        response.model_identity["cost_status"] = result.cost_status or "unknown"
        return response
