import json
import threading

from codeagent import orchestrator_pb2 as pb
from orchestrator.llm.client import ChatResponse
from orchestrator.memory.reflection import reflect_memory


class ReflectionModel:
    model = "fixture-reflector"

    def __init__(self, payload):
        self.payload = payload
        self.request = None

    async def chat(self, request):
        self.request = request
        return ChatResponse(text=json.dumps(self.payload))


def sources():
    return pb.MemoryReflectionRequest(
        session_id="session", source_checksum="a" * 64,
        sources=[pb.MemorySource(event_id="source-1", checksum="b" * 64, text="Prefer verified evidence")],
    )


def candidate():
    return {"kind": "preferences", "key": "verified-evidence", "abstract": "Prefer evidence",
            "overview": "Prefer source-verified evidence", "content": "Keep sources and failure conditions.",
            "source_event_ids": ["source-1"]}


def test_reflection_uses_model_without_tools_and_validates_provenance():
    model = ReflectionModel({"candidates": [candidate()]})
    result = reflect_memory(model, sources(), threading.Event())
    assert not result.error_code and result.candidates[0].kind == "preferences"
    assert model.request.allow_tools is False and model.request.tools == []
    assert model.request.purpose == "memory_reflection"
    assert result.source_checksum == "a" * 64


def test_reflection_rejects_untrusted_reference_and_credentials():
    for field, value in (("source_event_ids", ["foreign"]), ("content", "password=" + "fixture-secret")):
        item = candidate()
        item[field] = value
        result = reflect_memory(ReflectionModel({"candidates": [item]}), sources(), threading.Event())
        assert result.error_code and not result.candidates


def test_missing_provider_does_not_invent_semantic_memories():
    result = reflect_memory(None, sources(), threading.Event())
    assert result.error_code == "reflection_provider_unavailable"
    assert not result.candidates
