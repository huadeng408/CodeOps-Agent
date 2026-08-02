"""eval.contamination -- benchmark/corpus contamination scanner (plan Task 9.1).

Four-layer scan (exact hash / normalized n-gram / MinHash / embedding
nearest-neighbor) that isolates and reports overlap between benchmark
queries/answers/tests and production corpus chunks. Only reports — never
deletes data — and blocks release while unreviewed high-similarity items
remain.
"""

__all__ = [
    "ContaminationReport",
    "jaccard",
    "minhash_signature",
    "ngram_set",
    "normalize",
    "scan",
]


def __getattr__(name: str):
    """Lazy-import public names to avoid heavy imports at package load."""
    if name in __all__:
        import importlib

        mod = importlib.import_module(".scanner", __package__)
        return getattr(mod, name)
    raise AttributeError(f"module {__name__!r} has no attribute {name!r}")
