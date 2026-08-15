"""Bounded Terminal-Bench agent with auditable terminal-feedback turns."""

from __future__ import annotations

import contextlib
import json
import os
import re
from pathlib import Path

from eval.harness.official_agent_trace import receipt_agent_trace
from terminal_bench.agents.base_agent import AgentResult, BaseAgent
from terminal_bench.agents.failure_mode import FailureMode
from terminal_bench.terminal.tmux_session import TmuxSession


class DeepSeekTBAgent(BaseAgent):
    """Run bounded command-planning turns without exposing protected assets."""

    _MAX_TURNS = 3
    _MAX_COMMANDS_PER_TURN = 8
    _PROTECTED_PATH_MARKERS = ("/tests", "/solution", "/logs/verifier", "protected.tar")

    @staticmethod
    def name() -> str:
        return "deepseek-tb-agent"

    def __init__(self, **kwargs: object) -> None:
        super().__init__(**kwargs)
        self._api_key = str(kwargs.get("api_key") or os.environ.get("LOCAL_LLM_API_KEY", ""))
        self._model = str(kwargs.get("model") or "deepseek-chat")
        self._base_url = str(kwargs.get("base_url") or "https://api.deepseek.com/v1")
        self._wire_api = str(kwargs.get("wire_api") or "chat_completions")
        requested_turns = int(kwargs.get("max_turns") or 2)
        self._max_turns = min(max(requested_turns, 1), self._MAX_TURNS)
        self._temperature = 1.0 if self._model.startswith("gpt-5") else 0.0

    def perform_task(
        self,
        instruction: str,
        session: TmuxSession,
        logging_dir: Path | None = None,
    ) -> AgentResult:
        rendered = self._render_instruction(instruction)
        system_prompt = self._system_prompt()
        total_input_tokens = 0
        total_output_tokens = 0
        feedback = ""
        transcript: list[dict[str, object]] = []

        trace = receipt_agent_trace()
        agent_scope = trace.agent() if trace is not None else contextlib.nullcontext()
        with agent_scope:
            for turn in range(1, self._max_turns + 1):
                turn_instruction = rendered
                if feedback:
                    turn_instruction += f"\n\nTerminal feedback from your previous commands:\n{feedback}"
                output_text, tokens_in, tokens_out = self._request_commands(
                    turn_instruction, system_prompt
                )
                total_input_tokens += tokens_in
                total_output_tokens += tokens_out
                commands = self._command_lines(output_text)
                if not commands or any(self._is_protected_content(command) for command in commands):
                    self._write_transcript(logging_dir, transcript)
                    return AgentResult(
                        total_input_tokens=total_input_tokens,
                        total_output_tokens=total_output_tokens,
                        failure_mode=FailureMode.FATAL_LLM_PARSE_ERROR,
                    )

                for command in commands:
                    session.send_keys([command, "Enter"], block=True, max_timeout_sec=120)
                feedback = session.get_incremental_output()
                transcript.append(
                    {"turn": turn, "commands": commands, "terminal_output": feedback}
                )
                if self._is_protected_content(feedback):
                    self._write_transcript(logging_dir, transcript)
                    return AgentResult(
                        total_input_tokens=total_input_tokens,
                        total_output_tokens=total_output_tokens,
                        failure_mode=FailureMode.FATAL_LLM_PARSE_ERROR,
                    )

        self._write_transcript(logging_dir, transcript)
        return AgentResult(
            total_input_tokens=total_input_tokens,
            total_output_tokens=total_output_tokens,
            failure_mode=FailureMode.NONE,
        )

    @staticmethod
    def _system_prompt() -> str:
        return (
            "You are an expert software engineer working in a Linux container via tmux. "
            "Solve the task by issuing shell commands. You have a bounded number of turns; "
            "after each turn, inspect the supplied terminal feedback and continue. "
            "Never read /tests, /solution, /logs/verifier, or protected task assets. "
            "Do not use heredocs because tmux sends each line separately. "
            "Output only one bash code block. Each command must be one complete shell line."
        )

    def _request_commands(
        self, instruction: str, system_prompt: str = ""
    ) -> tuple[str, int, int]:
        """Request one command plan via the configured OpenAI-compatible API."""
        from openai import OpenAI

        client = OpenAI(api_key=self._api_key, base_url=self._base_url)
        trace = receipt_agent_trace()
        chat_scope = trace.chat() if trace is not None else contextlib.nullcontext()
        with chat_scope:
            if self._wire_api == "responses":
                response = client.responses.create(
                    model=self._model,
                    input=[
                        {"role": "developer", "content": system_prompt},
                        {"role": "user", "content": f"Task:\n\n{instruction}"},
                    ],
                    max_output_tokens=4096,
                )
                usage = getattr(response, "usage", None)
                return (
                    getattr(response, "output_text", "") or "",
                    int(getattr(usage, "input_tokens", 0) or 0),
                    int(getattr(usage, "output_tokens", 0) or 0),
                )

            response = client.chat.completions.create(
                model=self._model,
                messages=[
                    {"role": "system", "content": system_prompt},
                    {"role": "user", "content": f"Task:\n\n{instruction}"},
                ],
                temperature=self._temperature,
                max_tokens=4096,
            )
            usage = response.usage
            return (
                response.choices[0].message.content or "",
                int(getattr(usage, "prompt_tokens", 0) or 0),
                int(getattr(usage, "completion_tokens", 0) or 0),
            )

    @classmethod
    def _command_lines(cls, output_text: str) -> list[str]:
        commands = cls._extract_commands(output_text)
        lines = [
            line.strip()
            for line in commands.splitlines()
            if line.strip() and not line.lstrip().startswith("#") and not line.startswith("```")
        ]
        return lines[: cls._MAX_COMMANDS_PER_TURN]

    @classmethod
    def _is_protected_content(cls, value: str) -> bool:
        lowered = value.lower()
        return any(marker in lowered for marker in cls._PROTECTED_PATH_MARKERS)

    @staticmethod
    def _write_transcript(logging_dir: Path | None, turns: list[dict[str, object]]) -> None:
        if logging_dir is None:
            return
        logging_dir.mkdir(parents=True, exist_ok=True)
        (logging_dir / "agent-transcript.json").write_text(
            json.dumps({"turns": turns}, indent=2, ensure_ascii=False) + "\n",
            encoding="utf-8",
        )

    @staticmethod
    def _extract_commands(text: str) -> str:
        """Extract a bash code block or conservative raw shell-command lines."""
        block = re.search(r"```(?:bash|sh)\n(.*?)```", text, re.DOTALL)
        if block:
            return block.group(1).strip()
        block = re.search(r"```\n?(.*?)```", text, re.DOTALL)
        if block:
            return block.group(1).strip()
        accepted = []
        for line in text.splitlines():
            line = line.strip()
            if re.match(
                r"^(?:timeout\s+\d+\s+)?(sudo |cat |echo |printf |python[23]? |cd |bash |mkdir |pip |apt |npm |node |git |curl |wget |cp |mv |rm |docker |ls |pwd |grep |find |sed |awk |chmod |make |gcc |g\+\+|\./)",
                line,
            ):
                accepted.append(line)
        return "\n".join(accepted)
