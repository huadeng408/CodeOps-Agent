"""Bounded real-provider probe; the only authority arrives on private stdin."""
from __future__ import annotations

import asyncio
import hashlib
import json
import os
import sys
import threading

from google.protobuf.json_format import ParseDict

from codeagent import orchestrator_pb2 as pb
from orchestrator.context.compaction import CompactionRequest, LLMCompactionSummarizer
from orchestrator.llm.client import ChatMessage, ChatRequest
from orchestrator.llm.gateway import HarnessModelClient, ModelGatewayError
from orchestrator.memory.reflection import reflect_memory
from orchestrator.workflows.models import WorkerSpec
from orchestrator.workflows.providers import ProviderWorkerExecutor


def main() -> int:
    supplied = json.load(sys.stdin)
    client = HarnessModelClient(ParseDict(supplied["binding"], pb.ModelGatewayBinding()))
    results = []
    try:
        for purpose in supplied["purposes"]:
            record = {"purpose": purpose, "passed": False}
            results.append(record)
            if purpose == "foreground":
                response = asyncio.run(client.chat(ChatRequest(
                    model=client.model, messages=[ChatMessage("user", "Reply with one short acknowledgement. Do not use tools.")],
                    thinking_enabled=False,
                )))
                record["passed"] = bool(response.text.strip()) and not response.tool_calls
                record["input_tokens"] = response.usage.input_tokens
                record["output_tokens"] = response.usage.output_tokens
                record["cost_status"] = response.model_identity.get("cost_status")
            elif purpose == "memory_reflection":
                text = "A runtime model probe returned a nonempty acknowledgement. No code was modified."
                checksum = hashlib.sha256(text.encode()).hexdigest()
                response = reflect_memory(client, pb.MemoryReflectionRequest(
                    session_id="probe", source_checksum=checksum,
                    sources=[pb.MemorySource(event_id="probe-event", checksum=checksum, text=text)],
                ), threading.Event())
                record["passed"] = not response.error_code
                record["error_code"] = response.error_code
            elif purpose == "compaction":
                summary = LLMCompactionSummarizer(client, model=client.model).summarize(CompactionRequest(
                    prefix_messages=(), messages=(ChatMessage("user", "A bounded model probe completed. No tools were used."),),
                    model=client.model,
                ))
                record["passed"] = bool(summary.strip())
            elif purpose == "workflow_worker":
                result = asyncio.run(ProviderWorkerExecutor({"default": client})(
                    WorkerSpec("probe-worker", "Runtime probe", "Reply with one short acknowledgement. Do not use tools."), {},
                ))
                record["passed"] = bool(result.output.strip())
            else:
                raise ValueError("unsupported probe purpose")
            if not record["passed"]:
                break
    except ModelGatewayError as error:
        results[-1]["error_code"] = error.code
    except Exception:
        if results:
            results[-1]["error_code"] = "probe_runtime_error"
    finally:
        client.close()
    passed = len(results) == len(supplied["purposes"]) and all(item["passed"] for item in results)
    print(json.dumps({"pid": os.getpid(), "results": results, "passed": passed}))
    return 0 if passed else 1


if __name__ == "__main__":
    sys.exit(main())
