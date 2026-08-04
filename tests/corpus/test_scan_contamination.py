"""Contract tests for the corpus contamination scan driver (preflight gate).

The driver scrolls the production corpus index, loads the benchmark queries,
runs the four-layer scan (eval/contamination/scanner.py), and writes a JSONL
review report that gates release.

All tests are offline: ES scroll responses and the embedding service are
in-process fakes (``requests.get``/``requests.post`` monkeypatched); no real
ES, no real embedding service, no real scan of production data.
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest
import requests

from eval.contamination.scanner import ContaminationReport
from scripts.corpus import scan_contamination as sc


class FakeResp:
    def __init__(self, status_code: int, payload: dict | None = None) -> None:
        self.status_code = status_code
        self._payload = payload or {}

    def json(self) -> dict:
        return self._payload

    def raise_for_status(self) -> None:
        if self.status_code >= 400:
            raise requests.HTTPError(f"status {self.status_code}")


class FakeES:
    """In-process stand-in for the Elasticsearch scroll API.

    Pages are consumed in order: the first request (GET /<index>/_search)
    serves pages[0], every /_search/scroll continuation serves the next page.
    An exhausted fake serves an empty page without a scroll id, terminating
    the scroll loop.
    """

    def __init__(self, pages: list[list[tuple[str, dict]]]) -> None:
        self.pages = pages
        self.calls: list[dict] = []
        self.page_index = 0

    def get(self, url: str, params=None, json=None, timeout=None) -> FakeResp:
        self.calls.append(
            {"method": "GET", "url": url, "params": params, "json": json, "timeout": timeout}
        )
        return self._next_page()

    def post(self, url: str, params=None, json=None, timeout=None) -> FakeResp:
        self.calls.append(
            {"method": "POST", "url": url, "params": params, "json": json, "timeout": timeout}
        )
        return self._next_page()

    def _next_page(self) -> FakeResp:
        idx = self.page_index
        self.page_index += 1
        hits = []
        if idx < len(self.pages):
            hits = [{"_id": doc_id, "_source": source} for doc_id, source in self.pages[idx]]
        scroll_id = f"sid-{idx + 1}" if hits else None
        return FakeResp(200, {"_scroll_id": scroll_id, "hits": {"hits": hits}})


def patch_es(monkeypatch: pytest.MonkeyPatch, fake: FakeES) -> None:
    monkeypatch.setattr(sc.requests, "get", fake.get)
    monkeypatch.setattr(sc.requests, "post", fake.post)


def write_queries(path: Path, queries: list[str]) -> Path:
    """Write a queries.text.jsonl-shaped file ({"query_id","query"} lines)."""
    path.write_text(
        "\n".join(
            json.dumps({"query_id": f"q{i}", "query": query}) for i, query in enumerate(queries)
        )
        + "\n",
        encoding="utf-8",
    )
    return path


# ---------------------------------------------------------------------------
# Chunk fetching: scroll pagination + field selection (text_content first,
# embedding_text fallback, text-less docs skipped).
# ---------------------------------------------------------------------------


def test_fetch_chunks_scrolls_pages_and_selects_text_fields(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    pages = [
        [
            ("c1", {"text_content": "alpha text", "embedding_text": "ignored fallback"}),
            ("c2", {"text_content": "", "embedding_text": "beta fallback"}),
        ],
        [
            ("c3", {"text_content": "gamma text"}),
            ("c4", {"other_field": "no usable text"}),
        ],
    ]
    fake = FakeES(pages)
    patch_es(monkeypatch, fake)

    chunks = sc.fetch_chunks("http://es.local:9200", "my_index", batch_size=2)

    assert chunks == [("c1", "alpha text"), ("c2", "beta fallback"), ("c3", "gamma text")]
    # 首页：GET /<index>/_search，带 scroll 参数、match_all 与两个候选字段。
    first = fake.calls[0]
    assert first["method"] == "GET"
    assert first["url"] == "http://es.local:9200/my_index/_search"
    assert first["params"] == {"scroll": "2m"}
    assert first["json"]["size"] == 2
    assert first["json"]["query"] == {"match_all": {}}
    assert set(first["json"]["_source"]) == {"text_content", "embedding_text"}
    # 续页：POST /_search/scroll，逐次串联上一页返回的 scroll id。
    assert fake.calls[1]["url"] == "http://es.local:9200/_search/scroll"
    assert fake.calls[1]["json"] == {"scroll": "2m", "scroll_id": "sid-1"}
    assert fake.calls[2]["json"] == {"scroll": "2m", "scroll_id": "sid-2"}
    assert len(fake.calls) == 3


def test_fetch_chunks_uses_given_index_and_default_constant(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # 默认索引必须与导入侧 DEFAULT_TARGET_INDEX 一致。
    assert sc.DEFAULT_INDEX == "knowledge_base_v2_bge_m3"
    fake = FakeES([[("c1", {"text_content": "alpha text here"})]])
    patch_es(monkeypatch, fake)

    chunks = sc.fetch_chunks("http://es.local:9200/", "other_idx")

    assert chunks == [("c1", "alpha text here")]
    assert fake.calls[0]["url"] == "http://es.local:9200/other_idx/_search"


def test_fetch_chunks_raises_on_http_error(monkeypatch: pytest.MonkeyPatch) -> None:
    def fake_get(url, params=None, json=None, timeout=None):
        return FakeResp(500)

    monkeypatch.setattr(sc.requests, "get", fake_get)
    with pytest.raises(requests.HTTPError):
        sc.fetch_chunks("http://es.local:9200", "idx")


# ---------------------------------------------------------------------------
# Benchmark queries loading.
# ---------------------------------------------------------------------------


def test_load_queries_reads_query_field_and_skips_blank_or_missing(tmp_path: Path) -> None:
    path = tmp_path / "queries.text.jsonl"
    path.write_text(
        '{"query_id": "q1", "query": "first query"}\n'
        "\n"
        '{"query_id": "q2", "query": "second query"}\n'
        '{"query_id": "q3"}\n'
        '{"query_id": "q4", "query": ""}\n',
        encoding="utf-8",
    )
    assert sc.load_queries(path) == ["first query", "second query"]


def test_load_queries_missing_file_raises(tmp_path: Path) -> None:
    with pytest.raises(OSError):
        sc.load_queries(tmp_path / "does-not-exist.jsonl")


# ---------------------------------------------------------------------------
# Embedding fn construction: OpenAI-compatible POST, None degradation.
# ---------------------------------------------------------------------------


def test_build_embedding_fn_posts_openai_compatible_payload(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    calls: list[dict] = []

    def fake_post(url, params=None, json=None, timeout=None):
        calls.append({"url": url, "json": json, "timeout": timeout})
        return FakeResp(
            200, {"model": "BAAI/bge-m3", "data": [{"index": 0, "embedding": [0.1, 0.2, 0.3]}]}
        )

    monkeypatch.setattr(sc.requests, "post", fake_post)

    fn = sc.build_embedding_fn("http://127.0.0.1:8009/embeddings", timeout=45)
    assert fn is not None
    assert fn("hello world") == [0.1, 0.2, 0.3]

    # 一次探测 ping + 一次真实调用。
    assert len(calls) == 2
    assert calls[0]["json"] == {"model": "BAAI/bge-m3", "input": ["ping"]}
    assert calls[1]["url"] == "http://127.0.0.1:8009/embeddings"
    assert calls[1]["json"] == {"model": "BAAI/bge-m3", "input": ["hello world"]}
    assert calls[1]["timeout"] == 45


def test_build_embedding_fn_returns_none_when_unreachable(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    def fake_post(url, params=None, json=None, timeout=None):
        raise requests.ConnectionError("connection refused")

    monkeypatch.setattr(sc.requests, "post", fake_post)
    assert sc.build_embedding_fn("http://127.0.0.1:9999/embeddings") is None


def test_build_embedding_fn_returns_none_on_http_error(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    def fake_post(url, params=None, json=None, timeout=None):
        return FakeResp(503)

    monkeypatch.setattr(sc.requests, "post", fake_post)
    assert sc.build_embedding_fn("http://127.0.0.1:8009/embeddings") is None


def test_build_embedding_fn_tolerates_empty_data(monkeypatch: pytest.MonkeyPatch) -> None:
    # 200 但 data 为空：探测不视为不可达，空向量交给 scanner 优雅跳过。
    def fake_post(url, params=None, json=None, timeout=None):
        return FakeResp(200, {"data": []})

    monkeypatch.setattr(sc.requests, "post", fake_post)
    fn = sc.build_embedding_fn("http://127.0.0.1:8009/embeddings")
    assert fn is not None
    assert fn("anything") == []


# ---------------------------------------------------------------------------
# Report generation: exact/high_similarity -> JSONL rows.
# ---------------------------------------------------------------------------


def test_report_rows_map_exact_and_high_similarity_hits() -> None:
    report = ContaminationReport(
        exact_matches=[(0, 1)],
        high_similarity=[(1, 0, 0.91, "ngram"), (2, 0, 0.85, "minhash")],
        blocking=True,
    )
    rows = sc.report_to_jsonl_rows(
        report, ["chunk-a", "chunk-b"], ["query zero", "query one", "query two"]
    )
    assert rows == [
        {"id": "chunk-b", "source": "query zero", "similarity": 1.0, "reviewed": False},
        {"id": "chunk-a", "source": "query one", "similarity": 0.91, "reviewed": False},
        {"id": "chunk-a", "source": "query two", "similarity": 0.85, "reviewed": False},
    ]


def test_report_rows_dedupe_pair_keeping_exact_match() -> None:
    report = ContaminationReport(
        exact_matches=[(0, 0)],
        high_similarity=[(0, 0, 0.9, "ngram"), (0, 0, 0.95, "minhash")],
        blocking=True,
    )
    rows = sc.report_to_jsonl_rows(report, ["chunk-a"], ["query zero"])
    assert rows == [
        {"id": "chunk-a", "source": "query zero", "similarity": 1.0, "reviewed": False}
    ]


def test_report_rows_fall_back_to_index_id_when_chunk_id_unknown() -> None:
    report = ContaminationReport(high_similarity=[(3, 7, 0.82, "embedding")], blocking=True)
    rows = sc.report_to_jsonl_rows(report, [], ["q0", "q1", "q2", "query three"])
    assert rows == [
        {"id": "3:7", "source": "query three", "similarity": 0.82, "reviewed": False}
    ]


def test_report_rows_truncate_long_source_summary() -> None:
    long_query = "x" * 200
    report = ContaminationReport(exact_matches=[(0, 0)], blocking=True)
    rows = sc.report_to_jsonl_rows(report, ["chunk-a"], [long_query])
    assert rows[0]["source"] == "x" * 120
    assert len(rows[0]["source"]) <= 120


def test_write_jsonl_report_creates_parent_dirs_and_writes_lines(tmp_path: Path) -> None:
    out = tmp_path / "nested" / "dir" / "report.jsonl"
    rows = [
        {"id": "c1", "source": "查询一", "similarity": 1.0, "reviewed": False},
        {"id": "c2", "source": "query two", "similarity": 0.9, "reviewed": False},
    ]
    sc.write_jsonl_report(out, rows)
    lines = out.read_text(encoding="utf-8").splitlines()
    assert [json.loads(line) for line in lines] == rows
    # CJK 不做 ASCII 转义，保持报告可读。
    assert "查询一" in lines[0]


# ---------------------------------------------------------------------------
# Exit codes: blocking -> 1, clean -> 0, IO/parameter errors -> 2.
# ---------------------------------------------------------------------------


def test_main_blocking_exact_match_exit_1(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    queries = write_queries(tmp_path / "queries.jsonl", ["def foo(): return 42"])
    out = tmp_path / "report.jsonl"
    monkeypatch.setattr(
        sc,
        "fetch_chunks",
        lambda *args, **kwargs: [
            ("doc-1", "def foo(): return 42"),
            ("doc-2", "unrelated corpus text about gardening and tomatoes"),
        ],
    )
    monkeypatch.setattr(sc, "build_embedding_fn", lambda *args, **kwargs: None)

    rc = sc.main(["--queries", str(queries), "--out", str(out)])

    assert rc == 1
    lines = [json.loads(line) for line in out.read_text(encoding="utf-8").splitlines()]
    assert lines == [
        {"id": "doc-1", "source": "def foo(): return 42", "similarity": 1.0, "reviewed": False}
    ]
    stdout = capsys.readouterr().out
    assert "BLOCKING" in stdout


def test_main_blocking_near_duplicate_exit_1(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    chunk = "The quick brown fox jumps over the lazy dog."
    query = "the quick, brown fox jumps over the lazy dog!"
    queries = write_queries(tmp_path / "queries.jsonl", [query])
    out = tmp_path / "report.jsonl"
    monkeypatch.setattr(sc, "fetch_chunks", lambda *args, **kwargs: [("doc-9", chunk)])
    monkeypatch.setattr(sc, "build_embedding_fn", lambda *args, **kwargs: None)

    rc = sc.main(["--queries", str(queries), "--out", str(out)])

    assert rc == 1
    lines = [json.loads(line) for line in out.read_text(encoding="utf-8").splitlines()]
    assert len(lines) == 1
    assert lines[0]["id"] == "doc-9"
    assert lines[0]["similarity"] >= 0.8


def test_main_clean_exit_0(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    queries = write_queries(
        tmp_path / "queries.jsonl", ["How do I parse a TOML configuration file in Python?"]
    )
    out = tmp_path / "report.jsonl"
    monkeypatch.setattr(
        sc,
        "fetch_chunks",
        lambda *args, **kwargs: [
            ("doc-1", "Goroutines are lightweight concurrent threads of execution in Go.")
        ],
    )
    monkeypatch.setattr(sc, "build_embedding_fn", lambda *args, **kwargs: None)

    rc = sc.main(["--queries", str(queries), "--out", str(out)])

    assert rc == 0
    assert out.read_text(encoding="utf-8") == ""
    assert "clean" in capsys.readouterr().out


def test_main_returns_2_when_es_unreachable(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    queries = write_queries(tmp_path / "queries.jsonl", ["any query"])

    def boom(*args, **kwargs):
        raise requests.ConnectionError("connection refused")

    monkeypatch.setattr(sc, "fetch_chunks", boom)
    monkeypatch.setattr(sc, "build_embedding_fn", lambda *args, **kwargs: None)

    rc = sc.main(["--queries", str(queries), "--out", str(tmp_path / "report.jsonl")])
    assert rc == 2


def test_main_returns_2_when_queries_file_missing(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(sc, "fetch_chunks", lambda *args, **kwargs: [])
    rc = sc.main(
        ["--queries", str(tmp_path / "nope.jsonl"), "--out", str(tmp_path / "r.jsonl")]
    )
    assert rc == 2


def test_main_returns_2_when_queries_file_malformed(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    bad = tmp_path / "queries.jsonl"
    bad.write_text("{not json}\n", encoding="utf-8")
    monkeypatch.setattr(sc, "fetch_chunks", lambda *args, **kwargs: [])
    rc = sc.main(["--queries", str(bad), "--out", str(tmp_path / "r.jsonl")])
    assert rc == 2


def test_main_threads_cli_overrides_to_fetch_and_embedding(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    queries = write_queries(tmp_path / "queries.jsonl", ["some query text"])
    seen: dict = {}

    def fake_fetch(es_url, index, *, batch_size, http_timeout, **kwargs):
        seen["es_url"] = es_url
        seen["index"] = index
        seen["batch_size"] = batch_size
        seen["http_timeout"] = http_timeout
        return []

    def fake_build(embedding_url, **kwargs):
        seen["embedding_url"] = embedding_url
        return None

    monkeypatch.setattr(sc, "fetch_chunks", fake_fetch)
    monkeypatch.setattr(sc, "build_embedding_fn", fake_build)

    rc = sc.main(
        [
            "--es", "http://es.local:9201",
            "--index", "other_idx",
            "--queries", str(queries),
            "--embedding-url", "http://embed.local:8010/embeddings",
            "--out", str(tmp_path / "report.jsonl"),
            "--batch-size", "7",
            "--http-timeout", "33",
        ]
    )

    assert rc == 0
    assert seen == {
        "es_url": "http://es.local:9201",
        "index": "other_idx",
        "batch_size": 7,
        "http_timeout": 33,
        "embedding_url": "http://embed.local:8010/embeddings",
    }


def test_main_passes_chunk_texts_and_scan_parameters(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    queries = write_queries(tmp_path / "queries.jsonl", ["query text one"])
    captured: dict = {}

    def fake_scan(chunks, benchmark_items, embedding_fn=None, ngram=8, minhash_bands=32, threshold=0.8):
        captured["chunks"] = chunks
        captured["items"] = benchmark_items
        captured["embedding_fn"] = embedding_fn
        captured["ngram"] = ngram
        captured["minhash_bands"] = minhash_bands
        captured["threshold"] = threshold
        return ContaminationReport()

    def sentinel(text: str) -> list[float]:
        return [1.0]

    monkeypatch.setattr(sc, "fetch_chunks", lambda *args, **kwargs: [("doc-1", "chunk text one")])
    monkeypatch.setattr(sc, "build_embedding_fn", lambda *args, **kwargs: sentinel)
    monkeypatch.setattr(sc, "scan", fake_scan)

    rc = sc.main(
        [
            "--queries", str(queries),
            "--out", str(tmp_path / "report.jsonl"),
            "--threshold", "0.9",
            "--ngram", "5",
            "--minhash-bands", "16",
        ]
    )

    assert rc == 0
    assert captured["chunks"] == ["chunk text one"]
    assert captured["items"] == ["query text one"]
    assert captured["embedding_fn"] is sentinel
    assert captured["ngram"] == 5
    assert captured["minhash_bands"] == 16
    assert captured["threshold"] == 0.9
