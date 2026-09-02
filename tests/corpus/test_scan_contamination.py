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
import sys
from pathlib import Path

import pytest
import requests

from eval.contamination.scanner import ContaminationReport
from scripts.corpus import scan_contamination as sc

REPO_ROOT = Path(__file__).resolve().parents[2]


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


@pytest.fixture(autouse=True)
def _forbid_unpatched_network(monkeypatch: pytest.MonkeyPatch) -> None:
    """Fail loudly instead of silently reaching a live service.

    本文件全部离线。main 的 embedding 入口只有 build_embedding_batch_fn 一个，
    但历史用例只 patch 了 build_embedding_fn——改动之后那些调用会静默打到本机
    真实的 :8009 上，第 4 层真的跑了、判决被悄悄改写，而失败看起来只像
    「断言写错」。把裸网络调用变成显式失败，这类串台就不会再无声发生。
    用例自己的 monkeypatch 在本 fixture 之后生效，因此仍可正常覆盖。
    """

    def refuse(*args, **kwargs):
        raise AssertionError(
            "unpatched network call in an offline test: patch sc.fetch_chunks / "
            "sc.build_embedding_batch_fn, or monkeypatch sc.requests explicitly"
        )

    monkeypatch.setattr(sc.requests, "get", refuse)
    monkeypatch.setattr(sc.requests, "post", refuse)


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
    monkeypatch.setattr(sc, "build_embedding_batch_fn", lambda *args, **kwargs: None)

    rc = sc.main(
        [
            "--queries", str(queries),
            "--out", str(out),
            "--manifest", str(tmp_path / "manifest.json"),
            # 本例只考层 1-3，显式走重新编码路线，不牵扯库内向量。
            "--chunk-vector-field", "",
        ]
    )

    # BLOCKING 优先于 INCOMPLETE：即使第 4 层被跳过，命中就是 rc 1。
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
    monkeypatch.setattr(sc, "build_embedding_batch_fn", lambda *args, **kwargs: None)

    rc = sc.main(
        [
            "--queries", str(queries),
            "--out", str(out),
            "--manifest", str(tmp_path / "manifest.json"),
            # 本例只考层 1-3，显式走重新编码路线，不牵扯库内向量。
            "--chunk-vector-field", "",
        ]
    )

    # BLOCKING 优先于 INCOMPLETE：即使第 4 层被跳过，命中依然判 1。
    assert rc == 1
    lines = [json.loads(line) for line in out.read_text(encoding="utf-8").splitlines()]
    assert len(lines) == 1
    assert lines[0]["id"] == "doc-9"
    assert lines[0]["similarity"] >= 0.8


def test_main_clean_exit_0(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    """CLEAN + exit 0 只有在四层全部真实跑完时才允许。

    冻结策略 v1 的 skipped_layer_rule 规定：任何一层被跳过都必须降级为
    INCOMPLETE（rc 3），绝不能输出 clean。所以这个用例必须注入一个可用的
    embedding_fn，让第 4 层真正执行——rc 0 是挣来的，不是在跳层状态下断言的。

    这里显式走 --chunk-vector-field ''：库内向量成为默认之后，「向服务重新编码
    整个语料」这条路线仍必须可用（新索引还没写入向量时只有它能跑），所以要有
    用例守着它，而不是让它随默认切换一起烂掉。
    """
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
    # 正交单位向量：查询与 chunk 的余弦相似度恒为 0，CLEAN 不可能是运气。
    vectors = {
        "How do I parse a TOML configuration file in Python?": [1.0, 0.0],
        "Goroutines are lightweight concurrent threads of execution in Go.": [0.0, 1.0],
    }
    calls: list[str] = []

    def embedder(text: str) -> list[float]:
        calls.append(text)
        return vectors.get(text, [0.0, 0.0])

    def embed_batch(texts: list[str]) -> list[list[float]]:
        # main 只有批量这一个 embedding 入口，逐条走 embedder 以保留 calls 计数。
        return [embedder(text) for text in texts]

    monkeypatch.setattr(sc, "build_embedding_batch_fn", lambda *args, **kwargs: embed_batch)

    rc = sc.main(
        [
            "--queries", str(queries),
            "--out", str(out),
            "--manifest", str(tmp_path / "manifest.json"),
            # 显式守住重新编码路线：它不再是默认，但必须继续能跑。
            "--chunk-vector-field", "",
        ]
    )

    assert rc == 0
    assert out.read_text(encoding="utf-8") == ""
    assert "clean" in capsys.readouterr().out.lower()
    # 第 4 层真的被调用过，rc 0 不是靠跳层换来的。
    assert calls, "embedding layer never ran, so exit 0 would be a false clean"

    manifest = json.loads((tmp_path / "manifest.json").read_text(encoding="utf-8"))
    assert manifest["verdict"] == "CLEAN"
    assert set(manifest["layers_completed"]) == {"exact", "containment", "minhash", "embedding"}
    assert manifest["layers_skipped"] == {}


def test_main_returns_2_when_es_unreachable(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    queries = write_queries(tmp_path / "queries.jsonl", ["any query"])

    def boom(*args, **kwargs):
        raise requests.ConnectionError("connection refused")

    monkeypatch.setattr(sc, "fetch_chunks", boom)
    monkeypatch.setattr(sc, "build_embedding_batch_fn", lambda *args, **kwargs: None)

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
    monkeypatch.setattr(sc, "build_embedding_batch_fn", fake_build)

    rc = sc.main(
        [
            "--es", "http://es.local:9201",
            "--index", "other_idx",
            "--queries", str(queries),
            "--embedding-url", "http://embed.local:8010/embeddings",
            "--out", str(tmp_path / "report.jsonl"),
            "--manifest", str(tmp_path / "manifest.json"),
            "--batch-size", "7",
            "--http-timeout", "33",
        ]
    )

    # fake_build 返回 None，第 4 层被跳过 -> 冻结策略要求降级为 INCOMPLETE(rc 3)，
    # 这里断言的是 CLI 透传，不是判决；rc 不能是 0，否则就是跳层换来的假 clean。
    assert rc == 3
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

    def fake_scan(chunks, benchmark_items, embedding_fn=None, **kwargs):
        captured["chunks"] = chunks
        captured["items"] = benchmark_items
        captured["embedding_fn"] = embedding_fn
        captured.update(kwargs)
        # 假报告也必须诚实登记覆盖面：verdict 正向要求四层齐全，
        # 空 ContaminationReport() 会判 INCOMPLETE，而本例意在验证 CLEAN 路径的透传。
        return ContaminationReport(
            layers_completed=["exact", "containment", "minhash", "embedding"]
        )

    def sentinel(texts: list[str]) -> list[list[float]]:
        return [[1.0] for _ in texts]

    monkeypatch.setattr(sc, "fetch_chunks", lambda *args, **kwargs: [("doc-1", "chunk text one")])
    monkeypatch.setattr(sc, "build_embedding_batch_fn", lambda *args, **kwargs: sentinel)
    monkeypatch.setattr(sc, "scan", fake_scan)

    rc = sc.main(
        [
            "--queries", str(queries),
            "--out", str(tmp_path / "report.jsonl"),
            "--manifest", str(tmp_path / "manifest.json"),
            "--threshold", "0.9",
            "--ngram", "5",
            "--minhash-bands", "16",
            # 本例考的是扫描参数透传，显式走重新编码路线以免牵扯向量形状。
            "--chunk-vector-field", "",
        ]
    )

    assert rc == 0
    assert captured["chunks"] == ["chunk text one"]
    assert captured["items"] == ["query text one"]
    # main 只有批量这一个 embedding 入口，单条入口不再参与判决路径。
    assert captured["embedding_batch_fn"] is sentinel
    assert captured["embedding_fn"] is None
    assert captured["ngram"] == 5
    assert captured["minhash_bands"] == 16
    assert captured["threshold"] == 0.9
    # 冻结策略的 degenerate_fragment.min_ngrams 必须由 driver 透传给 scanner，
    # 不能让 scanner 用自己的默认值绕过策略。
    assert captured["min_ngrams"] == 16


# ---------------------------------------------------------------------------
# Batch embedding: layer 4 on the real corpus is 24,877 + 180 texts. One HTTP
# round-trip per text cannot fit the frozen 600 s SLA, so the driver must build
# a batch callable and hand it to the scanner with the policy's batch_size (64).
# ---------------------------------------------------------------------------


def test_build_embedding_batch_fn_sends_one_request_per_batch(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    calls: list[dict] = []

    def fake_post(url, params=None, json=None, timeout=None):
        calls.append({"url": url, "json": json, "timeout": timeout})
        texts = json["input"]
        return FakeResp(
            200,
            {
                "model": "BAAI/bge-m3",
                "data": [
                    {"index": i, "embedding": [float(len(t)), 0.0]} for i, t in enumerate(texts)
                ],
            },
        )

    monkeypatch.setattr(sc.requests, "post", fake_post)

    fn = sc.build_embedding_batch_fn("http://127.0.0.1:8009/embeddings", timeout=45)
    assert fn is not None

    vectors = fn(["a", "bb", "ccc"])
    assert vectors == [[1.0, 0.0], [2.0, 0.0], [3.0, 0.0]]
    # 一次探测 ping + 一次批量调用，而不是每条文本一次。
    assert len(calls) == 2
    assert calls[1]["json"] == {"model": "BAAI/bge-m3", "input": ["a", "bb", "ccc"]}
    assert calls[1]["timeout"] == 45


def test_build_embedding_batch_fn_orders_vectors_by_index(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """OpenAI 契约允许 data[] 乱序，必须按 index 复位。

    错位等于把 A 的向量当成 B 的：第 4 层会给出与事实无关的相似度，
    这种污染判决比不跑更危险。
    """

    def fake_post(url, params=None, json=None, timeout=None):
        texts = json["input"]
        data = [{"index": i, "embedding": [float(i), 9.0]} for i, _ in enumerate(texts)]
        return FakeResp(200, {"data": list(reversed(data))})

    monkeypatch.setattr(sc.requests, "post", fake_post)
    fn = sc.build_embedding_batch_fn("http://127.0.0.1:8009/embeddings")
    assert fn is not None
    assert fn(["x", "y", "z"]) == [[0.0, 9.0], [1.0, 9.0], [2.0, 9.0]]


def test_build_embedding_batch_fn_raises_on_count_mismatch(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """少返回一条向量必须炸，不能静默补空。

    静默补 [] 会让该 chunk 在第 4 层被跳过，却仍然计入 layers_completed，
    于是 CLEAN 变成「没查到」而不是「查过且没命中」。
    """

    def fake_post(url, params=None, json=None, timeout=None):
        return FakeResp(200, {"data": [{"index": 0, "embedding": [1.0]}]})

    monkeypatch.setattr(sc.requests, "post", fake_post)
    fn = sc.build_embedding_batch_fn("http://127.0.0.1:8009/embeddings")
    assert fn is not None
    with pytest.raises(ValueError):
        fn(["one", "two", "three"])


def test_build_embedding_batch_fn_returns_none_when_unreachable(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    def fake_post(url, params=None, json=None, timeout=None):
        raise requests.ConnectionError("connection refused")

    monkeypatch.setattr(sc.requests, "post", fake_post)
    assert sc.build_embedding_batch_fn("http://127.0.0.1:9999/embeddings") is None


def test_main_threads_batch_fn_and_policy_batch_size_to_scan(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    queries = write_queries(tmp_path / "queries.jsonl", ["query text one"])
    captured: dict = {}

    def batch_sentinel(texts: list[str]) -> list[list[float]]:
        return [[1.0] for _ in texts]

    def fake_scan(chunks, benchmark_items, embedding_fn=None, **kwargs):
        captured.update(kwargs)
        captured["embedding_fn"] = embedding_fn
        return ContaminationReport(
            layers_completed=["exact", "containment", "minhash", "embedding"]
        )

    monkeypatch.setattr(sc, "fetch_chunks", lambda *args, **kwargs: [("doc-1", "chunk text one")])
    monkeypatch.setattr(sc, "build_embedding_batch_fn", lambda *args, **kwargs: batch_sentinel)
    monkeypatch.setattr(sc, "scan", fake_scan)

    rc = sc.main(
        [
            "--queries", str(queries),
            "--out", str(tmp_path / "report.jsonl"),
            "--manifest", str(tmp_path / "manifest.json"),
            # 本例考的是 batch_size 来自冻结策略，显式走重新编码路线。
            "--chunk-vector-field", "",
        ]
    )

    assert rc == 0
    assert captured["embedding_batch_fn"] is batch_sentinel
    # 冻结策略 layers[3].batch_size = 64，driver 不得用自己的默认值。
    assert captured["embedding_batch_size"] == 64


# ---------------------------------------------------------------------------
# 层 4 复用库内向量。
#
# 首次全量真实执行里，层 1-3 在 24,822 chunk x 180 query 上
# 107.6s 跑完，层 4 却在第一个 chunk 批次上把服务顶到单批 183.57s，超过 60s
# 客户端读超时——判决停在 INCOMPLETE。
#
# 但索引里已经存着这批 chunk 的 1024 维 cosine 向量（field `vector`，
# dense_vector），与冻结 revision 同源，且实测 cos(stored, fresh[embedding_text])
# = 1.000000。冻结策略自己的 ann_note 与 peak_rss_rationale 写的就是「持有
# 24877 x 1024 float32（约 102 MB）做一次 matmul」——复用库内向量是在执行策略，
# 重新编码才是偏离。
#
# 两个必须显式记账的事实，不能靠「跑通了」掩盖：
# 1. 库内向量算的是 `embedding_text`，层 1-3 哈希的是 `text_content` 优先级链。
#    两者不是同一个字符串，所以 manifest 要同时落 embedding_text_field 与
#    layer_text_field，让读报告的人自己判断这个语义差。
# 2. 缺向量、空向量、混合 model_version 一律抛错。静默丢掉那些 chunk 会让层 4
#    没覆盖到它们、却仍然计入 layers_completed——「没查」被写成「查过且干净」。
# ---------------------------------------------------------------------------


def vec_page(rows: list[tuple[str, dict]]) -> list[tuple[str, dict]]:
    """Passthrough helper kept for symmetry with write_queries/patch_es."""
    return rows


def test_fetch_chunks_returns_stored_vectors_when_vector_field_given(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    fake = FakeES(
        [
            [
                (
                    "c1",
                    {
                        "text_content": "alpha text",
                        "vector": [0.1, 0.2],
                        "model_version": "bge-m3@5617a9f6",
                    },
                ),
                (
                    "c2",
                    {
                        "text_content": "beta text",
                        "vector": [0.3, 0.4],
                        "model_version": "bge-m3@5617a9f6",
                    },
                ),
            ]
        ]
    )
    patch_es(monkeypatch, fake)

    chunks = sc.fetch_chunks(
        "http://es.local:9200", "idx", batch_size=2, vector_field="vector"
    )

    # 向量字段与 model_version 必须真的进 _source，否则 ES 不会把它们回传。
    requested = set(fake.calls[0]["json"]["_source"])
    assert {"text_content", "embedding_text", "vector", "model_version"} <= requested

    assert len(chunks) == 2
    assert [record.doc_id for record in chunks] == ["c1", "c2"]
    assert [record.text for record in chunks] == ["alpha text", "beta text"]
    assert [record.vector for record in chunks] == [[0.1, 0.2], [0.3, 0.4]]
    assert {record.model_version for record in chunks} == {"bge-m3@5617a9f6"}
    # 位置解包必须照旧可用：下游是按下标对齐 chunk 与向量的。
    doc_id, text, vector, model_version = chunks[0]
    assert (doc_id, text, vector, model_version) == (
        "c1",
        "alpha text",
        [0.1, 0.2],
        "bge-m3@5617a9f6",
    )


def test_fetch_chunks_without_vector_field_stays_two_tuples(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """向后兼容：不要向量时返回二元组，且不向 ES 多要 dense_vector 字段。

    24,877 个 1024 维向量是几百 MB 的回传量。只想跑层 1-3 的调用方不该为此付钱。
    """
    fake = FakeES([[("c1", {"text_content": "alpha text", "vector": [0.1, 0.2]})]])
    patch_es(monkeypatch, fake)

    chunks = sc.fetch_chunks("http://es.local:9200", "idx")

    assert chunks == [("c1", "alpha text")]
    assert set(fake.calls[0]["json"]["_source"]) == {"text_content", "embedding_text"}


def test_fetch_chunks_raises_when_stored_vector_missing(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """有文本却没有向量 → 抛错，绝不静默丢弃。

    丢掉这个 chunk，层 4 就没覆盖它，可 layers_completed 照旧写着 embedding：
    判决从「查过且干净」退化成「没查」，而报告读起来一模一样。
    """
    fake = FakeES(
        [
            [
                ("c1", {"text_content": "alpha", "vector": [0.1], "model_version": "m"}),
                ("c2", {"text_content": "beta", "model_version": "m"}),
            ]
        ]
    )
    patch_es(monkeypatch, fake)

    with pytest.raises(ValueError) as excinfo:
        sc.fetch_chunks("http://es.local:9200", "idx", vector_field="vector")
    assert "c2" in str(excinfo.value)


def test_fetch_chunks_raises_when_stored_vector_empty(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    # 空列表和缺字段等价：都表示这个 chunk 没有可比对的向量。
    fake = FakeES(
        [[("c9", {"text_content": "alpha", "vector": [], "model_version": "m"})]]
    )
    patch_es(monkeypatch, fake)

    with pytest.raises(ValueError):
        sc.fetch_chunks("http://es.local:9200", "idx", vector_field="vector")


def test_fetch_chunks_raises_on_mixed_model_version(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """混合 model_version → 抛错：跨模型的余弦值没有可比性。

    两个模型的向量空间不同，把它们放进同一个阈值判决里，得到的相似度既不能
    证明污染也不能证明干净。
    """
    fake = FakeES(
        [
            [
                ("c1", {"text_content": "alpha", "vector": [0.1], "model_version": "bge-m3@aaa"}),
                ("c2", {"text_content": "beta", "vector": [0.2], "model_version": "bge-m3@bbb"}),
            ]
        ]
    )
    patch_es(monkeypatch, fake)

    with pytest.raises(ValueError) as excinfo:
        sc.fetch_chunks("http://es.local:9200", "idx", vector_field="vector")
    message = str(excinfo.value)
    assert "bge-m3@aaa" in message and "bge-m3@bbb" in message


CHUNK_TEXT = "Goroutines are lightweight concurrent threads of execution in Go."
QUERY_TEXT = "How do I parse a TOML configuration file in Python?"


def run_main_with_stored_vectors(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    *,
    extra_argv: list[str] | None = None,
) -> tuple[int, dict, list[str]]:
    """Run main with a one-chunk corpus whose vector already lives in the index.

    Returns ``(exit_code, manifest, texts_the_embedding_service_saw)``. The chunk
    and query vectors are orthogonal, so a CLEAN verdict cannot be luck.
    """
    queries = write_queries(tmp_path / "queries.jsonl", [QUERY_TEXT])
    manifest_path = tmp_path / "manifest.json"
    seen_texts: list[str] = []

    monkeypatch.setattr(
        sc,
        "fetch_chunks",
        lambda *args, **kwargs: [
            sc.ChunkRecord("doc-1", CHUNK_TEXT, [0.0, 1.0], "bge-m3@5617a9f6")
        ],
    )

    def embed_batch(texts: list[str]) -> list[list[float]]:
        seen_texts.extend(texts)
        return [[1.0, 0.0] for _ in texts]

    monkeypatch.setattr(sc, "build_embedding_batch_fn", lambda *a, **k: embed_batch)

    argv = [
        "--queries", str(queries),
        "--out", str(tmp_path / "report.jsonl"),
        "--manifest", str(manifest_path),
    ]
    rc = sc.main(argv + (extra_argv or []))
    manifest = json.loads(manifest_path.read_text(encoding="utf-8")) if manifest_path.exists() else {}
    return rc, manifest, seen_texts


def test_main_uses_stored_vectors_without_embedding_any_chunk(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """层 4 跑完 + chunk 侧零次 embedding 请求。

    断言的是「服务看到了哪些文本」而不是「调用了几次」：只有前者能证明 chunk
    根本没上过网线，这正是 §29 那次 183.57s 单批超时的成因。
    """
    rc, manifest, seen_texts = run_main_with_stored_vectors(tmp_path, monkeypatch)

    assert rc == 0, f"expected CLEAN, manifest={manifest}"
    assert set(manifest["layers_completed"]) == {
        "exact",
        "containment",
        "minhash",
        "embedding",
    }
    assert manifest["layers_skipped"] == {}
    # 查询侧仍然要过服务（没有它就没有可比对的向量），chunk 侧一条都不许上网。
    assert seen_texts == [QUERY_TEXT]
    assert CHUNK_TEXT not in seen_texts


def test_main_manifest_records_vector_provenance(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """manifest 必须说清层 4 的向量从哪来、算的是哪个字段、哪个模型。

    尤其是 embedding_text_field 与 layer_text_field 的差别：库内向量算的是
    embedding_text，层 1-3 哈希的是 text_content 优先级链。不写下来，日后没人
    能判断这个语义差有没有影响判决。
    """
    rc, manifest, _ = run_main_with_stored_vectors(tmp_path, monkeypatch)

    assert rc == 0
    assert manifest["embedding_vector_source"] == "es_stored_vectors"
    assert manifest["embedding_vector_field"] == "vector"
    assert manifest["embedding_model_version"] == "bge-m3@5617a9f6"
    assert manifest["embedding_text_field"] == "embedding_text"
    assert "text_content" in manifest["layer_text_field"]


def test_main_chunk_vector_field_can_be_disabled(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """--chunk-vector-field '' 回到重新编码路线，且 manifest 如实标注来源。

    保留这条路线是为了能在新索引（还没写入向量）上跑，而不是给复用向量留一个
    静默回退口子——回退必须是显式要求的。
    """
    queries = write_queries(tmp_path / "queries.jsonl", [QUERY_TEXT])
    captured: dict = {}

    def fake_scan(chunks, benchmark_items, embedding_fn=None, **kwargs):
        captured.update(kwargs)
        return ContaminationReport(
            layers_completed=["exact", "containment", "minhash", "embedding"]
        )

    monkeypatch.setattr(sc, "fetch_chunks", lambda *a, **k: [("doc-1", CHUNK_TEXT)])
    monkeypatch.setattr(
        sc, "build_embedding_batch_fn", lambda *a, **k: (lambda texts: [[1.0] for _ in texts])
    )
    monkeypatch.setattr(sc, "scan", fake_scan)

    manifest_path = tmp_path / "manifest.json"
    rc = sc.main(
        [
            "--queries", str(queries),
            "--out", str(tmp_path / "report.jsonl"),
            "--manifest", str(manifest_path),
            "--chunk-vector-field", "",
        ]
    )

    assert rc == 0
    assert captured["chunk_vectors"] is None
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    assert manifest["embedding_vector_source"] == "embedding_service"
    assert manifest["embedding_vector_field"] == ""


def test_main_maps_vector_integrity_error_to_exit_2(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """向量完整性错误 → rc 2，不是未捕获异常。

    未捕获异常让解释器退 1，而 1 在冻结策略里是 BLOCKING = 「发现污染」。
    一个取数据的故障绝不能伪装成污染判决。
    """
    queries = write_queries(tmp_path / "queries.jsonl", [QUERY_TEXT])

    def boom(*args, **kwargs):
        raise ValueError("chunk c2 has no stored vector in field 'vector'")

    monkeypatch.setattr(sc, "fetch_chunks", boom)
    monkeypatch.setattr(sc, "build_embedding_batch_fn", lambda *a, **k: None)

    rc = sc.main(
        [
            "--queries", str(queries),
            "--out", str(tmp_path / "report.jsonl"),
            "--manifest", str(tmp_path / "manifest.json"),
        ]
    )
    assert rc == 2


def test_main_defaults_to_the_stored_vector_field(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """默认就复用库内向量：默认路线必须是能在 SLA 内跑完的那条。"""
    queries = write_queries(tmp_path / "queries.jsonl", [QUERY_TEXT])
    seen: dict = {}

    def fake_fetch(es_url, index, *, batch_size, http_timeout, vector_field=None, **kwargs):
        seen["vector_field"] = vector_field
        return [sc.ChunkRecord("doc-1", CHUNK_TEXT, [0.0, 1.0], "m")]

    monkeypatch.setattr(sc, "fetch_chunks", fake_fetch)
    monkeypatch.setattr(
        sc, "build_embedding_batch_fn", lambda *a, **k: (lambda texts: [[1.0, 0.0] for _ in texts])
    )

    rc = sc.main(
        [
            "--queries", str(queries),
            "--out", str(tmp_path / "report.jsonl"),
            "--manifest", str(tmp_path / "manifest.json"),
        ]
    )

    assert rc == 0
    assert seen["vector_field"] == "vector"


# ---------------------------------------------------------------------------
# CLI invocability. The exit-code table in this script's own docstring is a
# contract for CI wrappers, which run it as `python scripts/corpus/
# scan_contamination.py`, not through pytest. Under pytest the repo root is on
# sys.path via pyproject's `pythonpath = ["."]`; a bare shell invocation gets
# sys.path[0] = scripts/corpus/ instead, so `import eval` fails and the gate
# dies at import with no exit code from the table at all.
# ---------------------------------------------------------------------------


def test_script_is_invocable_from_the_shell(tmp_path: Path) -> None:
    """--help must work as a plain subprocess, run from outside the repo root."""
    import subprocess

    script = REPO_ROOT / "scripts" / "corpus" / "scan_contamination.py"
    proc = subprocess.run(
        [sys.executable, str(script), "--help"],
        # cwd 故意设在仓库外：入口不能依赖「刚好在仓库根目录启动」。
        cwd=str(tmp_path),
        capture_output=True,
        text=True,
        timeout=120,
    )
    assert "ModuleNotFoundError" not in proc.stderr, proc.stderr
    assert proc.returncode == 0, f"stdout={proc.stdout}\nstderr={proc.stderr}"
    assert "--manifest" in proc.stdout


def test_script_reports_policy_exit_2_from_the_shell(tmp_path: Path) -> None:
    """A missing policy must surface as exit 2, not as an import traceback."""
    import subprocess

    script = REPO_ROOT / "scripts" / "corpus" / "scan_contamination.py"
    proc = subprocess.run(
        [
            sys.executable, str(script),
            "--policy", str(tmp_path / "no-such-policy.json"),
            "--queries", str(write_queries(tmp_path / "q.jsonl", ["any query"])),
            "--out", str(tmp_path / "report.jsonl"),
            "--manifest", str(tmp_path / "manifest.json"),
        ],
        cwd=str(tmp_path),
        capture_output=True,
        text=True,
        timeout=120,
    )
    assert "ModuleNotFoundError" not in proc.stderr, proc.stderr
    assert proc.returncode == 2, f"stdout={proc.stdout}\nstderr={proc.stderr}"
    assert "POLICY_MISSING" in proc.stderr
