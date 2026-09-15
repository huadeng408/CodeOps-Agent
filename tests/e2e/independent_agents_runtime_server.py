from __future__ import annotations

import argparse
import json
import threading
from pathlib import Path

import orchestrator.server as server_module
from orchestrator.llm.client import ChatResponse, ToolCall, Usage, message_content_text
from orchestrator.server import OrchestratorServer, ServerConfig


class IndependentAgentModel:
    """Deterministic model double, not a provider or benchmark scorer."""

    model = "independent-agent-fixture"

    def __init__(self, observations: Path) -> None:
        self.observations = observations
        self.lock = threading.Lock()

    def record(self, **values) -> None:
        with self.lock:
            with self.observations.open("a", encoding="utf-8") as stream:
                stream.write(json.dumps(values, sort_keys=True) + "\n")

    @staticmethod
    def call(tool_name: str, call_id: str, **arguments) -> ChatResponse:
        return ChatResponse(
            tool_calls=[ToolCall(name=tool_name, id=call_id, arguments=arguments,
                                 arguments_json=json.dumps(arguments, sort_keys=True))],
            usage=Usage(input_tokens=1, output_tokens=1),
        )

    @staticmethod
    def output(message):
        text = message_content_text(message.content)
        if "[Untrusted tool output]\n" in text:
            text = text.partition("[Untrusted tool output]\n")[2]
        return json.loads(text)

    async def chat(self, request):
        if request.purpose == "memory_reflection":
            assert request.tools == [] and not request.allow_tools
            sources = json.loads(message_content_text(request.messages[-1].content))
            self.record(reflection_without_tools=True)
            return ChatResponse(text=json.dumps({"candidates": [{
                "kind": "patterns", "key": "agents/independent-history",
                "abstract": "Keep independent agent history recoverable",
                "overview": "Keep independent agent history and provenance recoverable across process restart.",
                "content": "Independent task history is recovered from its child Session Ledger, without parent history.",
                "source_event_ids": [sources[-1]["event_id"]],
            }]}), usage=Usage(input_tokens=1, output_tokens=1))
        rendered = "\n".join(message_content_text(m.content) for m in request.messages)
        user_index = max(i for i, m in enumerate(request.messages) if m.role == "user")
        latest_user = message_content_text(request.messages[user_index].content)
        tool_messages = [m for m in request.messages[user_index + 1:] if m.role == "tool"]
        is_child = "You are an independent child agent." in rendered
        if is_child:
            assert "PARENT_PRIVATE_HISTORY" not in rendered
            assert "PARENT_FILE_MEMORY" not in rendered
            assert not any(t["function"]["name"] == "SpawnAgent" for t in request.tools)
            assert "EXPLICIT_CHILD_MATERIAL" in rendered
            resumed = "Continue after process restart" in latest_user
            self.record(child_context_isolated=True, restart_history=resumed and "Use branch main" in rendered)
            if "Use branch main" not in rendered:
                return self.call("AskUser", "child-ask", question="Which branch?", options=["main"])
            if not any(m.name == "Skill" for m in tool_messages):
                assert "SKILL_BODY_LAZY_MARKER" not in str(request.tools)
                self.record(skill_metadata_only=True)
                return self.call("Skill", "child-skill", name="case-inspect")
            if not any(m.name == "PublishArtifact" for m in tool_messages):
                assert "SKILL_BODY_LAZY_MARKER" in rendered
                return self.call("PublishArtifact", "child-file", artifact={
                    "name": "Independent source report",
                    "parts": [{"file": {"path": "report.txt", "media_type": "text/plain"}},
                              {"data_json": json.dumps({"branch": "main", "restarted": resumed})}],
                })
            artifact = self.output(next(m for m in reversed(tool_messages) if m.name == "PublishArtifact"))
            assert len(artifact["checksum"]) == 64 and artifact["parts"][0]["file"]["sha256"]
            self.record(file_artifact_pinned=True)
            return ChatResponse(text="CHILD_INDEPENDENT_REPORT", usage=Usage(input_tokens=1, output_tokens=1))
        if "RESUME_AGENT_CASE" in latest_user:
            if not tool_messages:
                return self.call("AgentTask", "parent-list-restart", action="list")
            if tool_messages[-1].tool_call_id == "parent-list-restart":
                task = self.output(tool_messages[-1])[0]
                self.record(task_id_after_restart=task["id"])
                return self.call("AgentTask", "parent-message-restart", action="message", task_id=task["id"],
                                 message={"parts": [{"text": "Continue after process restart"}]})
        elif not tool_messages:
            return self.call("SpawnAgent", "parent-spawn", kind="explore", title="Inspect sources",
                             objective="Inspect explicit sources; ask which branch to use.",
                             context={"material": "EXPLICIT_CHILD_MATERIAL"})
        task = self.output(tool_messages[-1])
        if task["status"] == "input_required":
            self.record(task_input_required=True)
            return self.call("AgentTask", "parent-message", action="message", task_id=task["id"],
                             message={"parts": [{"text": "Use branch main"},
                                                {"data_json": json.dumps({"scope": "source"})}]})
        if task["status"] != "completed":
            assert task["status"] not in {"failed", "canceled"}
            wait_call_id = "parent-wait-restart" if "RESUME_AGENT_CASE" in latest_user else "parent-wait-" + str(len(tool_messages))
            return self.call("AgentTask", wait_call_id, action="wait", task_id=task["id"])
        assert task["artifacts"] and task["working_dir"]
        self.record(completed_task_id=task["id"])
        return ChatResponse(text="PARENT_DELEGATION_COMPLETED", usage=Usage(input_tokens=1, output_tokens=1))


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, required=True)
    parser.add_argument("--project-root", type=Path, required=True)
    parser.add_argument("--observations", type=Path, required=True)
    args = parser.parse_args()
    # This fixture never reads dotenv or approved local provider files.
    server_module.load_dotenv = lambda: None
    root = args.project_root.resolve()
    app = OrchestratorServer(ServerConfig(port=args.port, project_root=str(root), working_dir=str(root),
                                         memory_dir=str(root / ".agent" / "fixture-memory")))
    model = IndependentAgentModel(args.observations)
    app.llm = model
    app.fast_llm = model
    app.provider_clients = {"default": model}
    app.provider_router = None
    app.serve()


if __name__ == "__main__":
    main()
