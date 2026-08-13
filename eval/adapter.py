"""Shared adapter interface for eval benchmarks.

Defines the canonical data containers (:class:`EvalInstance`, :class:`EvalResult`)
and the :class:`AgentAdapter` protocol that every headless driver must satisfy
so benchmark scripts can call ``adapter.solve_instance(instance, working_dir)``
without caring about the underlying LLM provider or orchestration path.
"""

from __future__ import annotations

import time
import uuid
from dataclasses import dataclass, field
from typing import Any, Protocol, runtime_checkable


# ---------------------------------------------------------------------------
# Data containers
# ---------------------------------------------------------------------------


@dataclass(slots=True)
class EvalInstance:
    """A single benchmark problem instance.

    Attributes
    ----------
    instance_id:
        Unique id such as ``"HumanEval/0"`` or ``"swe-bench__org/repo__issue-42"``.
    task_description:
        The issue statement, function signature, or problem description fed to
        the agent as the user message.
    metadata:
        Benchmark-specific extra data: repo URL, base commit, expected test
        file, language, difficulty, etc.
    """

    instance_id: str
    task_description: str
    metadata: dict = field(default_factory=dict)


@dataclass(slots=True)
class EvalResult:
    """Result of one agent invocation on a single :class:`EvalInstance`.

    Fields follow the shared eval spec so every benchmark adapter can consume
    them uniformly.  Exactly one of *model_patch* (repo-level diff) or *answer*
    (function-level generated code) is expected to be non-empty.
    """

    instance_id: str

    # ---- output ----
    model_patch: str = ""       # git diff for repo-level tasks (SWE-bench)
    answer: str = ""           # function body for function-level tasks (HumanEval)

    # ---- cost accounting ----
    cost: float = 0.0
    tokens_in: int = 0
    tokens_out: int = 0

    # ---- metadata ----
    trace_id: str = ""
    error: str = ""
    wall_time_s: float = 0.0
    # Only profile-approved, non-content evidence may be stored here. O3 uses
    # stable retrieval identifiers/ranks/scores; queries, prompt text, qrels,
    # and document bodies are forbidden.
    evidence: dict[str, Any] = field(default_factory=dict)


# ---------------------------------------------------------------------------
# Agent adapter protocol
# ---------------------------------------------------------------------------


@runtime_checkable
class AgentAdapter(Protocol):
    """Protocol that every headless driver must satisfy.

    Benchmark adapters in ``eval/benchmarks/*.py`` call::

        result = adapter.solve_instance(instance, working_dir)

    and receive an :class:`EvalResult` regardless of which LLM or orchestration
    path was used.
    """

    def solve_instance(
        self,
        instance: EvalInstance,
        working_dir: str,
        **kwargs,
    ) -> EvalResult:
        """Solve a single eval instance and return the result.

        Parameters
        ----------
        instance:
            The problem instance (task description + metadata).
        working_dir:
            Absolute path to a writable directory the agent may use.  For
            repo-level benchmarks this should be a git working tree; for
            function-level benchmarks it can be a temp directory.
        **kwargs:
            Extra arguments forwarded by the benchmark adapter (e.g.
            ``cancel_event``, ``timeout_s``).

        Returns
        -------
        EvalResult
            Never returns ``None``.  On unrecoverable errors the *error*
            field is set and the caller should treat the instance as
            failed.
        """
        ...


# ---------------------------------------------------------------------------
# Default adapter (timing / cost wrapper)
# ---------------------------------------------------------------------------


class DefaultAgentAdapter:
    """Base class that wraps ``_do_solve`` with wall-clock timing and a
    trace-id so concrete drivers only need to implement one method.

    Subclasses override :meth:`_do_solve` and the base class adds timing,
    trace-id generation, and guarantees :class:`EvalResult.error` is never
    empty on exceptions (so the caller always gets a result object).
    """

    def solve_instance(
        self,
        instance: EvalInstance,
        working_dir: str,
        **kwargs,
    ) -> EvalResult:
        trace_id = str(uuid.uuid4())
        t0 = time.perf_counter()
        try:
            result = self._do_solve(instance, working_dir, trace_id=trace_id, **kwargs)
        except Exception as exc:
            elapsed = time.perf_counter() - t0
            return EvalResult(
                instance_id=instance.instance_id,
                error=f"{type(exc).__name__}: {exc}",
                trace_id=trace_id,
                wall_time_s=round(elapsed, 3),
            )
        elapsed = time.perf_counter() - t0
        # Preserve wall_time_s set by subclass if already non-zero, else set it.
        if result.wall_time_s == 0.0:
            result.wall_time_s = round(elapsed, 3)
        if not result.trace_id:
            result.trace_id = trace_id
        return result

    def _do_solve(
        self,
        instance: EvalInstance,
        working_dir: str,
        trace_id: str = "",
        **kwargs,
    ) -> EvalResult:
        """Override point for concrete drivers.

        The base class guarantees that any exception raised here is caught
        and turned into ``EvalResult.error``.
        """
        raise NotImplementedError("subclasses must implement _do_solve")
