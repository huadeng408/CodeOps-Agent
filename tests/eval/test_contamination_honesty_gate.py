"""D3 regression: a scan that did not finish must never report itself clean.

Frozen contract: data/eval/contamination/contamination-policy.v1.json
  skipped_layer_rule.rule
    "If any of the four layers did not complete, the scan verdict is INCOMPLETE
     and the run MUST NOT emit clean."
  skipped_layer_rule.forbidden_output_when_incomplete  ["clean", "exit code 0"]
  skipped_layer_rule.clean_requires
    "all four layers completed AND zero exact matches AND zero threshold hits"
  exit_codes."3"  "INCOMPLETE - at least one layer was skipped or did not finish"

Measured violation being fixed (policy skipped_layer_rule.violation_evidence):
with the embedding service at http=000, the pre-v1 driver printed
"clean: no unreviewed contamination found" and returned 0 while layer_notes
recorded "skipped: embedding_fn not provided". That behaviour was locked in by
a *passing* test, tests/corpus/test_scan_contamination.py::test_main_clean_exit_0,
which asserted rc == 0 and "clean" in stdout. Deleting the assertion is not
enough; the opposite has to be asserted, which is what this file does.

All tests are offline: no ES, no embedding service, no production scan.
"""

from __future__ import annotations

import hashlib
import json
from pathlib import Path

import pytest

from eval.contamination.scanner import ContaminationReport, scan
from scripts.corpus import scan_contamination as sc

REPO_ROOT = Path(__file__).resolve().parents[2]
POLICY_PATH = REPO_ROOT / "data" / "eval" / "contamination" / "contamination-policy.v1.json"
SIDECAR_PATH = POLICY_PATH.with_suffix(".json.sha256")

# Distinct on purpose: no exact match, no shared 8-grams, no near-duplicate.
CLEAN_QUERY = "How do I parse a TOML configuration file in Python?"
CLEAN_CHUNK = "Goroutines are lightweight concurrent threads of execution in Go."


def policy() -> dict:
    return json.loads(POLICY_PATH.read_text(encoding="utf-8"))


class OneHotEmbedder:
    """Deterministic, collision-free stand-in for the bge-m3 service.

    Distinct texts get distinct basis vectors, so cosine is exactly 0.0 between
    them and exactly 1.0 for a repeat. No randomness, so a CLEAN verdict in
    these tests can never be luck.
    """

    def __init__(self, dims: int = 32) -> None:
        self._seen: dict[str, int] = {}
        self._dims = dims
        self.calls = 0

    def __call__(self, text: str) -> list[float]:
        self.calls += 1
        idx = self._seen.setdefault(text, len(self._seen))
        vec = [0.0] * self._dims
        vec[idx % self._dims] = 1.0
        return vec

    def batch(self, texts: list[str]) -> list[list[float]]:
        """Batch adapter: main() only has one embedding entry point (batch).

        Routes through __call__ so ``calls`` still counts real work and an
        assertion like ``embedder.calls > 0`` keeps its meaning.
        """
        return [self(text) for text in texts]


def stored_chunk(text: str, *, doc_id: str = "doc-1", basis: int = 1, dims: int = 32):
    """A chunk whose vector already lives in the index — the shipping default route.

    层 4 默认复用库内向量（--chunk-vector-field vector），所以诚实性门禁必须在
    这条真正会发货的路线上验证，而不是靠 --chunk-vector-field '' 退回旧路线来
    回避形状变化。这样断言的语义反而更强：即使 chunk 向量已经在手，只要查询侧
    没有 embedder，层 4 依然必须记为跳过、判 INCOMPLETE。

    basis 默认取 1，而 OneHotEmbedder 把首个见到的文本（查询）分到 basis 0，
    两者正交 → 余弦恒为 0，CLEAN 不可能是运气。
    """
    vector = [0.0] * dims
    vector[basis % dims] = 1.0
    return sc.ChunkRecord(doc_id, text, vector, "bge-m3@test")


@pytest.fixture(autouse=True)
def _forbid_unpatched_embedding_network(monkeypatch: pytest.MonkeyPatch) -> None:
    """Fail loudly instead of silently reaching a live embedding service.

    这些用例全部离线。之前它们只 patch 了 build_embedding_fn，而 main 改走
    build_embedding_batch_fn 之后，未被拦截的调用会静默打到本机真实的
    :8009 上：第 4 层真的跑了，判决从 INCOMPLETE 变成 CLEAN，测试却看起来
    只是「断言写错了」。把裸网络调用变成显式失败，这类静默串台就不会再发生。
    用例自己的 monkeypatch 在本 fixture 之后执行，因此仍可正常覆盖。
    """

    def refuse(*args, **kwargs):
        raise AssertionError(
            "unpatched network call in an offline test: patch "
            "sc.build_embedding_batch_fn (main's only embedding entry point)"
        )

    monkeypatch.setattr(sc.requests, "post", refuse)


def write_queries(path: Path, queries: list[str]) -> Path:
    path.write_text(
        "\n".join(
            json.dumps({"query_id": f"q{i}", "query": q}) for i, q in enumerate(queries)
        )
        + "\n",
        encoding="utf-8",
    )
    return path


# --------------------------------------------------------------------------- #
# Scanner level: the report has to be able to say "I did not finish"
# --------------------------------------------------------------------------- #


def test_report_records_which_layer_was_skipped() -> None:
    report = scan([CLEAN_CHUNK], [CLEAN_QUERY])
    assert "embedding" in report.skipped_layers


def test_skipped_layer_carries_a_reason() -> None:
    """required_output_when_incomplete: "the reason each was skipped"."""
    report = scan([CLEAN_CHUNK], [CLEAN_QUERY])
    reason = report.skipped_layers["embedding"]
    assert isinstance(reason, str)
    assert reason.strip(), "a skipped layer with a blank reason is not auditable"


def test_report_is_incomplete_when_a_layer_is_skipped() -> None:
    report = scan([CLEAN_CHUNK], [CLEAN_QUERY])
    assert report.incomplete is True
    assert report.verdict == "INCOMPLETE"


def test_incomplete_report_is_not_clean_even_with_zero_hits() -> None:
    """The precise defect: zero hits was being read as proof of cleanliness."""
    report = scan([CLEAN_CHUNK], [CLEAN_QUERY])
    assert report.exact_matches == []
    assert report.high_similarity == []
    assert report.blocking is False
    # Zero hits from three layers is not clean when the fourth never ran.
    assert report.verdict != "CLEAN"


def test_verdict_is_clean_only_when_all_four_layers_complete() -> None:
    report = scan([CLEAN_CHUNK], [CLEAN_QUERY], embedding_fn=OneHotEmbedder())
    assert report.skipped_layers == {}
    assert report.incomplete is False
    assert report.verdict == "CLEAN"


def test_layers_completed_names_the_layers_that_actually_ran() -> None:
    partial = scan([CLEAN_CHUNK], [CLEAN_QUERY])
    assert set(partial.layers_completed) == {"exact", "containment", "minhash"}

    full = scan([CLEAN_CHUNK], [CLEAN_QUERY], embedding_fn=OneHotEmbedder())
    assert set(full.layers_completed) == {"exact", "containment", "minhash", "embedding"}


def test_report_with_no_layers_recorded_is_not_clean() -> None:
    """Fail-closed: absent skip records must not read as full coverage.

    verdict 早期实现只问「有没有登记跳过」，于是一个从未跑过任何层、
    skipped_layers 也是空的报告会被判成 CLEAN。任何忘记登记 skip 的
    代码路径都能靠这个漏洞拿到 exit 0，所以覆盖面必须正向断言。
    """
    empty = ContaminationReport()
    assert empty.exact_matches == []
    assert empty.skipped_layers == {}
    assert empty.layers_completed == []
    assert empty.verdict == "INCOMPLETE"

    partial = ContaminationReport(layers_completed=["exact", "containment", "minhash"])
    assert partial.verdict == "INCOMPLETE"

    full = ContaminationReport(
        layers_completed=["exact", "containment", "minhash", "embedding"]
    )
    assert full.verdict == "CLEAN"


def test_blocking_outranks_incomplete() -> None:
    """A real hit is more actionable than missing coverage, so BLOCKING wins."""
    report = scan([CLEAN_QUERY], [CLEAN_QUERY])  # exact match, embedding skipped
    assert report.exact_matches
    assert report.incomplete is True
    assert report.verdict == "BLOCKING"


def test_verdict_stays_inside_the_policy_vocabulary() -> None:
    vocabulary = set(policy()["manifest"]["verdict_vocabulary"])
    for report in (
        scan([CLEAN_CHUNK], [CLEAN_QUERY]),
        scan([CLEAN_CHUNK], [CLEAN_QUERY], embedding_fn=OneHotEmbedder()),
        scan([CLEAN_QUERY], [CLEAN_QUERY]),
    ):
        assert report.verdict in vocabulary


# --------------------------------------------------------------------------- #
# Driver level: the exit code and the words on stdout
# --------------------------------------------------------------------------- #


def test_main_refuses_clean_when_embedding_layer_skipped(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    """Replaces test_main_clean_exit_0, which asserted the bug.

    Same inputs as that test: zero hits, embedding service unavailable. The old
    contract was rc == 0 plus "clean"; the frozen contract is rc == 3 and no
    "clean" anywhere in the output.
    """
    queries = write_queries(tmp_path / "queries.jsonl", [CLEAN_QUERY])
    out = tmp_path / "report.jsonl"
    manifest = tmp_path / "manifest.json"
    monkeypatch.setattr(sc, "fetch_chunks", lambda *a, **k: [stored_chunk(CLEAN_CHUNK)])
    monkeypatch.setattr(sc, "build_embedding_batch_fn", lambda *a, **k: None)

    rc = sc.main(
        [
            "--queries", str(queries),
            "--out", str(out),
            "--manifest", str(manifest),
        ]
    )

    stdout = capsys.readouterr().out
    # pytest 用测试函数名派生 tmp_path，而本函数名里就含 "clean"，
    # 因此断言驱动措辞前必须先剔除回显的路径，否则测的是路径不是措辞。
    prose = stdout.replace(str(manifest), "<manifest>").replace(
        str(manifest).lower(), "<manifest>"
    )
    assert rc == 3, "a scan with a skipped layer must not exit 0"
    assert "clean" not in prose.lower()
    assert "INCOMPLETE" in prose


def test_main_incomplete_output_names_the_skipped_layer_and_reason(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    queries = write_queries(tmp_path / "queries.jsonl", [CLEAN_QUERY])
    monkeypatch.setattr(sc, "fetch_chunks", lambda *a, **k: [stored_chunk(CLEAN_CHUNK)])
    monkeypatch.setattr(sc, "build_embedding_batch_fn", lambda *a, **k: None)

    sc.main(
        [
            "--queries", str(queries),
            "--out", str(tmp_path / "report.jsonl"),
            "--manifest", str(tmp_path / "manifest.json"),
        ]
    )

    stdout = capsys.readouterr().out
    assert "embedding" in stdout, "the operator cannot act on an unnamed skipped layer"


def test_main_exits_0_only_when_all_four_layers_complete(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    queries = write_queries(tmp_path / "queries.jsonl", [CLEAN_QUERY])
    out = tmp_path / "report.jsonl"
    embedder = OneHotEmbedder()
    monkeypatch.setattr(sc, "fetch_chunks", lambda *a, **k: [stored_chunk(CLEAN_CHUNK)])
    monkeypatch.setattr(sc, "build_embedding_batch_fn", lambda *a, **k: embedder.batch)

    rc = sc.main(
        [
            "--queries", str(queries),
            "--out", str(out),
            "--manifest", str(tmp_path / "manifest.json"),
        ]
    )

    assert rc == 0
    assert out.read_text(encoding="utf-8") == ""
    assert "clean" in capsys.readouterr().out.lower()
    assert embedder.calls > 0, "exit 0 while the embedding layer never ran is the defect"


def test_main_blocking_outranks_incomplete(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    queries = write_queries(tmp_path / "queries.jsonl", [CLEAN_QUERY])
    monkeypatch.setattr(sc, "fetch_chunks", lambda *a, **k: [stored_chunk(CLEAN_QUERY)])
    monkeypatch.setattr(sc, "build_embedding_batch_fn", lambda *a, **k: None)

    rc = sc.main(
        [
            "--queries", str(queries),
            "--out", str(tmp_path / "report.jsonl"),
            "--manifest", str(tmp_path / "manifest.json"),
        ]
    )
    assert rc == 1


# --------------------------------------------------------------------------- #
# The manifest: on-disk proof of what ran, per policy manifest.required_fields
# --------------------------------------------------------------------------- #


def run_incomplete(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> tuple[int, dict]:
    queries = write_queries(tmp_path / "queries.jsonl", [CLEAN_QUERY])
    manifest_path = tmp_path / "manifest.json"
    monkeypatch.setattr(sc, "fetch_chunks", lambda *a, **k: [stored_chunk(CLEAN_CHUNK)])
    monkeypatch.setattr(sc, "build_embedding_batch_fn", lambda *a, **k: None)
    rc = sc.main(
        [
            "--queries", str(queries),
            "--out", str(tmp_path / "report.jsonl"),
            "--manifest", str(manifest_path),
            "--index", "knowledge_base_v2_bge_m3",
        ]
    )
    return rc, json.loads(manifest_path.read_text(encoding="utf-8"))


def test_manifest_is_written_even_when_the_scan_is_incomplete(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    rc, manifest = run_incomplete(tmp_path, monkeypatch)
    assert rc == 3
    assert manifest["verdict"] == "INCOMPLETE"


def test_manifest_carries_every_policy_required_field(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    _, manifest = run_incomplete(tmp_path, monkeypatch)
    missing = [f for f in policy()["manifest"]["required_fields"] if f not in manifest]
    assert missing == [], f"MANIFEST_MISSING_FIELD: {missing}"


def test_manifest_pins_the_policy_it_was_run_under(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """MANIFEST_STALE guard: a result is only interpretable against its policy."""
    _, manifest = run_incomplete(tmp_path, monkeypatch)
    expected = hashlib.sha256(POLICY_PATH.read_bytes()).hexdigest()
    assert manifest["policy_sha256"] == expected
    assert manifest["policy_id"] == "e4-contamination-policy"
    assert manifest["policy_version"] == "v1"


def test_manifest_records_the_skipped_layer_and_the_counts(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    _, manifest = run_incomplete(tmp_path, monkeypatch)
    assert "embedding" in manifest["layers_skipped"]
    assert sorted(manifest["layers_completed"]) == ["containment", "exact", "minhash"]
    assert manifest["chunks_scanned"] == 1
    assert manifest["queries_scanned"] == 1
    assert manifest["es_index"] == "knowledge_base_v2_bge_m3"
    for field in ("exact_hits", "containment_hits", "minhash_hits", "embedding_hits"):
        assert manifest[field] == 0
    assert isinstance(manifest["wall_clock_seconds"], (int, float))
    assert isinstance(manifest["degenerate_fragments"], int)


def test_manifest_verdict_clean_only_with_all_layers(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    queries = write_queries(tmp_path / "queries.jsonl", [CLEAN_QUERY])
    manifest_path = tmp_path / "manifest.json"
    monkeypatch.setattr(sc, "fetch_chunks", lambda *a, **k: [stored_chunk(CLEAN_CHUNK)])
    monkeypatch.setattr(sc, "build_embedding_batch_fn", lambda *a, **k: OneHotEmbedder().batch)

    rc = sc.main(
        [
            "--queries", str(queries),
            "--out", str(tmp_path / "report.jsonl"),
            "--manifest", str(manifest_path),
        ]
    )
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    assert rc == 0
    assert manifest["verdict"] == "CLEAN"
    assert manifest["layers_skipped"] in ({}, [])


# --------------------------------------------------------------------------- #
# Policy binding: POLICY_MISSING / POLICY_HASH_MISMATCH
# --------------------------------------------------------------------------- #


def test_main_refuses_to_run_without_its_policy(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    """POLICY_MISSING: an unpinned scan produces an uninterpretable result."""
    queries = write_queries(tmp_path / "queries.jsonl", [CLEAN_QUERY])
    monkeypatch.setattr(sc, "fetch_chunks", lambda *a, **k: [stored_chunk(CLEAN_CHUNK)])
    monkeypatch.setattr(sc, "build_embedding_batch_fn", lambda *a, **k: OneHotEmbedder().batch)

    rc = sc.main(
        [
            "--queries", str(queries),
            "--out", str(tmp_path / "report.jsonl"),
            "--manifest", str(tmp_path / "manifest.json"),
            "--policy", str(tmp_path / "no-such-policy.json"),
        ]
    )
    assert rc == 2
    assert "clean" not in capsys.readouterr().out.lower()


def test_main_refuses_a_policy_that_does_not_match_its_sidecar(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    """POLICY_HASH_MISMATCH: an edited policy invalidates the frozen SLA."""
    tampered = tmp_path / "contamination-policy.v1.json"
    doc = policy()
    doc["sla"]["max_wall_clock_seconds"] = 999999
    tampered.write_text(json.dumps(doc, indent=2), encoding="utf-8")
    # Sidecar still carries the original digest, so the bytes no longer match.
    tampered.with_suffix(".json.sha256").write_bytes(SIDECAR_PATH.read_bytes())

    queries = write_queries(tmp_path / "queries.jsonl", [CLEAN_QUERY])
    monkeypatch.setattr(sc, "fetch_chunks", lambda *a, **k: [stored_chunk(CLEAN_CHUNK)])
    monkeypatch.setattr(sc, "build_embedding_batch_fn", lambda *a, **k: OneHotEmbedder().batch)

    rc = sc.main(
        [
            "--queries", str(queries),
            "--out", str(tmp_path / "report.jsonl"),
            "--manifest", str(tmp_path / "manifest.json"),
            "--policy", str(tampered),
        ]
    )
    assert rc == 2
    assert "clean" not in capsys.readouterr().out.lower()


# --------------------------------------------------------------------------- #
# Mid-scan embedding failure. Policy failure_semantics
# EMBEDDING_SERVICE_UNREACHABLE is behaviour "non_clean": "the scan continues
# and reports layers 1 to 3, but the verdict is INCOMPLETE, never CLEAN".
# A traceback instead would exit 1, which the exit-code table reserves for
# BLOCKING - a crash would be read as "contamination found".
# --------------------------------------------------------------------------- #


def test_embedding_failure_midscan_degrades_to_skip_not_crash() -> None:
    """服务在扫描途中挂掉：层 1-3 结果必须保留，层 4 记为跳过。"""

    def dies_on_real_text(text: str) -> list[float]:
        if text == "ping":
            return [1.0, 0.0]
        raise RuntimeError("embedding service died mid-scan")

    report = scan([CLEAN_CHUNK], [CLEAN_QUERY], embedding_fn=dies_on_real_text)

    assert "embedding" in report.skipped_layers
    assert report.verdict == "INCOMPLETE"
    # 前三层照常完成并可被引用，而不是整场扫描一起丢掉。
    assert set(report.layers_completed) == {"exact", "containment", "minhash"}
    assert report.chunks_scanned == 1
    assert report.queries_scanned == 1


def test_embedding_failure_reason_names_the_failure() -> None:
    def boom(text: str) -> list[float]:
        raise RuntimeError("connection reset by peer")

    report = scan([CLEAN_CHUNK], [CLEAN_QUERY], embedding_fn=boom)
    reason = report.skipped_layers["embedding"]
    assert "connection reset by peer" in reason


def test_embedding_failure_still_reports_blocking_from_lower_layers() -> None:
    """层 4 挂掉不能掩盖层 1 已经抓到的精确匹配。"""

    def boom(text: str) -> list[float]:
        raise RuntimeError("service gone")

    report = scan([CLEAN_QUERY], [CLEAN_QUERY], embedding_fn=boom)
    assert report.exact_matches
    assert report.verdict == "BLOCKING"


def test_main_embedding_layer_has_a_single_network_entry_point(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """回归：main 曾有两个独立 embedding 入口，只 patch 一个的测试会打到真实服务。

    这条断言不看判决，只看隔离：把 requests.post 换成炸弹后，main 必须仍然
    走完并判 INCOMPLETE——也就是说所有 embedding 网络访问都经由可被单点
    拦截的 build_embedding_batch_fn，没有第二条绕过它的路径。
    """
    queries = write_queries(tmp_path / "queries.jsonl", [CLEAN_QUERY])
    manifest_path = tmp_path / "manifest.json"
    monkeypatch.setattr(sc, "fetch_chunks", lambda *a, **k: [stored_chunk(CLEAN_CHUNK)])

    def no_network(*args, **kwargs):
        raise AssertionError("embedding layer reached the network outside the patched entry point")

    monkeypatch.setattr(sc, "build_embedding_batch_fn", lambda *a, **k: None)
    monkeypatch.setattr(sc.requests, "post", no_network)

    rc = sc.main(
        [
            "--queries", str(queries),
            "--out", str(tmp_path / "report.jsonl"),
            "--manifest", str(manifest_path),
        ]
    )

    assert rc == 3
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    assert manifest["verdict"] == "INCOMPLETE"
    assert "embedding" in manifest["layers_skipped"]
