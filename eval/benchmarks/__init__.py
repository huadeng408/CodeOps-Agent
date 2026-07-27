"""
eval.benchmarks  --  benchmark adapter collection for the code-agent eval harness.

Each module implements a standardized adapter for a specific benchmark family:

    =============  =============================
    Module          Benchmark
    =============  =============================
    ``evalplus``    HumanEval+ (164) / MBPP+ (378)
    =============  =============================

Every benchmark adapter follows the same contract:

1. Load or accept problem instances from the canonical dataset source.
2. Convert each problem to an ``EvalInstance`` (see ``eval.adapter``).
3. Call ``adapter.solve_instance(instance, working_dir)`` for every instance.
4. Write results in the benchmark-native format and score them.
"""

__all__ = [
    "EvalPlusBenchmark",
    "EvalPlusStats",
    "MockAgentAdapter",
]


def __getattr__(name: str):
    """Lazy-import public names to avoid circular/duplicate-import warnings."""
    if name in __all__:
        import importlib
        mod = importlib.import_module(".evalplus", __package__)
        return getattr(mod, name)
    raise AttributeError(f"module {__name__!r} has no attribute {name!r}")
