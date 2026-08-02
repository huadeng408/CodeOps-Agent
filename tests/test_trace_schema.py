"""Trace schema golden tests (design spec §6.1, plan Task 6.1).

The RAG attribute names below MUST match the Go side
(internal/telemetry/genai/schema_test.go). Phoenix join tests depend on
these exact keys; renaming one side breaks cross-language trace queries.
"""

from __future__ import annotations

from orchestrator.rag.trace import RAG_ATTRIBUTE_NAMES, TraceSpan


def test_rag_attribute_names_match_go_golden() -> None:
    # Keep in sync with internal/telemetry/genai/schema_test.go
    want = [
        "rag.corpus_generation",
        "rag.index_alias",
        "rag.index_physical",
        "rag.mapping_version",
        "rag.query_hash",
        "rag.top_n",
        "rag.retrieval_mode",
        "rag.reranker_applied",
        "rag.visual_path",
        "rag.document_hash",
        "rag.document_length",
    ]
    assert RAG_ATTRIBUTE_NAMES == want


def test_trace_span_builder_sets_standard_attributes() -> None:
    span = TraceSpan(name="rag.retrieve", operation="retrieve", provider="elasticsearch")
    span.set_query_hash("abc123")
    span.set_top_n(10)
    span.set_corpus("techdocs-2026-07-30-v1")
    span.set_index_alias("knowledge_base_current")
    span.set_retrieval_mode("hybrid")
    span.set_reranker_applied(True)
    span.set_visual_path("disabled")

    attrs = span.attributes()
    assert attrs["gen_ai.operation.name"] == "retrieve"
    assert attrs["gen_ai.provider.name"] == "elasticsearch"
    assert attrs["rag.query_hash"] == "abc123"
    assert attrs["rag.top_n"] == 10
    assert attrs["rag.corpus_generation"] == "techdocs-2026-07-30-v1"
    assert attrs["rag.index_alias"] == "knowledge_base_current"
    assert attrs["rag.retrieval_mode"] == "hybrid"
    assert attrs["rag.reranker_applied"] is True
    assert attrs["rag.visual_path"] == "disabled"
    # Privacy: no raw query/document content in span attributes.
    for value in attrs.values():
        assert "raw query" not in str(value)
