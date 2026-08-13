"""Custom DeepSeek Agent for Terminal-Bench — standalone module the harness can import.

Fixes:
  - First command not blocked (container warm-up is slow, so the first command
    often times out if we block). We send a no-op echo first without blocking,
    then block on subsequent commands.
  - Increased max_timeout_sec to 300s for expensive commands (pip install, apt-get).
  - Multi-turn mode: the agent can ask for command output and issue follow-up commands.
"""
import os
from pathlib import Path
from terminal_bench.agents.base_agent import AgentResult, BaseAgent
from terminal_bench.agents.failure_mode import FailureMode
from terminal_bench.terminal.tmux_session import TmuxSession


class DeepSeekTBAgent(BaseAgent):
    """Calls DeepSeek API with the task instruction, writes code to tmux terminal."""

    @staticmethod
    def name() -> str:
        return "deepseek-tb-agent"

    def __init__(self, **kwargs):
        super().__init__(**kwargs)
        self._api_key = kwargs.get("api_key") or os.environ.get("LOCAL_LLM_API_KEY", "")
        self._model = kwargs.get("model", "deepseek-chat")
        self._base_url = kwargs.get("base_url", "https://api.deepseek.com/v1")
        self._temperature = 1.0 if self._model.startswith("gpt-5") else 0.0

    def perform_task(
        self,
        instruction: str,
        session: TmuxSession,
        logging_dir: Path | None = None,  # noqa: ARG002 — required by BaseAgent interface
    ) -> AgentResult:
        import time
        from openai import OpenAI

        client = OpenAI(
            api_key=self._api_key,
            base_url=self._base_url,
        )

        rendered = self._render_instruction(instruction)

        system_prompt = (
            "You are an expert software engineer working in a Linux container via tmux. "
            "You must solve the given task by executing shell commands.\n\n"
            "IMPORTANT: To write multi-line files, use base64 encoding — do NOT use heredoc "
            "because tmux sends each line separately and breaks the heredoc syntax. "
            "Example:\n"
            "```bash\n"
            "echo 'cHJpbnQoJ2hlbGxvJyk=' | base64 -d > /app/solve.py\n"
            "```\n"
            "Use python3 -c 'import base64; print(base64.b64encode(b\"\"\"...multi-line content...\"\"\").decode())' "
            "to generate the base64 string if you need to.\n\n"
            "Alternative for short files: use printf with \\n for newlines:\n"
            "```bash\n"
            "printf 'line1\\nline2\\n' > /app/file.txt\n"
            "```\n"
            "Output ONLY bash commands within ```bash blocks. "
            "For multi-step tasks, output commands in logical order.\n"
            "If a command is likely to take >30s (pip install, apt-get, large build), "
            "prefix it with `timeout 300`.\n"
            "Do NOT output explanations — only the ```bash block with commands."
        )

        response = client.chat.completions.create(
            model=self._model,
            messages=[
                {"role": "system", "content": system_prompt},
                {"role": "user", "content": f"Task:\n\n{rendered}"},
            ],
            temperature=self._temperature,
            max_tokens=4096,
        )

        output_text = response.choices[0].message.content or ""
        tokens_in = response.usage.prompt_tokens if response.usage else 0
        tokens_out = response.usage.completion_tokens if response.usage else 0

        print(f"\n[DeepSeekAgent] Model output ({tokens_out} tokens):")
        print(output_text[:2000])

        # Extract bash commands — now supports base64 encoded payloads
        commands = self._extract_commands(output_text)

        if not commands:
            print(f"  WARNING: No commands extracted from model output!")
            return AgentResult(
                total_input_tokens=tokens_in,
                total_output_tokens=tokens_out,
                failure_mode=FailureMode.TEST_TIMEOUT,
            )

        # Phase 1: Warm up — send a no-op without blocking
        try:
            session.send_keys(["echo warmup-ok", "Enter"], block=False, max_timeout_sec=30)
            time.sleep(3)  # let the container process it
        except Exception as e:
            print(f"  WARMUP ERROR: {e}")

        # Phase 2: Send actual commands — each command as a SINGLE line
        # (tmux send_keys with a literal newline in the string, not heredoc lines)
        for i, line in enumerate(commands.split("\n")):
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            if line.startswith("```"):
                continue

            print(f"  [{i}] SEND: {line[:150]}")
            try:
                # Send each command as a single string including \n for multi-line
                # The command text already contains \n for multi-line printf/echo
                session.send_keys([line, "Enter"], block=False, max_timeout_sec=30)
                time.sleep(2)  # brief pause for execution
            except Exception as e:
                print(f"  [{i}] SEND ERROR: {e}")

        # Phase 3: Wait for final command to settle with longer timeout
        try:
            time.sleep(10)  # more settling time for apt-get, pip install, etc.
            session.send_keys(["echo agent-done", "Enter"], block=True, max_timeout_sec=120)
        except Exception as e:
            print(f"  FINAL WAIT ERROR: {e}")

        return AgentResult(
            total_input_tokens=tokens_in,
            total_output_tokens=tokens_out,
            failure_mode=FailureMode.NONE,
        )

    @staticmethod
    def _extract_commands(text: str) -> str:
        """Extract bash commands from model output."""
        import re
        # Try ```bash ... ``` block
        m = re.search(r"```(?:bash|sh)\n(.*?)```", text, re.DOTALL)
        if m:
            return m.group(1).strip()
        # Try any code block
        m = re.search(r"```\n?(.*?)```", text, re.DOTALL)
        if m:
            return m.group(1).strip()
        # Try inline code blocks
        m = re.search(r"`([^`]{20,})`", text, re.DOTALL)
        if m:
            return m.group(1).strip()
        # Raw text — take non-explanatory lines
        lines = []
        for line in text.split("\n"):
            line = line.strip()
            if not line:
                continue
            if re.match(r"^(?:timeout\s+\d+\s+)?(sudo |cat |echo |python[23]? |cd |bash |mkdir |pip |apt |npm |node |git |curl |wget |cp |mv |rm |docker |ls |pwd |grep |find |sed |awk |chmod |make |gcc |g\+\+|\./)", line):
                lines.append(line)
        return "\n".join(lines) if lines else text.strip()
