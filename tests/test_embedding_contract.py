from __future__ import annotations

import pytest

from scripts.embedding_contract import backend_name, native_dimensions, prepare_vectors


def test_bge_m3_selects_sentence_transformers_backend() -> None:
    assert backend_name("BAAI/bge-m3") == "sentence-transformers"
    assert native_dimensions("BAAI/bge-m3") == 1024


def test_bge_m3_rejects_dimension_resize_that_would_fake_native_vectors() -> None:
    with pytest.raises(ValueError, match="native dimension"):
        prepare_vectors("BAAI/bge-m3", [[0.1] * 512], 1024)


def test_bge_m3_accepts_only_actual_native_dimensions() -> None:
    vector = [0.1] * 1024
    assert prepare_vectors("BAAI/bge-m3", [vector], 1024) == [vector]


def test_non_bge_development_models_keep_legacy_resize_behavior() -> None:
    assert prepare_vectors("jinaai/dev", [[1.0, 2.0]], 3) == [[1.0, 2.0, 1.0]]
