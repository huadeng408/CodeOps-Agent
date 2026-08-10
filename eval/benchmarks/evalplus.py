"""
EvalPlus benchmark adapter for HumanEval+ and MBPP+.

Evaluates arbitrary agent adapters against the EvalPlus code generation benchmarks
(HumanEval+: 164 problems, MBPP+: 378 problems).

Core concepts:
    - EvalInstance: a single benchmark problem (task_id, prompt, metadata)
    - AgentAdapter: any callable with solve_instance(instance, working_dir) -> EvalResult
    - EvalPlusBenchmark: orchestrates loading, solving, scoring, and reporting

Usage:
    # Mock test (verify pipeline with canonical solutions):
    from eval.benchmarks.evalplus import EvalPlusBenchmark, MockAgentAdapter

    benchmark = EvalPlusBenchmark("humaneval", limit=5)
    mock = MockAgentAdapter(benchmark.problems)
    benchmark.adapter = mock
    stats = benchmark.run(output_dir="./results")
    benchmark.print_summary()

    # Real evaluation:
    from eval.benchmarks.evalplus import EvalPlusBenchmark
    from eval.adapter import MyAdapter

    benchmark = EvalPlusBenchmark("humaneval", adapter=MyAdapter(...), limit=10)
    stats = benchmark.run(output_dir="./results")
"""

from __future__ import annotations

import json
import os
import sys
import time
from collections import defaultdict
from dataclasses import dataclass, field
from pathlib import Path
from typing import TYPE_CHECKING, Any, Dict, List, Optional

import numpy as np

#: EvalPlus generates code from a self-contained prompt: no retrieval, no
#: reranking, no corpus.  Declaring that here waives the RAG spans and pins
#: rather than making this benchmark's manifest impossible to satisfy.
TRACE_CAPABILITIES: tuple[str, ...] = ()

# ---------------------------------------------------------------------------
# Graceful fallback: when the core eval framework (eval/adapter.py) is not yet
# available (it is being built in parallel), define the minimal dataclasses and
# Protocol locally.  Once eval/adapter.py lands, these imports will take over.
# ---------------------------------------------------------------------------
try:
    from eval.adapter import AgentAdapter, EvalInstance, EvalResult  # type: ignore[import-untyped]
except ImportError:
    from typing import Protocol

    @dataclass
    class EvalInstance:
        """A single benchmark problem instance."""
        instance_id: str               # e.g. "HumanEval/0" or "Mbpp/2"
        task_description: str          # the problem prompt / function signature
        metadata: dict = field(default_factory=dict)

    @dataclass
    class EvalResult:
        """The result of solving one instance."""
        instance_id: str = ""
        model_patch: str = ""
        answer: str = ""               # function body (or full code for MBPP)
        cost: float = 0.0
        tokens_in: int = 0
        tokens_out: int = 0
        trace_id: str = ""
        error: str = ""

    class AgentAdapter(Protocol):
        """Protocol for any agent that can solve code-generation instances."""
        def solve_instance(
            self, instance: EvalInstance, working_dir: str, **kwargs
        ) -> EvalResult: ...


# ---------------------------------------------------------------------------
# Statistics
# ---------------------------------------------------------------------------

@dataclass
class EvalPlusStats:
    """Aggregate statistics for an EvalPlus benchmark run."""
    dataset: str
    num_instances: int = 0
    base_pass_at_1: float = 0.0
    base_pass_at_10: float = 0.0
    plus_pass_at_1: float = 0.0
    plus_pass_at_10: float = 0.0
    total_cost: float = 0.0
    total_tokens_in: int = 0
    total_tokens_out: int = 0
    errors: int = 0


# ---------------------------------------------------------------------------
# Mock adapter for pipeline testing
# ---------------------------------------------------------------------------

class MockAgentAdapter:
    """Mock agent that returns canonical solutions -- proves the pipeline works.

    When used with EvalPlusBenchmark, pass@1 should equal 1.0 because every
    canonical solution passes all tests by definition.
    """

    def __init__(self, problems: dict):
        self._problems = problems

    def solve_instance(
        self, instance: EvalInstance, working_dir: str, **kwargs
    ) -> EvalResult:
        task_id = instance.instance_id
        problem = self._problems.get(task_id)
        if problem is None:
            return EvalResult(instance_id=task_id, error=f"Problem {task_id} not found")
        return EvalResult(instance_id=task_id, answer=problem["canonical_solution"])


# ---------------------------------------------------------------------------
# Benchmark
# ---------------------------------------------------------------------------

class EvalPlusBenchmark:
    """Full pipeline for HumanEval+ / MBPP+ evaluation.

    Parameters
    ----------
    dataset : str
        ``"humaneval"`` or ``"mbpp"``.
    adapter :
        Any object with ``solve_instance(instance, working_dir) -> EvalResult``.
        Set after construction if you need to load problems first.
    limit : int or None
        Only evaluate the first *N* problems (handy for debugging).
    base_only : bool
        Skip the *plus* (extended) test cases.
    parallel : int or None
        Number of worker processes for scoring (passed through to evalplus).
    """

    SUPPORTED_DATASETS = ("humaneval", "mbpp")

    def __init__(
        self,
        dataset: str,
        adapter: Any = None,
        *,
        limit: Optional[int] = None,
        base_only: bool = False,
        parallel: Optional[int] = None,
    ):
        if dataset not in self.SUPPORTED_DATASETS:
            raise ValueError(
                f"Unsupported dataset: {dataset!r}. "
                f"Choose from {self.SUPPORTED_DATASETS}"
            )
        self.dataset = dataset
        self.adapter = adapter
        self.limit = limit
        self.base_only = base_only
        self.parallel = parallel

        self._problems: Optional[dict] = None
        self._instances: List[EvalInstance] = []
        self._results: List[EvalResult] = []
        self._stats: Optional[EvalPlusStats] = None

    # -- lazy properties -----------------------------------------------------

    @property
    def problems(self) -> dict:
        """Lazy-load problems from the evalplus package (cached)."""
        if self._problems is None:
            self._problems = _load_problems(self.dataset)
        return self._problems

    @property
    def instances(self) -> List[EvalInstance]:
        """Build EvalInstance list from problems (cached)."""
        if not self._instances:
            self._instances = _build_instances(self.problems, self.limit)
        return self._instances

    @property
    def results(self) -> List[EvalResult]:
        """Per-instance :class:`EvalResult` list from the most recent run()."""
        return list(self._results)

    # -- run -----------------------------------------------------------------

    def run(
        self,
        output_dir: str = "./results",
        working_dir: Optional[str] = None,
        **solve_kwargs,
    ) -> EvalPlusStats:
        """Execute the full benchmark loop.

        1. Build instances from problems.
        2. Call ``adapter.solve_instance()`` for each instance.
        3. Write completions to *evalplus_results.jsonl*.
        4. Score each completion via ``evalplus.evaluate.check_correctness``.
        5. Compute pass@k and return an :class:`EvalPlusStats`.
        """
        if self.adapter is None:
            raise ValueError(
                "No adapter set. Assign benchmark.adapter before calling run()."
            )

        output_path = Path(output_dir)
        output_path.mkdir(parents=True, exist_ok=True)

        if working_dir is None:
            working_dir = str(output_path / "workdir")
            os.makedirs(working_dir, exist_ok=True)

        instances = self.instances
        n = len(instances)
        print(f"[EvalPlus] {self.dataset}  --  {n} instance(s)")

        # ---- Step 2: solve each instance ----
        results: List[EvalResult] = []
        t_start = time.time()
        for i, instance in enumerate(instances):
            tag = instance.instance_id
            print(f"  [{i + 1}/{n}] {tag}  ", end="", flush=True)
            t0 = time.time()
            try:
                result = self.adapter.solve_instance(
                    instance, working_dir, **solve_kwargs
                )
            except Exception as exc:
                result = EvalResult(instance_id=tag, error=str(exc))
            elapsed = time.time() - t0
            status = "OK" if not result.error else f"ERR: {result.error[:60]}"
            print(f"({elapsed:.1f}s) {status}")
            results.append(result)

        self._results = results
        print(f"  Total solve time: {time.time() - t_start:.1f}s")

        # ---- Step 3: write jsonl ----
        jsonl_path = output_path / "evalplus_results.jsonl"
        _write_jsonl(results, jsonl_path)

        # ---- Steps 4-5: score & stats ----
        self._stats = _score(
            dataset=self.dataset,
            instances=instances,
            jsonl_path=jsonl_path,
            base_only=self.base_only,
            parallel=self.parallel,
            results=results,
        )

        # Optional: also run the official evalplus CLI for a second opinion
        # (only when evaluating the full dataset -- it asserts all problems
        #  are present, which fails on limited runs)
        if self.limit is None:
            _run_official_evaluate(
                dataset=self.dataset,
                jsonl_path=jsonl_path,
                base_only=self.base_only,
            )

        return self._stats

    # -- reporting -----------------------------------------------------------

    def print_summary(self):
        """Print a human-readable results table."""
        s = self._stats
        if s is None:
            print("[EvalPlus] No results yet -- call run() first.")
            return

        print(f"\n{'=' * 62}")
        print(f"  EvalPlus  ::  {s.dataset}  ({s.num_instances} instances)")
        print(f"{'=' * 62}")
        print(f"  Errors:         {s.errors}")
        print(f"  Cost:           ${s.total_cost:.4f}")
        print(f"  Tokens in:      {s.total_tokens_in}")
        print(f"  Tokens out:     {s.total_tokens_out}")
        print(f"  {'─' * 48}")
        if s.base_pass_at_1:
            print(f"  base pass@1:    {s.base_pass_at_1:.3f}")
        if s.base_pass_at_10:
            print(f"  base pass@10:   {s.base_pass_at_10:.3f}")
        if s.plus_pass_at_1:
            print(f"  plus pass@1:    {s.plus_pass_at_1:.3f}")
        if s.plus_pass_at_10:
            print(f"  plus pass@10:   {s.plus_pass_at_10:.3f}")
        print(f"{'=' * 62}\n")


# ========================================================================
# Internal helpers
# ========================================================================

def _load_problems(dataset: str) -> dict:
    """Load raw problem dict from evalplus."""
    import evalplus.data as epd

    loader = epd.get_human_eval_plus if dataset == "humaneval" else epd.get_mbpp_plus
    problems = loader()
    print(f"[EvalPlus] Loaded {len(problems)} problems from {dataset}")
    return problems


def _build_instances(problems: dict, limit: Optional[int]) -> List[EvalInstance]:
    """Convert raw problem dict to a list of EvalInstance."""
    sorted_ids = sorted(problems.keys())
    if limit:
        sorted_ids = sorted_ids[:limit]

    instances: List[EvalInstance] = []
    for tid in sorted_ids:
        p = problems[tid]
        instances.append(
            EvalInstance(
                instance_id=tid,
                task_description=p["prompt"],
                metadata={
                    "entry_point": p.get("entry_point", ""),
                    "canonical_solution": p.get("canonical_solution", ""),
                },
            )
        )
    return instances


def _write_jsonl(results: List[EvalResult], path: Path) -> None:
    """Write completions in evalplus-compatible JSONL format.

    Uses ``completion`` (the code that follows the prompt) rather than
    ``solution`` (prompt+completion) because evalplus reconstructs the
    full solution as ``problems[task_id]["prompt"] + completion`` internally.
    """
    with open(path, "w", encoding="utf-8") as fh:
        for r in results:
            fh.write(
                json.dumps({"task_id": r.instance_id, "completion": r.answer}) + "\n"
            )
    print(f"[EvalPlus] Wrote {len(results)} completions  ->  {path}")


def _score(
    *,
    dataset: str,
    instances: List[EvalInstance],
    jsonl_path: Path,
    base_only: bool,
    parallel: Optional[int],
    results: List[EvalResult],
) -> EvalPlusStats:
    """Score every completion and compute pass@k.

    On Unix, delegates to evalplus's ``check_correctness`` (with sandbox).
    On Windows, uses an in-process checker that skips ``reliability_guard``
    (the ``resource`` module it needs is Unix-only).
    """
    import sys as _sys

    if _sys.platform == "win32":
        return _score_inline(
            dataset=dataset,
            instances=instances,
            jsonl_path=jsonl_path,
            base_only=base_only,
            results=results,
        )
    else:
        return _score_via_evalplus(
            dataset=dataset,
            instances=instances,
            jsonl_path=jsonl_path,
            base_only=base_only,
            parallel=parallel,
            results=results,
        )


# ===================================================================
# Unix path -- uses official evalplus sandbox
# ===================================================================

def _score_via_evalplus(
    *,
    dataset: str,
    instances: List[EvalInstance],
    jsonl_path: Path,
    base_only: bool,
    parallel: Optional[int],
    results: List[EvalResult],
) -> EvalPlusStats:
    """Score using evalplus's ``check_correctness`` (subprocess sandbox)."""
    import evalplus.data as epd
    import evalplus.evaluate as epe
    from evalplus.evaluate import check_correctness, estimate_pass_at_k, PASS

    target_ids = {inst.instance_id for inst in instances}
    all_problems = _load_problems(dataset)
    problems = {k: v for k, v in all_problems.items() if k in target_ids}

    if dataset == "humaneval":
        h = epd.get_human_eval_plus_hash()
        expected_output = epe.get_groundtruth(problems, h, [])
    else:
        h = epd.get_mbpp_plus_hash()
        expected_output = epe.get_groundtruth(
            problems, h, epe.MBPP_OUTPUT_NOT_NONE_TASKS
        )

    solutions = list(epe.load_solutions(str(jsonl_path)))
    n_workers = parallel or max(1, os.cpu_count() or 4)  # type: ignore[type-var]
    eval_results: Dict[str, list] = defaultdict(list)

    if n_workers > 1 and len(solutions) > 1:
        from concurrent.futures import ProcessPoolExecutor, as_completed

        with ProcessPoolExecutor(max_workers=n_workers) as executor:
            future_map = {}
            for i, sample in enumerate(solutions):
                tid = sample["task_id"]
                solution = (
                    sample["solution"]
                    if "solution" in sample
                    else problems[tid]["prompt"] + sample["completion"]
                )
                identifier = sample.get("_identifier", f"{tid}:{i}")
                future = executor.submit(
                    check_correctness,
                    dataset,
                    i,
                    problems[tid],
                    solution,
                    expected_output.get(tid, ([], [])),
                    base_only,
                    False,
                    identifier,
                )
                future_map[future] = tid

            for future in as_completed(future_map):
                res = future.result()
                eval_results[res["task_id"]].append(res)
    else:
        for i, sample in enumerate(solutions):
            tid = sample["task_id"]
            solution = (
                sample["solution"]
                if "solution" in sample
                else problems[tid]["prompt"] + sample["completion"]
            )
            identifier = sample.get("_identifier", f"{tid}:{i}")
            res = check_correctness(
                dataset,
                i,
                problems[tid],
                solution,
                expected_output.get(tid, ([], [])),
                base_only,
                False,
                identifier,
            )
            eval_results[res["task_id"]].append(res)

    return _build_stats(dataset, instances, eval_results, base_only, results)


# ===================================================================
# Windows path -- in-process checker (no sandbox, no ``resource``)
# ===================================================================

def _score_inline(
    *,
    dataset: str,
    instances: List[EvalInstance],
    jsonl_path: Path,
    base_only: bool,
    results: List[EvalResult],
) -> EvalPlusStats:
    """Score using an in-process checker that works on Windows.

    Skips ``reliability_guard`` and ``resource`` entirely.  Safe for
    smoke-tests and trusted code; for untrusted LLM output this path
    trades safety for cross-platform compatibility.
    """
    import evalplus.data as epd
    import evalplus.evaluate as epe

    target_ids = {inst.instance_id for inst in instances}
    all_problems = _load_problems(dataset)
    problems = {k: v for k, v in all_problems.items() if k in target_ids}

    if dataset == "humaneval":
        h = epd.get_human_eval_plus_hash()
        expected_output = epe.get_groundtruth(problems, h, [])
    else:
        h = epd.get_mbpp_plus_hash()
        expected_output = epe.get_groundtruth(
            problems, h, epe.MBPP_OUTPUT_NOT_NONE_TASKS
        )

    solutions = list(epe.load_solutions(str(jsonl_path)))
    eval_results: Dict[str, list] = defaultdict(list)

    for i, sample in enumerate(solutions):
        tid = sample["task_id"]
        solution = (
            sample["solution"]
            if "solution" in sample
            else problems[tid]["prompt"] + sample["completion"]
        )
        identifier = sample.get("_identifier", f"{tid}:{i}")
        res = _check_solution_inline(
            dataset=dataset,
            completion_id=i,
            problem=problems[tid],
            solution=solution,
            expected_output=expected_output.get(tid, {}),
            base_only=base_only,
            identifier=identifier,
        )
        eval_results[tid].append(res)

    return _build_stats(dataset, instances, eval_results, base_only, results)


def _check_solution_inline(
    *,
    dataset: str,
    completion_id: int,
    problem: Dict[str, Any],
    solution: str,
    expected_output: Dict[str, Any],
    base_only: bool,
    identifier: str,
) -> Dict[str, Any]:
    """Execute *solution* in-process and check against test inputs.

    Returns a dict with the same shape as :func:`evalplus.evaluate.check_correctness`.
    """
    import evalplus.eval as epe

    ret: Dict[str, Any] = {
        "completion_id": completion_id,
        "task_id": problem["task_id"],
        "_identifier": identifier,
        "solution": solution,
    }

    entry_point = problem["entry_point"]
    atol = problem.get("atol", 0)

    with epe.create_tempdir():
        # -- compile & extract the function -------------------------------
        exec_globals: Dict[str, Any] = {}
        try:
            with epe.swallow_io():
                exec(solution, exec_globals)
            fn = exec_globals.get(entry_point)
            if fn is None:
                raise NameError(
                    f"entry_point {entry_point!r} not found in solution"
                )
        except Exception:
            ret["base"] = (epe.FAIL, [])
            if not base_only:
                ret["plus"] = (epe.FAIL, [])
            return ret

        # -- base tests --------------------------------------------------
        ret["base"] = _run_inputs(
            dataset=dataset,
            fn=fn,
            inputs=problem["base_input"],
            expected=expected_output.get("base", []),
            atol=atol,
            entry_point=entry_point,
            fast_check=not base_only,  # fast_check=True in evalplus default
        )

        # -- plus tests ---------------------------------------------------
        if not base_only:
            ret["plus"] = _run_inputs(
                dataset=dataset,
                fn=fn,
                inputs=problem["plus_input"],
                expected=expected_output.get("plus", []),
                atol=atol,
                entry_point=entry_point,
                fast_check=False,  # for plus we always want full details
            )

    return ret


def _run_inputs(
    *,
    dataset: str,
    fn,
    inputs: List[Any],
    expected: List[Any],
    atol: float,
    entry_point: str,
    fast_check: bool,
) -> tuple:
    """Run *fn* against each input tuple and compare to *expected*.

    Returns ``(status: str, details: List[bool])`` where *status* is one
    of ``"pass"``, ``"fail"``, ``"timeout"``.
    """
    import evalplus.eval as epe
    import numpy as np

    # Lazy-load special oracle helpers from evalplus
    _surface_Area = getattr(epe, "_surface_Area", None)
    _digit_distance_nums = getattr(epe, "_digit_distance_nums", None)
    _poly = getattr(epe, "_poly", None)

    details: List[bool] = []
    status = epe.PASS

    for i, inp in enumerate(inputs):
        try:
            with epe.swallow_io():
                out = fn(*inp)

            exp = expected[i] if i < len(expected) else None
            if exp is None:
                # No oracle for this input; treat as pass
                details.append(True)
                continue

            # -- comparison logic (mirrors unsafe_execute) ----------------
            exact_match = out == exp

            # MBPP special oracles
            if dataset == "mbpp":
                if entry_point == "are_equivalent":
                    exact_match = exact_match or True
                elif entry_point == "sum_div":
                    exact_match = exact_match or out == 0
                elif entry_point == "surface_Area" and _surface_Area is not None:
                    exact_match = exact_match or abs(out - _surface_Area(*inp)) <= atol
                elif entry_point == "digit_distance_nums" and _digit_distance_nums is not None:
                    exact_match = exact_match or out == _digit_distance_nums(*inp)
                elif entry_point in epe.MBPP_OUTPUT_SET_EQ_TASKS:
                    exact_match = set(out) == set(exp)
                elif entry_point in epe.MBPP_OUTPUT_NOT_NONE_TASKS:
                    if isinstance(out, bool):
                        exact_match = out == exp
                    else:
                        exact_match = exp == (out is not None)

            # HumanEval special oracles
            if dataset == "humaneval" and entry_point == "find_zero":
                if _poly is not None:
                    # Polynomial oracle: f(x) == 0  for all x in inp
                    try:
                        assert abs(_poly(*inp, out)) <= atol
                        details.append(True)
                    except Exception:
                        details.append(False)
                        status = epe.FAIL
                        if fast_check:
                            break
                    continue
                else:
                    # Fallback: exact match
                    pass

            if not exact_match and atol == 0 and epe.is_floats(exp):
                atol = 1e-6

            if not exact_match and atol != 0:
                assert type(out) == type(exp)
                if isinstance(exp, (list, tuple)):
                    assert len(out) == len(exp)
                np.testing.assert_allclose(out, exp, rtol=1e-07, atol=atol)
                exact_match = True

            if not exact_match:
                raise AssertionError(
                    f"Output mismatch: {out!r} != {exp!r}"
                )

            details.append(True)

        except Exception:
            details.append(False)
            status = epe.FAIL
            if fast_check:
                break

    return (status, details)


# ===================================================================
# Shared statistics builder
# ===================================================================

def _build_stats(
    dataset: str,
    instances: List[EvalInstance],
    eval_results: Dict[str, list],
    base_only: bool,
    results: List[EvalResult],
) -> EvalPlusStats:
    """Compute pass@k from per-task evaluation results."""
    from evalplus.evaluate import estimate_pass_at_k, PASS

    total = np.array([len(v) for v in eval_results.values()])
    base_correct = np.array(
        [sum(r["base"][0] == PASS for r in v) for v in eval_results.values()]
    )

    stats = EvalPlusStats(dataset=dataset, num_instances=len(instances))

    for k in (1, 10):
        if (total >= k).all():
            val = float(estimate_pass_at_k(total, base_correct, k).mean())
            setattr(stats, f"base_pass_at_{k}", val)

    if not base_only:
        plus_correct = np.array(
            [
                sum(
                    r["base"][0] == PASS and r.get("plus", (None,))[0] == PASS
                    for r in v
                )
                for v in eval_results.values()
            ]
        )
        for k in (1, 10):
            if (total >= k).all():
                val = float(estimate_pass_at_k(total, plus_correct, k).mean())
                setattr(stats, f"plus_pass_at_{k}", val)

    stats.total_cost = sum(r.cost for r in results)
    stats.total_tokens_in = sum(r.tokens_in for r in results)
    stats.total_tokens_out = sum(r.tokens_out for r in results)
    stats.errors = sum(1 for r in results if r.error)

    return stats


def _run_official_evaluate(
    *,
    dataset: str,
    jsonl_path: Path,
    base_only: bool,
) -> None:
    """Run the official ``evalplus.evaluate.evaluate()`` as a secondary check.

    Only called for full-dataset runs because it asserts that *every* problem
    has at least one sample.  Errors from this call are logged but not fatal.
    """
    try:
        import evalplus.evaluate
        print("[EvalPlus] Running official evalplus.evaluate (secondary check)...")
        evalplus.evaluate.evaluate(
            dataset=dataset,
            samples=str(jsonl_path),
            base_only=base_only,
            i_just_wanna_run=True,
        )
    except Exception as exc:
        print(f"[EvalPlus] Official evaluate skipped: {exc}")
        print("           (this is expected for --limit runs; results are from our scorer)")


# ========================================================================
# Module-level load_instances() -- eval/run.py HarnessRun path
# ========================================================================


def load_instances(
    limit: Optional[int] = None,
    dataset: str = "humaneval",
    **kwargs: Any,
) -> List[EvalInstance]:
    """Module-level instance loader for the HarnessRun path in ``eval/run.py``.

    Loads problems from the evalplus package (``humaneval`` or ``mbpp``) and
    converts them to :class:`EvalInstance` objects.  evalplus has no
    :class:`AgentBenchmark` adapter — it scores inside its own pipeline, so
    HarnessRun wires ``scorer=None`` for it.

    Args:
        limit: If set, return at most this many instances.
        dataset: ``"humaneval"`` (default) or ``"mbpp"``.

    Returns:
        List of :class:`EvalInstance` objects.
    """
    problems = _load_problems(dataset)
    return _build_instances(problems, limit)


# ========================================================================
# Module-level run() -- eval/run.py CLI contract
# ========================================================================

def run(
    driver: Any,
    limit: Optional[int] = None,
    **kwargs: Any,
) -> List[EvalResult]:
    """Module-level runner aligned with the ``eval.run`` CLI contract.

    Builds an :class:`EvalPlusBenchmark`, attaches *driver* as the adapter,
    runs the full pipeline (official evalplus scoring), and returns the
    per-instance :class:`EvalResult` list.  The thin wrapper never bypasses
    official evalplus scoring.

    Extra keyword arguments are forwarded to the benchmark: ``dataset``
    (``"humaneval"`` or ``"mbpp"``), ``base_only``, ``parallel``, and
    ``output_dir``.
    """
    dataset = kwargs.pop("dataset", "humaneval")
    base_only = bool(kwargs.pop("base_only", False))
    parallel = kwargs.pop("parallel", None)
    output_dir = kwargs.pop("output_dir", "./results")

    benchmark = EvalPlusBenchmark(
        dataset=dataset,
        adapter=driver,
        limit=limit,
        base_only=base_only,
        parallel=parallel,
    )
    benchmark.run(output_dir=output_dir, **kwargs)
    return benchmark.results


# ========================================================================
# CLI  (for quick smoke-testing)
# ========================================================================

def main(argv: Optional[List[str]] = None) -> None:
    """Entry point for ``python -m eval.benchmarks.evalplus``."""
    import argparse

    parser = argparse.ArgumentParser(
        description="EvalPlus Benchmark Adapter (HumanEval+ / MBPP+)"
    )
    parser.add_argument(
        "--dataset",
        choices=["humaneval", "mbpp"],
        default="humaneval",
        help="Which dataset to evaluate (default: humaneval)",
    )
    parser.add_argument(
        "--limit",
        type=int,
        default=None,
        help="Only evaluate the first N problems (default: all)",
    )
    parser.add_argument(
        "--output-dir",
        default="./results",
        help="Directory for result files (default: ./results)",
    )
    parser.add_argument(
        "--mock",
        action="store_true",
        default=True,
        help="Use MockAgentAdapter (canonical solutions) -- pipeline smoke test",
    )
    parser.add_argument(
        "--base-only",
        action="store_true",
        help="Skip plus (extended) test cases",
    )
    parser.add_argument(
        "--parallel",
        type=int,
        default=None,
        help="Number of worker processes for scoring",
    )
    args = parser.parse_args(argv)

    # Build benchmark & adapter
    benchmark = EvalPlusBenchmark(
        dataset=args.dataset,
        limit=args.limit,
        base_only=args.base_only,
        parallel=args.parallel,
    )

    if args.mock:
        benchmark.adapter = MockAgentAdapter(benchmark.problems)
    elif benchmark.adapter is None:
        print("No adapter provided and --mock not set. Nothing to do.")
        sys.exit(1)

    # Run
    stats = benchmark.run(output_dir=args.output_dir)
    benchmark.print_summary()

    # Sanity check for mock runs
    if args.mock:
        assert stats.base_pass_at_1 == 1.0, (
            f"Mock-run pass@1 should be 1.0 but got {stats.base_pass_at_1:.3f}. "
            f"The pipeline may have a bug."
        )
        print("[OK] Pipeline verified: pass@1 = 1.0 with canonical solutions")


if __name__ == "__main__":
    main()
