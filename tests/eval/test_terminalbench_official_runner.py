"""Contract tests for the pinned Terminal-Bench official receipt boundary."""

from __future__ import annotations

import hashlib
import json
from pathlib import Path


def _write_dataset(root: Path) -> None:
    task = root / "tasks" / "fixed-task"
    task.mkdir(parents=True)
    (root / "terminalbench_2.jsonl").write_text(
        '{"name": "fixed-task", "description": "fixture"}\n', encoding="utf-8"
    )


def _write_official_run(root: Path, *, resolved: bool) -> None:
    root.mkdir(parents=True)
    (root / "results.json").write_text(
        json.dumps({"results": [{"task_id": "fixed-task", "is_resolved": resolved}]}),
        encoding="utf-8",
    )
    (root / "run_metadata.json").write_text(
        json.dumps({"agent_name": "fixture-agent", "n_concurrent_trials": 1}),
        encoding="utf-8",
    )


def test_terminalbench_official_runner_copies_raw_results_and_marks_failure(tmp_path: Path) -> None:
    from eval.benchmarks.terminalbenchofficial import (
        TerminalBenchOfficialConfig,
        TerminalBenchOfficialRunner,
    )

    dataset = tmp_path / "dataset"
    _write_dataset(dataset)
    source_hash = hashlib.sha256((dataset / "terminalbench_2.jsonl").read_bytes()).hexdigest()
    raw_run = tmp_path / "upstream-run"
    _write_official_run(raw_run, resolved=False)
    runner = TerminalBenchOfficialRunner(
        TerminalBenchOfficialConfig(
            dataset_root=dataset,
            dataset_sha256=source_hash,
            package_version="0.2.18",
            model="openai/gpt-5.6-sol",
            task_id="fixed-task",
        )
    )

    receipt = runner.collect_receipt(raw_run, tmp_path / "receipt")

    copied = tmp_path / "receipt" / "scorer" / "terminalbench-results.json"
    assert copied.read_bytes() == (raw_run / "results.json").read_bytes()
    assert receipt["status"] == "OFFICIAL_FAILURE"
    assert receipt["task_ids"] == ["fixed-task"]
    assert receipt["official_output_sha256"] == hashlib.sha256(copied.read_bytes()).hexdigest()
    assert (tmp_path / "receipt" / "checksums.sha256").is_file()


def test_terminalbench_official_runner_rejects_invalid_model_and_concurrency(tmp_path: Path) -> None:
    from eval.benchmarks.terminalbenchofficial import (
        TerminalBenchOfficialConfig,
        TerminalBenchOfficialRunner,
    )

    runner = TerminalBenchOfficialRunner(
        TerminalBenchOfficialConfig(
            dataset_root=tmp_path,
            dataset_sha256="a" * 64,
            package_version="0.2.18",
            model="gpt-5.6-sol",
            task_id="fixed-task",
            max_concurrency=11,
        )
    )

    assert runner.validate() == [
        "model must include a LiteLLM provider prefix",
        "max_concurrency must be between 1 and 10",
    ]


def test_terminalbench_agent_uses_responses_wire_api_without_chat_completion(monkeypatch) -> None:
    from eval.swebench_work.deepseek_tb_agent import DeepSeekTBAgent

    calls: list[tuple[str, dict[str, object]]] = []

    class FakeResponses:
        def create(self, **kwargs):
            calls.append(("responses", kwargs))
            return type(
                "Response",
                (),
                {
                    "output_text": "```bash\necho solved > /app/out.html\n```",
                    "usage": type("Usage", (), {"input_tokens": 7, "output_tokens": 9})(),
                },
            )()

    class FakeChatCompletions:
        def create(self, **kwargs):  # pragma: no cover - must never be selected
            raise AssertionError("chat completions must not be used for responses wire API")

    class FakeClient:
        responses = FakeResponses()
        chat = type("Chat", (), {"completions": FakeChatCompletions()})()

    monkeypatch.setattr("openai.OpenAI", lambda **kwargs: FakeClient())
    agent = DeepSeekTBAgent(model="gpt-5.6-sol", wire_api="responses", api_key="x", base_url="https://example.test/v1")

    output, tokens_in, tokens_out = agent._request_commands(
        "do the task", "return only a bash command block"
    )

    assert output.startswith("```bash")
    assert (tokens_in, tokens_out) == (7, 9)
    assert calls == [
        (
            "responses",
            {
                "model": "gpt-5.6-sol",
                "input": [
                    {"role": "developer", "content": "return only a bash command block"},
                    {"role": "user", "content": "Task:\n\ndo the task"},
                ],
                "max_output_tokens": 4096,
            },
        )
    ]


def test_terminalbench_agent_multiturn_mode_replays_terminal_feedback_and_writes_transcript(
    monkeypatch, tmp_path: Path
) -> None:
    from eval.swebench_work.deepseek_tb_agent import DeepSeekTBAgent

    responses = iter([
        ("```bash\nprintf 'first'\n```", 10, 11),
        ("```bash\nprintf 'second'\n```", 12, 13),
    ])
    prompts: list[str] = []

    class FakeSession:
        def __init__(self) -> None:
            self.commands: list[object] = []
            self.outputs = iter(["Current Terminal Screen:\nfirst\n", "New Terminal Output:\nsecond\n"])

        def send_keys(self, keys, **kwargs) -> None:
            self.commands.append(keys)

        def get_incremental_output(self) -> str:
            return next(self.outputs)

    agent = DeepSeekTBAgent(
        model="gpt-5.6-sol",
        wire_api="responses",
        max_turns=2,
    )

    def fake_request(instruction: str, system_prompt: str = "") -> tuple[str, int, int]:
        prompts.append(instruction)
        return next(responses)

    monkeypatch.setattr(agent, "_request_commands", fake_request)
    session = FakeSession()

    result = agent.perform_task("solve the task", session, logging_dir=tmp_path)

    assert result.total_input_tokens == 22
    assert result.total_output_tokens == 24
    assert len(prompts) == 2
    assert "Terminal feedback" in prompts[1]
    assert "first" in prompts[1]
    assert session.commands == [
        ["printf 'first'", "Enter"],
        ["printf 'second'", "Enter"],
    ]
    transcript = json.loads((tmp_path / "agent-transcript.json").read_text(encoding="utf-8"))
    assert transcript["turns"] == [
        {"turn": 1, "commands": ["printf 'first'"], "terminal_output": "Current Terminal Screen:\nfirst\n"},
        {"turn": 2, "commands": ["printf 'second'"], "terminal_output": "New Terminal Output:\nsecond\n"},
    ]


def test_terminalbench_agent_multiturn_mode_rejects_protected_path_commands(monkeypatch) -> None:
    from eval.swebench_work.deepseek_tb_agent import DeepSeekTBAgent
    from terminal_bench.agents.failure_mode import FailureMode

    agent = DeepSeekTBAgent(max_turns=1)
    monkeypatch.setattr(
        agent,
        "_request_commands",
        lambda instruction, system_prompt="": ("```bash\ncat /tests/test_outputs.py\n```", 3, 4),
    )

    result = agent.perform_task("solve the task", object())

    assert result.failure_mode is FailureMode.FATAL_LLM_PARSE_ERROR
