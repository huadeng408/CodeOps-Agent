from __future__ import annotations

import importlib.util
from pathlib import Path


def _module():
    path = Path(__file__).parents[1] / "scripts" / "eval" / "trace_assert.py"
    spec = importlib.util.spec_from_file_location("trace_assert", path)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def test_find_run_trace_selects_unique_trace_by_eval_run_id(monkeypatch) -> None:
    trace_assert = _module()
    run_id = "rag-run-42"
    trace_id = "a" * 32
    spans = [
        {
            "name": "retrieve orchestrator /knowledge-search",
            "context": {"trace_id": trace_id, "span_id": "b" * 16},
            "attributes": {"eval.run_id": run_id},
        },
        {
            "name": "historical",
            "context": {"trace_id": "c" * 32, "span_id": "d" * 16},
            "attributes": {"eval.run_id": "another-run"},
        },
    ]
    monkeypatch.setattr(trace_assert, "_get_json", lambda _url: {"data": spans})

    got_trace_id, got_spans = trace_assert.find_run_trace(
        "http://phoenix", "default", run_id, "2026-08-13T00:00:00Z"
    )

    assert got_trace_id == trace_id
    assert got_spans == [spans[0]]
