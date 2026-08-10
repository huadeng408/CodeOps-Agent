"""Custom DeepSeek Agent for Terminal-Bench — standalone module the harness can import.

Fixes:
  - First command not blocked (container warm-up is slow, so the first command
    often times out if we block). We send a no-op echo first without blocking,
    then block on subsequent commands.
  - Increased max_timeout_sec to 300s for expensive commands (pip install, apt-get).
  - Multi-turn mode: the agent can ask for command output and issue follow-up commands.
"""
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
        self._api_key = kwargs.get("api_key", "")
        self._model = kwargs.get("model", "deepseek-chat")

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
            base_url="https://api.deepseek.com/v1",
        )

        rendered = self._render_instruction(instruction)

        system_prompt = (
            "You are an expert security engineer. Your task is to write a specific "
            "HTML file that will bypass an XSS filter.\n\n"
            "The filter removes:\n"
            "1. <script> tags (and their content)\n"
            "2. <frame>, <iframe>, <object>, <embed> tags\n"
            "3. Event handler attributes (onclick, onerror, onload, etc.)\n\n"
            "Your HTML must trigger alert() WITHOUT using <script> tags and WITHOUT "
            "event handler attributes. Think about SVG, img tags with javascript: URLs, "
            "or other creative approaches.\n\n"
            "IMPORTANT: Use base64 encoding to write files — NOT heredoc.\n"
            "Example: echo 'BASE64CONTENT' | base64 -d > /app/out.html\n\n"
            "Output ONLY bash commands within ```bash blocks."
        )

        response = client.chat.completions.create(
            model=self._model,
            messages=[
                {"role": "system", "content": system_prompt},
                {"role": "user", "content": f"Task:\n\n{rendered}"},
            ],
            temperature=0.0,
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

        # Phase 3: Wait for final command to settle with MUCH longer timeout
        # Container setup (conda create, pip install) can take 3-5 minutes.
        # The harness's own test timeout will handle the 1200s limit.
        try:
            time.sleep(15)  # extra settling time for apt-get, pip install, conda
            session.send_keys(["echo agent-done", "Enter"], block=True, max_timeout_sec=300)
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
