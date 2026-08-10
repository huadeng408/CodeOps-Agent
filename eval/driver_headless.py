"""Headless driver -- runs the LLM agent without the TUI / gRPC harness.

Two execution paths are available (tried in order):

1. **ConversationRunner path** (for SWE-bench / repo-level tasks):
   Constructs a full :class:`ConversationRunner` with a local-tool executor,
   feeds the task description, collects tool calls & results, and captures
   ``git diff HEAD`` after completion.

2. **Direct LLM path** (for HumanEval / function-level tasks):
   Calls the LLM client directly without any orchestration stack.  Faster,
   simpler, and the default for code-generation benchmarks.

The driver defaults to the ollama ``qwen3:4b`` model at
``http://127.0.0.1:11434/v1``.  Override via environment variables
``LOCAL_LLM_BASE_URL``, ``LOCAL_LLM_MODEL``, ``LOCAL_LLM_API_KEY``, or pass
them to the constructor (explicit arguments win over env vars).
"""

from __future__ import annotations

import asyncio
import json
import os
import queue
import re
import subprocess
import sys
import tempfile
import time
import traceback
import uuid
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from eval.adapter import DefaultAgentAdapter, EvalInstance, EvalResult


# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

DEFAULT_OLLAMA_BASE_URL = "http://127.0.0.1:11434/v1"
DEFAULT_OLLAMA_MODEL = "qwen3:4b"
DEFAULT_TIMEOUT_S = 120.0
MAX_TOOL_OUTPUT_BYTES = 50_000
MAX_TOOL_OUTPUT_LINES = 250

# Tools whose output is deterministic for the same arguments (safe to cache
# within a single conversation turn).
_IDEMPOTENT_TOOLS = frozenset({"Read", "Glob", "Grep"})


# ---------------------------------------------------------------------------
# Lightweight tool result
# ---------------------------------------------------------------------------


@dataclass(slots=True)
class _LocalToolResult:
    output: str = ""
    error: str = ""
    exit_code: int = 0
    truncated: bool = False


# ---------------------------------------------------------------------------
# Local tool executor
# ---------------------------------------------------------------------------


class LocalToolExecutor:
    """Executes a subset of the agent's tools locally (in-process).

    The Go harness normally handles tool execution; this class replicates the
    essential tools so the headless driver can run the full
    :class:`~orchestrator.runtime.conversation.ConversationRunner` without a
    gRPC round-trip.

    Supported tools: Read, Write, Edit, Bash, Glob, Grep.
    Unsupported tools return a stub error so the LLM can adapt.
    """

    def __init__(self, working_dir: str) -> None:
        self._cwd = Path(working_dir).resolve()

    # -- public API ---------------------------------------------------------

    def execute(self, tool_name: str, params_json: str) -> _LocalToolResult:
        params: dict[str, Any] = {}
        try:
            params = json.loads(params_json or "{}")
        except json.JSONDecodeError:
            pass
        if not isinstance(params, dict):
            params = {}

        try:
            handler_name = _TOOL_HANDLERS.get(tool_name)
            if handler_name is None:
                return _LocalToolResult(
                    error=f"Tool not available in headless eval: {tool_name}",
                    exit_code=1,
                )
            handler = getattr(self, handler_name)
            return handler(params)
        except Exception:
            return _LocalToolResult(
                error=traceback.format_exc(),
                exit_code=1,
            )

    # -- helpers ------------------------------------------------------------

    def _resolve(self, rel: str) -> Path:
        """Resolve a workspace-relative or absolute path."""
        p = Path(rel)
        if p.is_absolute():
            return p
        return (self._cwd / p).resolve()

    @staticmethod
    def _truncate(text: str) -> tuple[str, bool]:
        lines = text.splitlines()
        truncated = False
        if len(lines) > MAX_TOOL_OUTPUT_LINES:
            lines = lines[:MAX_TOOL_OUTPUT_LINES]
            truncated = True
        joined = "\n".join(lines)
        if len(joined.encode("utf-8")) > MAX_TOOL_OUTPUT_BYTES:
            joined = joined.encode("utf-8")[:MAX_TOOL_OUTPUT_BYTES].decode(
                "utf-8", errors="replace"
            )
            truncated = True
        return joined, truncated

    # -- tool implementations -----------------------------------------------

    def _read(self, params: dict[str, Any]) -> _LocalToolResult:
        path = self._resolve(params.get("path", params.get("file_path", "")))
        if not path.exists():
            return _LocalToolResult(error=f"File not found: {path}", exit_code=1)
        if path.is_dir():
            # List directory contents
            try:
                entries = sorted(path.iterdir(), key=lambda e: e.name.lower())
                lines = [f"{'[DIR] ' if e.is_dir() else '[FILE]'} {e.name}" for e in entries]
                output = "\n".join(lines)
                output, truncated = self._truncate(output)
                return _LocalToolResult(output=output, truncated=truncated)
            except OSError as exc:
                return _LocalToolResult(error=str(exc), exit_code=1)

        try:
            text = path.read_text(encoding="utf-8", errors="replace")
        except OSError as exc:
            return _LocalToolResult(error=str(exc), exit_code=1)

        # Apply offset/limit for text files
        offset = params.get("offset", params.get("start", 0))
        if isinstance(offset, int) and offset > 0:
            offset = offset - 1 if params.get("start") else offset
        limit = params.get("limit")
        if isinstance(offset, int) and offset > 0 or isinstance(limit, int) and limit is not None:
            lines = text.splitlines()
            start = max(0, int(offset or 0))
            end = start + int(limit or len(lines)) if limit is not None else len(lines)
            text = "\n".join(lines[start:end])

        output, truncated = self._truncate(text)
        return _LocalToolResult(output=output, truncated=truncated)

    def _write(self, params: dict[str, Any]) -> _LocalToolResult:
        path = self._resolve(params.get("path", params.get("file_path", "")))
        content = params.get("content", "")
        if not str(path):
            return _LocalToolResult(error="path is required", exit_code=1)
        try:
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(content, encoding="utf-8")
        except OSError as exc:
            return _LocalToolResult(error=str(exc), exit_code=1)
        return _LocalToolResult(output=f"Wrote {len(content)} bytes to {path}")

    def _edit(self, params: dict[str, Any]) -> _LocalToolResult:
        path = self._resolve(params.get("path", params.get("file_path", "")))
        old_str = params.get("old", params.get("old_string", ""))
        new_str = params.get("new", params.get("new_string", ""))
        replace_all = params.get("replace_all", False)

        if not str(path):
            return _LocalToolResult(error="path is required", exit_code=1)
        if not old_str:
            return _LocalToolResult(error="old string is required", exit_code=1)

        try:
            original = path.read_text(encoding="utf-8")
        except OSError as exc:
            return _LocalToolResult(error=str(exc), exit_code=1)

        count = original.count(old_str)
        if count == 0:
            return _LocalToolResult(
                error=f"old_string not found in {path}", exit_code=1
            )
        if count > 1 and not replace_all:
            return _LocalToolResult(
                error=(
                    f"old_string appears {count} times in {path}. "
                    f"Use replace_all=true or make the match more specific."
                ),
                exit_code=1,
            )

        updated = original.replace(old_str, new_str) if replace_all else original.replace(old_str, new_str, 1)
        try:
            path.write_text(updated, encoding="utf-8")
        except OSError as exc:
            return _LocalToolResult(error=str(exc), exit_code=1)
        return _LocalToolResult(output=f"Edit applied to {path}")

    def _bash(self, params: dict[str, Any]) -> _LocalToolResult:
        command = params.get("command", "")
        if not command:
            return _LocalToolResult(error="command is required", exit_code=1)

        cwd = self._cwd
        cwd_param = params.get("cwd")
        if cwd_param:
            cwd = self._resolve(cwd_param)

        timeout = params.get("timeout_seconds", params.get("timeout", 60))
        try:
            timeout = float(timeout)
        except (TypeError, ValueError):
            timeout = 60.0

        try:
            proc = subprocess.run(
                command,
                shell=True,
                cwd=str(cwd),
                capture_output=True,
                timeout=timeout,
                env={**os.environ},
            )
        except subprocess.TimeoutExpired:
            return _LocalToolResult(
                error=f"Command timed out after {timeout}s", exit_code=124
            )
        except OSError as exc:
            return _LocalToolResult(error=str(exc), exit_code=1)

        stdout = proc.stdout.decode("utf-8", errors="replace")
        stderr = proc.stderr.decode("utf-8", errors="replace")
        output = stdout
        if stderr:
            output = f"{stdout}\n[stderr]\n{stderr}" if stdout else stderr
        output, truncated = self._truncate(output)
        return _LocalToolResult(
            output=output,
            error="",
            exit_code=proc.returncode,
            truncated=truncated,
        )

    def _glob(self, params: dict[str, Any]) -> _LocalToolResult:
        pattern = params.get("pattern", "**/*")
        head_limit = params.get("head_limit")
        try:
            head_limit = int(head_limit) if head_limit is not None else 200
        except (TypeError, ValueError):
            head_limit = 200

        from glob import iglob

        matches: list[str] = []
        abs_pattern = str(self._cwd / pattern)
        try:
            for p in iglob(abs_pattern, recursive=True):
                matches.append(str(Path(p).relative_to(self._cwd)))
                if len(matches) >= head_limit:
                    break
        except Exception as exc:
            return _LocalToolResult(error=str(exc), exit_code=1)

        output = "\n".join(matches) if matches else "(no matches)"
        output, truncated = self._truncate(output)
        return _LocalToolResult(output=output, truncated=truncated)

    def _grep(self, params: dict[str, Any]) -> _LocalToolResult:
        pat = params.get("pattern", "")
        if not pat:
            return _LocalToolResult(error="pattern is required", exit_code=1)

        search_path = self._cwd
        path_param = params.get("path")
        if path_param:
            search_path = self._resolve(path_param)
        if not search_path.exists():
            return _LocalToolResult(error=f"Path not found: {search_path}", exit_code=1)

        glob_filter = params.get("glob")
        output_mode = params.get("output_mode", "files_with_matches")
        head_limit_raw = params.get("head_limit", 50)
        try:
            head_limit = int(head_limit_raw) if head_limit_raw else 50
        except (TypeError, ValueError):
            head_limit = 50
        ignore_case = params.get("ignore_case", params.get("-i", False))
        context = params.get("context", params.get("-C", 0))
        try:
            context = int(context)
        except (TypeError, ValueError):
            context = 0

        flags = re.IGNORECASE if ignore_case else 0
        try:
            regex = re.compile(pat, flags | re.MULTILINE)
        except re.error as exc:
            return _LocalToolResult(error=f"Invalid regex: {exc}", exit_code=1)

        results: list[str] = []
        py_files: list[Path] = []
        if search_path.is_file():
            py_files = [search_path]
        else:
            pattern_glob = glob_filter or "**/*"
            from glob import iglob
            for p in iglob(str(search_path / pattern_glob), recursive=True):
                pp = Path(p)
                if pp.is_file():
                    py_files.append(pp)

        for fpath in py_files:
            try:
                text = fpath.read_text(encoding="utf-8", errors="replace")
            except OSError:
                continue
            lines = text.splitlines()
            for i, line in enumerate(lines):
                if regex.search(line):
                    if output_mode == "files_with_matches":
                        results.append(str(fpath.relative_to(self._cwd)))
                        break
                    elif output_mode == "count":
                        pass  # accumulate below
                    else:  # content
                        if context > 0:
                            start = max(0, i - context)
                            end = min(len(lines), i + context + 1)
                            for j in range(start, end):
                                marker = ">" if j == i else " "
                                results.append(
                                    f"{str(fpath.relative_to(self._cwd))}:{j + 1}:{marker}{lines[j]}"
                                )
                        else:
                            results.append(
                                f"{str(fpath.relative_to(self._cwd))}:{i + 1}:{line}"
                            )
            if output_mode == "count":
                count = sum(1 for line in lines if regex.search(line))
                if count > 0:
                    results.append(
                        f"{str(fpath.relative_to(self._cwd))}:{count}"
                    )

        if head_limit > 0 and len(results) > head_limit:
            results = results[:head_limit]

        output = "\n".join(results) if results else "(no matches)"
        output, truncated = self._truncate(output)
        return _LocalToolResult(output=output, truncated=truncated)


# Tool name -> handler mapping
_TOOL_HANDLERS: dict[str, str] = {
    "Read": "_read",
    "Write": "_write",
    "Edit": "_edit",
    "Bash": "_bash",
    "Glob": "_glob",
    "Grep": "_grep",
}


# ---------------------------------------------------------------------------
# Headless driver
# ---------------------------------------------------------------------------


class HeadlessDriver(DefaultAgentAdapter):
    """Headless eval driver -- calls the LLM without the TUI / gRPC harness.

    Two execution strategies, tried in order:

    * **runner** -- Full :class:`ConversationRunner` with local tool
      execution.  Used when the orchestrator imports succeed.  Captures
      ``git diff HEAD`` as the model patch for repo-level tasks.
    * **direct** -- Simple chat completion.  Used as a fallback and also
      the primary path for function-level benchmarks (HumanEval) where no
      tool calls are expected.

    Parameters
    ----------
    model:
        Model name (default ``"qwen3:4b"``).
    base_url:
        OpenAI-compatible base URL (default ``http://127.0.0.1:11434/v1``).
    api_key:
        API key sent to the LLM provider.  Resolution order: explicit value,
        then ``LOCAL_LLM_API_KEY``, then the local-ollama placeholder
        ``"ollama"``.  The default ``None`` matters: a hard-coded ``"ollama"``
        default would shadow ``LOCAL_LLM_API_KEY`` and silently break remote
        providers.
    use_runner:
        If ``True``, always attempt the ConversationRunner path first.
        Default ``True``.
    timeout_s:
        Per-request timeout in seconds.
    """

    def __init__(
        self,
        model: str | None = None,
        base_url: str | None = None,
        api_key: str | None = None,
        use_runner: bool = True,
        timeout_s: float = DEFAULT_TIMEOUT_S,
    ) -> None:
        super().__init__()
        self.model = model or os.environ.get("LOCAL_LLM_MODEL", DEFAULT_OLLAMA_MODEL)
        self.base_url = base_url or os.environ.get(
            "LOCAL_LLM_BASE_URL", DEFAULT_OLLAMA_BASE_URL
        )
        # Explicit argument -> LOCAL_LLM_API_KEY env -> local ollama placeholder.
        # Defaulting to None (not "ollama") is what lets the env var win.
        self.api_key = api_key or os.environ.get("LOCAL_LLM_API_KEY", "ollama")
        self.timeout_s = timeout_s
        self.use_runner = use_runner

        # Lazy-initialised LLM client
        self._llm: Any = None

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    def _do_solve(
        self,
        instance: EvalInstance,
        working_dir: str,
        trace_id: str = "",
        **kwargs,
    ) -> EvalResult:
        """Solve *instance* and return an :class:`EvalResult`.

        Tries the ConversationRunner path first; falls back to direct LLM call.
        Failures are captured as ``EvalResult.error`` (never raised).
        """
        cancel_event = kwargs.get("cancel_event", None)

        # ---- path 1: full ConversationRunner ----
        if self.use_runner:
            try:
                return self._solve_with_runner(instance, working_dir, trace_id, cancel_event)
            except Exception as exc:
                # Log and fall through to direct path
                sys.stderr.write(
                    f"[eval] ConversationRunner path failed for {instance.instance_id}, "
                    f"falling back to direct LLM:\n{traceback.format_exc()}\n"
                )
                sys.stderr.flush()

                # ---- IMPROVED FALLBACK: give the direct LLM the SAME context ----
                # Previously the direct path lost ALL tool-execution context (git diff
                # output, file contents the agent read, error messages from tool calls).
                # Now we capture whatever the ConversationRunner managed to produce
                # (tool outputs, state updates) as context for the direct fallback.
                if isinstance(exc, Exception):
                    fallback_context = self._capture_fallback_context(working_dir)
                    instance.task_description = (
                        instance.task_description
                        + "\n\n## Additional Context (from prior tool execution)\n\n"
                        + fallback_context
                    )

        # ---- path 2: direct LLM call ----
        try:
            return self._solve_direct(instance, trace_id, cancel_event)
        except Exception:
            return EvalResult(
                instance_id=instance.instance_id,
                error=traceback.format_exc(),
                trace_id=trace_id,
            )

    # ------------------------------------------------------------------
    # Path 1: ConversationRunner (repo-level / SWE-bench)
    # ------------------------------------------------------------------

    def _solve_with_runner(
        self,
        instance: EvalInstance,
        working_dir: str,
        trace_id: str,
        cancel_event: Any = None,
    ) -> EvalResult:
        """Run the full ConversationRunner with local tool execution."""
        # Deferred imports so the module is importable even when parts of the
        # orchestrator stack are unavailable.
        from orchestrator.config import load_dotenv
        from orchestrator.graph.main_graph import build_graph
        from orchestrator.llm.client import (
            ChatMessage,
            ChatRequest,
            ChatResponse,
            ToolCall,
            Usage,
        )
        from orchestrator.llm.providers.local import LocalClient
        from orchestrator.memory.manager import MemoryManager
        from orchestrator.runtime.conversation import ConversationRunner
        from orchestrator.runtime.tools import ToolRegistry
        from orchestrator.skills.manager import SkillManager
        from orchestrator.todo.manager import TodoManager

        # Ensure env is loaded (for .env.local overrides)
        load_dotenv()

        # Build the LLM client
        llm = self._get_llm_client()

        # Build the orchestrator components
        graph = build_graph()
        tools = ToolRegistry(working_dir)
        todo_mgr = TodoManager()
        memory_mgr = MemoryManager(str(Path(working_dir) / ".agent" / "memory"))
        skills_mgr = SkillManager()

        runner = ConversationRunner(
            graph=graph,
            llm=llm,
            tool_registry=tools,
            todo_manager=todo_mgr,
            memory_manager=memory_mgr,
            skills=skills_mgr,
            project_root=working_dir,
            working_dir=working_dir,
            max_tool_rounds=8,
        )

        # ---- Queue-based tool result bridge ----
        # ConversationRunner.run() is a generator that yields protobuf
        # messages and *pulls* tool results from the request_iterator
        # parameter.  We use a queue.Queue to feed results back in the
        # same thread: each time the runner yields a ToolRequest we
        # execute it locally, push the result into the queue, and the
        # runner picks it up via its internal next(request_iterator) call.
        tool_queue: queue.Queue = queue.Queue()

        def request_iter():
            while True:
                msg = tool_queue.get()
                if msg is None:       # sentinel -- runner should stop
                    break
                yield msg

        # Start the runner
        gen = runner.run(
            instance.task_description,
            request_iter(),
            cancel_event=cancel_event,
        )

        tool_executor = LocalToolExecutor(working_dir)
        final_text_parts: list[str] = []
        tokens_in = 0
        tokens_out = 0
        cached_tokens = 0
        cost = 0.0
        success = False

        # We need to import protobuf types to construct tool result messages
        try:
            from codeagent import orchestrator_pb2  # type: ignore[import-untyped]
            _HAS_PROTOBUF = True
        except ImportError:
            _HAS_PROTOBUF = False

        if not _HAS_PROTOBUF:
            raise RuntimeError(
                "codeagent protobuf module not available -- "
                "cannot run ConversationRunner path"
            )

        try:
            while True:
                try:
                    msg = next(gen)
                except StopIteration:
                    break

                which = msg.WhichOneof("payload")

                if which == "text":
                    final_text_parts.append(msg.text.text)

                elif which == "tool_request":
                    req = msg.tool_request
                    result = tool_executor.execute(req.tool_name, req.parameters_json)
                    tool_queue.put(
                        orchestrator_pb2.HarnessMessage(
                            tool_result=orchestrator_pb2.ToolResult(
                                tool_call_id=req.tool_call_id,
                                output=result.output,
                                error=result.error,
                                exit_code=result.exit_code,
                                truncated=result.truncated,
                            )
                        )
                    )

                elif which == "tool_request_batch":
                    batch = msg.tool_request_batch
                    for req in batch.requests:
                        result = tool_executor.execute(req.tool_name, req.parameters_json)
                        tool_queue.put(
                            orchestrator_pb2.HarnessMessage(
                                tool_result=orchestrator_pb2.ToolResult(
                                    tool_call_id=req.tool_call_id,
                                    output=result.output,
                                    error=result.error,
                                    exit_code=result.exit_code,
                                    truncated=result.truncated,
                                )
                            )
                        )

                elif which == "session_meta":
                    meta = msg.session_meta
                    tokens_in = meta.tokens_in
                    tokens_out = meta.tokens_out
                    cached_tokens = getattr(meta, "cached_tokens", 0)
                    cost = meta.cost

                elif which == "ask_user_request":
                    # Auto-respond to AskUser during eval: deny with a
                    # message so the agent continues without blocking.
                    ask = msg.ask_user_request
                    tool_queue.put(
                        orchestrator_pb2.HarnessMessage(
                            tool_result=orchestrator_pb2.ToolResult(
                                tool_call_id=ask.ask_user_id,
                                output="[eval] User interaction is disabled during evaluation. "
                                       "Proceed with best-effort answer.",
                                error="",
                                exit_code=0,
                            )
                        )
                    )

                elif which == "done":
                    success = msg.done.success
                    break

        finally:
            # Signal the request iterator to stop and close the generator
            tool_queue.put(None)
            try:
                gen.close()
            except Exception:
                pass

        # ---- Capture git diff as model_patch ----
        model_patch = ""
        try:
            proc = subprocess.run(
                ["git", "diff", "HEAD"],
                cwd=working_dir,
                capture_output=True,
                timeout=15,
            )
            if proc.returncode == 0:
                model_patch = proc.stdout.decode("utf-8", errors="replace")
        except Exception:
            pass

        # Streaming deltas are fragments of a sentence, not lines: joining them
        # with "\n" turned "Now I can see" into "Now\nI\ncan\nsee" in the
        # recorded prediction, corrupting the artifact that IS the evidence.
        final_text = "".join(final_text_parts)

        return EvalResult(
            instance_id=instance.instance_id,
            model_patch=model_patch,
            answer=final_text,
            cost=round(cost, 6),
            tokens_in=tokens_in,
            tokens_out=tokens_out,
            trace_id=trace_id,
            error="" if success else "runner completed with done.success=False",
        )

    # ------------------------------------------------------------------
    # Path 2: Direct LLM call (function-level / HumanEval)
    # ------------------------------------------------------------------

    def _solve_direct(
        self,
        instance: EvalInstance,
        trace_id: str,
        cancel_event: Any = None,
    ) -> EvalResult:
        """Simplified path: one-shot chat completion, no tools.

        When used as a fallback from ConversationRunner failure, the
        task_description has already been enriched with git diff context.
        """
        from orchestrator.llm.client import ChatMessage, ChatRequest

        llm = self._get_llm_client()

        system_prompt = (
            "You are a coding assistant. Complete the given task accurately. "
            "Output ONLY the requested code or answer. Do not include "
            "explanations, markdown fences, or commentary unless explicitly "
            "asked. If the task is to complete a function, output the complete "
            "function including the signature."
        )

        messages = [
            ChatMessage(role="system", content=system_prompt),
            ChatMessage(role="user", content=instance.task_description),
        ]

        request = ChatRequest(
            model=self.model,
            messages=messages,
            temperature=0.0,
            cancel_event=cancel_event,
        )

        response = asyncio.run(llm.chat(request))

        # Strip markdown code fences from the answer if present
        answer = self._strip_code_fences(response.text)

        return EvalResult(
            instance_id=instance.instance_id,
            answer=answer,
            cost=round(
                self._estimate_local_cost(
                    response.usage.input_tokens, response.usage.output_tokens
                ),
                6,
            ),
            tokens_in=response.usage.input_tokens,
            tokens_out=response.usage.output_tokens,
            trace_id=trace_id,
        )

    # ------------------------------------------------------------------
    # Helpers
    # ------------------------------------------------------------------

    def _get_llm_client(self) -> Any:
        """Return (or lazily create) the LLM client."""
        if self._llm is None:
            from orchestrator.llm.providers.local import LocalClient

            self._llm = LocalClient(
                api_key=self.api_key,
                base_url=self.base_url,
                model=self.model,
                timeout=self.timeout_s,
                max_retries=1,
            )
        return self._llm

    @staticmethod
    def _strip_code_fences(text: str) -> str:
        """Remove markdown code-fence wrappers from LLM output.

        Many models wrap code in ```python ... ``` blocks even when told
        not to.  This strips a single outermost fence if present.
        """
        text = text.strip()
        # Pattern: optional opening ```lang, then content, then closing ```
        m = re.match(
            r"^```[a-zA-Z0-9]*[\r\n]+(.*?)[\r\n]*```$", text, re.DOTALL
        )
        if m:
            return m.group(1).strip()
        return text

    def _estimate_local_cost(
        self, tokens_in: int, tokens_out: int
    ) -> float:
        """Estimate cost for a local model (ollama).  Local models are
        free, but we record a small per-token cost for accounting so
        cost-based comparisons work uniformly."""
        # Ollama is free; use a nominal $0 / 1k tokens rate for accounting.
        return 0.0

    def _capture_fallback_context(self, working_dir: str) -> str:
        """Capture context from the failed ConversationRunner for the
        direct LLM fallback.

        When the ConversationRunner path fails (e.g. tool-calling protocol
        error), the direct fallback used to get only the problem statement.
        This collects whatever the agent managed to produce — git diff,
        recent file modifications, error logs — so the direct LLM has
        meaningful context to work with.
        """
        parts: list[str] = []

        # 1. Git diff (what the agent has already changed)
        try:
            proc = subprocess.run(
                ["git", "diff", "HEAD"],
                cwd=working_dir,
                capture_output=True,
                timeout=15,
            )
            if proc.returncode == 0 and proc.stdout.strip():
                diff_text = proc.stdout.decode("utf-8", errors="replace")
                parts.append(f"Current changes (git diff):\n```diff\n{diff_text}\n```")
        except Exception:
            pass

        # 2. Git log (last commit for context)
        try:
            proc = subprocess.run(
                ["git", "log", "--oneline", "-5"],
                cwd=working_dir,
                capture_output=True,
                timeout=10,
            )
            if proc.returncode == 0 and proc.stdout.strip():
                parts.append(f"Recent commits:\n{proc.stdout.decode('utf-8', errors='replace')}")
        except Exception:
            pass

        # 3. List recently modified files
        try:
            proc = subprocess.run(
                ["git", "diff", "--name-only", "HEAD"],
                cwd=working_dir,
                capture_output=True,
                timeout=10,
            )
            if proc.returncode == 0 and proc.stdout.strip():
                changed = proc.stdout.decode("utf-8", errors="replace").strip()
                parts.append(f"Modified files: {changed}")
        except Exception:
            pass

        if not parts:
            return ""

        return "\n\n".join(parts)


# ---------------------------------------------------------------------------
# Convenience factory
# ---------------------------------------------------------------------------


def create_driver(
    model: str | None = None,
    base_url: str | None = None,
    api_key: str | None = None,
    use_runner: bool = True,
) -> HeadlessDriver:
    """Create a :class:`HeadlessDriver` with the given or env-default settings.

    This is the intended entry point for benchmark scripts::

        from eval.driver_headless import create_driver
        driver = create_driver()
        result = driver.solve_instance(instance, "/tmp/eval_work")

    *api_key* may carry an already-resolved key (e.g. read from
    ``LOCAL_LLM_API_KEY``); if omitted the driver resolves it itself in the
    order explicit argument -> ``LOCAL_LLM_API_KEY`` -> ``"ollama"``.
    """
    return HeadlessDriver(
        model=model, base_url=base_url, api_key=api_key, use_runner=use_runner
    )
