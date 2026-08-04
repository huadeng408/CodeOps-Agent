"""Offline contract tests for the corpus consistency audit.

The audit cross-checks the three stores a corpus import touches:
  ES  knowledge_base_v2_bge_m3  (chunk field ``document_id``)
  MySQL knowledge_document       (status / document_id / file_md5 per generation)
  MySQL document_vectors         (file_md5 coverage)

All tests are offline. ``fetch_es_document_ids`` / ``fetch_mysql_state`` are
monkeypatched at the module level for the ``main`` scenarios, ES scroll I/O is
faked through ``requests.post`` / ``requests.delete``, and the MySQL layer is
exercised through its pure pieces (``parse_go_dsn``, ``query_vector_md5s``,
``build_report``) — no real ES, MySQL, or pymysql is ever touched.
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest
import requests

from scripts.corpus import consistency_audit as audit

TEST_DSN = (
    "codeagent:codeagent@tcp(127.0.0.1:3306)/codeagent"
    "?charset=utf8mb4&parseTime=True&loc=Local"
)
GENERATION = "techdocs-2026-07-30-v1"
INDEX = "knowledge_base_v2_bge_m3"


# --------------------------------------------------------------------------- #
# Helpers
# --------------------------------------------------------------------------- #


def make_state(
    *,
    active: dict[str, str] | None = None,  # document_id -> file_md5
    other: dict[str, str] | None = None,  # document_id -> non-ACTIVE status
    vector_md5s: set[str] | None = None,
) -> dict:
    """Build the fetch_mysql_state result shape used by build_report/main."""
    active = dict(active or {})
    other = dict(other or {})
    counts = {"ACTIVE": len(active), "FAILED": 0, "SKIPPED": 0, "STAGED": 0}
    for status in other.values():
        if status in counts:
            counts[status] += 1
    statuses = {doc_id: "ACTIVE" for doc_id in active}
    statuses.update(other)
    return {
        "counts": counts,
        "statuses": statuses,
        "active_ids": set(active),
        "active_md5s": active,
        "vector_md5s": set(vector_md5s or set()),
    }


def patch_fetchers(
    monkeypatch: pytest.MonkeyPatch, es_ids: set[str], state: dict
) -> dict:
    calls: dict[str, dict] = {}

    def fake_es(es_url: str, index: str, **kwargs: object) -> set[str]:
        calls["es"] = {"es_url": es_url, "index": index}
        return set(es_ids)

    def fake_mysql(dsn: str, generation: str, **kwargs: object) -> dict:
        calls["mysql"] = {"dsn": dsn, "generation": generation}
        return state

    monkeypatch.setattr(audit, "fetch_es_document_ids", fake_es)
    monkeypatch.setattr(audit, "fetch_mysql_state", fake_mysql)
    return calls


class FakeEsResp:
    def __init__(self, payload: dict, status_code: int = 200) -> None:
        self._payload = payload
        self.status_code = status_code

    def json(self) -> dict:
        return self._payload

    def raise_for_status(self) -> None:
        if self.status_code >= 400:
            raise requests.HTTPError(f"status {self.status_code}")


class FakeES:
    """In-process stand-in for the ES scroll API.

    The first POST (<index>/_search?scroll=...) consumes pages[0]; every
    following POST to _search/scroll consumes the next page until the list is
    exhausted, then returns empty hits so the scroll loop terminates.
    """

    def __init__(self, pages: list[list[str]], status_code: int = 200) -> None:
        self.pages = list(pages)
        self.status_code = status_code
        self.post_calls: list[dict] = []
        self.delete_calls: list[dict] = []

    def _next_page(self) -> dict:
        docs = self.pages.pop(0) if self.pages else []
        return {
            "_scroll_id": "scroll-ctx-1",
            "hits": {"hits": [{"_source": {"document_id": d}} for d in docs]},
        }

    def post(self, url: str, json=None, timeout=None, **kwargs: object):
        self.post_calls.append({"url": url, "json": json, "timeout": timeout})
        if self.status_code >= 400:
            return FakeEsResp({}, self.status_code)
        return FakeEsResp(self._next_page())

    def delete(self, url: str, json=None, timeout=None, **kwargs: object):
        self.delete_calls.append({"url": url, "json": json})
        return FakeEsResp({})


# --------------------------------------------------------------------------- #
# parse_go_dsn
# --------------------------------------------------------------------------- #


def test_parse_go_dsn_extracts_fields_and_drops_go_only_params() -> None:
    cfg = audit.parse_go_dsn(TEST_DSN)
    assert cfg["user"] == "codeagent"
    assert cfg["password"] == "codeagent"
    assert cfg["host"] == "127.0.0.1"
    assert cfg["port"] == 3306
    assert cfg["database"] == "codeagent"
    # parseTime / loc are Go driver specifics and must be dropped; charset is
    # meaningful to pymysql and is kept.
    assert cfg["params"] == {"charset": "utf8mb4"}


def test_parse_go_dsn_handles_password_with_at_and_colon_and_default_port() -> None:
    cfg = audit.parse_go_dsn("app:p@ss:w0rd@tcp(db.internal)/appdb")
    assert cfg["user"] == "app"
    assert cfg["password"] == "p@ss:w0rd"
    assert cfg["host"] == "db.internal"
    assert cfg["port"] == 3306  # omitted port defaults to MySQL default
    assert cfg["database"] == "appdb"
    assert cfg["params"] == {}


@pytest.mark.parametrize(
    "bad",
    [
        "",
        "root:root@localhost/db",  # no @tcp(...) segment
        "user@tcp(h:3306)/db",  # credentials without ':'
        "u:p@tcp(host:notaport)/db",  # non-numeric port
        "u:p@tcp()/db",  # empty host
    ],
)
def test_parse_go_dsn_rejects_malformed(bad: str) -> None:
    with pytest.raises(ValueError):
        audit.parse_go_dsn(bad)


# --------------------------------------------------------------------------- #
# build_report: orphans / missing / vector coverage
# --------------------------------------------------------------------------- #


def test_build_report_orphans_carry_mysql_status_or_null() -> None:
    es_ids = {"a", "b", "c"}
    state = make_state(active={"a": "m-a"}, other={"b": "FAILED"})
    report = audit.build_report(es_ids, state)

    # a is ACTIVE in both stores; b is an orphan classified as FAILED; c has no
    # knowledge_document row at all -> mysqlStatus null.
    assert report["orphans"] == [
        {"documentId": "b", "mysqlStatus": "FAILED"},
        {"documentId": "c", "mysqlStatus": None},
    ]
    assert report["orphanCount"] == 2
    assert report["missing"] == []
    assert not report["consistent"]


def test_build_report_missing_lists_active_docs_absent_from_es() -> None:
    state = make_state(
        active={"a": "m-a", "d": "m-d"}, vector_md5s={"m-a", "m-d"}
    )
    report = audit.build_report({"a"}, state)

    assert report["missing"] == ["d"]
    assert report["missingCount"] == 1
    assert report["orphans"] == []
    assert not report["consistent"]


def test_build_report_vector_coverage_with_shared_md5() -> None:
    # a and b share one md5: one document_vectors row covers both. c's md5 has
    # no vector rows -> gap.
    state = make_state(
        active={"a": "m-shared", "b": "m-shared", "c": "m-lonely"},
        vector_md5s={"m-shared"},
    )
    report = audit.build_report({"a", "b", "c"}, state)

    assert report["vectorGaps"] == [{"documentId": "c", "fileMd5": "m-lonely"}]
    assert report["vectorGapCount"] == 1
    assert not report["consistent"]


def test_build_report_active_doc_without_md5_is_a_vector_gap() -> None:
    state = make_state(active={"a": ""})
    report = audit.build_report({"a"}, state)
    assert report["vectorGaps"] == [{"documentId": "a", "fileMd5": ""}]


def test_build_report_consistent_report_and_exit_codes() -> None:
    state = make_state(active={"a": "m-a"}, vector_md5s={"m-a"})
    report = audit.build_report({"a"}, state)

    assert report["consistent"] is True
    assert report["esUniqueDocumentIds"] == 1
    assert report["mysqlActiveDocuments"] == 1
    assert report["mysqlCounts"] == {
        "ACTIVE": 1,
        "FAILED": 0,
        "SKIPPED": 0,
        "STAGED": 0,
    }
    assert audit.exit_code_for(report) == 0

    # any orphan flips the exit code to 1
    inconsistent = audit.build_report({"a", "ghost"}, state)
    assert audit.exit_code_for(inconsistent) == 1


def test_exit_code_for_reports_inconsistency_for_missing_and_gaps() -> None:
    complete = make_state(active={"a": "m-a"}, vector_md5s={"m-a"})
    missing = make_state(active={"a": "m-a", "z": "m-z"}, vector_md5s={"m-a", "m-z"})
    gap = make_state(active={"a": "m-a"})  # m-a has no vector rows

    assert audit.exit_code_for(audit.build_report({"a"}, complete)) == 0
    assert audit.exit_code_for(audit.build_report({"a"}, missing)) == 1
    assert audit.exit_code_for(audit.build_report({"a"}, gap)) == 1


# --------------------------------------------------------------------------- #
# query_vector_md5s: batched IN queries
# --------------------------------------------------------------------------- #


def test_query_vector_md5s_batches_500_at_a_time() -> None:
    md5s = [f"md5-{i:05d}" for i in range(1200)]
    seen: list[tuple[str, tuple]] = []

    def fake_query(sql: str, params: tuple) -> list[dict]:
        seen.append((sql, params))
        return [{"file_md5": params[0]}]  # only each batch's first md5 is covered

    covered = audit.query_vector_md5s(fake_query, md5s)

    assert [len(params) for _sql, params in seen] == [500, 500, 200]
    for sql, params in seen:
        assert "FROM document_vectors" in sql
        assert "file_md5 IN" in sql
        assert sql.count("%s") == len(params)
    assert covered == {"md5-00000", "md5-00500", "md5-01000"}


def test_query_vector_md5s_dedups_inputs_and_skips_empty() -> None:
    seen: list[tuple] = []

    def fake_query(sql: str, params: tuple) -> list:
        seen.append(params)
        return []

    covered = audit.query_vector_md5s(fake_query, ["x", "x", "y", ""])
    assert covered == set()
    assert seen == [("x", "y")]  # deduplicated, empty md5 never queried


def test_query_vector_md5s_accepts_tuple_rows() -> None:
    def fake_query(sql: str, params: tuple) -> list[tuple]:
        return [(md5,) for md5 in params]

    covered = audit.query_vector_md5s(fake_query, ["x", "y"], batch_size=1)
    assert covered == {"x", "y"}


# --------------------------------------------------------------------------- #
# fetch_es_document_ids: scroll + dedup
# --------------------------------------------------------------------------- #


def test_fetch_es_document_ids_scrolls_pages_and_dedups(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    fake = FakeES(pages=[["a", "b"], ["b", "c"], ["c"]])
    monkeypatch.setattr(audit.requests, "post", fake.post)
    monkeypatch.setattr(audit.requests, "delete", fake.delete)

    ids = audit.fetch_es_document_ids("http://es:9200", INDEX)

    assert ids == {"a", "b", "c"}

    # the initial query pulls only the document_id field and targets the index
    first = fake.post_calls[0]
    assert first["json"]["_source"] == ["document_id"]
    assert f"/{INDEX}/_search" in first["url"]
    assert "scroll=" in first["url"]

    # subsequent pages are fetched through _search/scroll until an empty page
    scroll_posts = [c for c in fake.post_calls[1:] if "_search/scroll" in c["url"]]
    assert len(scroll_posts) >= 2

    # the scroll context is cleared when done
    assert fake.delete_calls
    assert "_search/scroll" in fake.delete_calls[0]["url"]


def test_fetch_es_document_ids_raises_on_http_error(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    fake = FakeES(pages=[], status_code=500)
    monkeypatch.setattr(audit.requests, "post", fake.post)
    monkeypatch.setattr(audit.requests, "delete", fake.delete)

    with pytest.raises(requests.HTTPError):
        audit.fetch_es_document_ids("http://es:9200", INDEX)


# --------------------------------------------------------------------------- #
# main: exit codes, JSON report shape, list-limit, config fallback
# --------------------------------------------------------------------------- #


def test_main_exit_0_when_fully_consistent(
    monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    state = make_state(active={"a": "m-a"}, vector_md5s={"m-a"})
    calls = patch_fetchers(monkeypatch, {"a"}, state)

    rc = audit.main(["--mysql-dsn", TEST_DSN])

    captured = capsys.readouterr()
    assert rc == 0
    report = json.loads(captured.out)
    assert report["consistent"] is True
    assert report["generation"] == GENERATION
    assert report["esIndex"] == INDEX
    assert "CONSISTENT" in captured.err
    assert calls["mysql"]["dsn"] == TEST_DSN
    assert calls["mysql"]["generation"] == GENERATION
    assert calls["es"]["index"] == INDEX


def test_main_exit_1_on_orphans_and_missing(
    monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    state = make_state(
        active={"a": "m-a", "m": "m-m"},
        other={"f": "FAILED"},
        vector_md5s={"m-a", "m-m"},
    )
    # ES has a + f (f is FAILED, not ACTIVE -> orphan) but lacks m -> missing
    patch_fetchers(monkeypatch, {"a", "f"}, state)

    rc = audit.main(["--mysql-dsn", TEST_DSN])

    captured = capsys.readouterr()
    assert rc == 1
    report = json.loads(captured.out)
    assert report["consistent"] is False
    assert report["orphans"] == [{"documentId": "f", "mysqlStatus": "FAILED"}]
    assert report["missing"] == ["m"]
    assert "INCONSISTENT" in captured.err


def test_main_list_limit_truncates_orphan_and_missing_lists(
    monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    active = {f"doc-{i}": f"m-{i}" for i in range(4)}
    ghosts = {f"ghost-{i}" for i in range(3)}
    state = make_state(active=active, vector_md5s={f"m-{i}" for i in range(4)})
    patch_fetchers(monkeypatch, set(active) | ghosts, state)

    rc = audit.main(["--mysql-dsn", TEST_DSN, "--list-limit", "2"])

    captured = capsys.readouterr()
    assert rc == 1
    report = json.loads(captured.out)
    assert len(report["orphans"]) == 2  # truncated to the limit
    assert report["orphanCount"] == 3  # true count preserved
    assert report["missing"] == []
    assert report["missingCount"] == 0


def test_main_exit_2_when_no_dsn_and_config_missing(
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
    tmp_path: Path,
) -> None:
    calls = patch_fetchers(monkeypatch, set(), make_state())

    rc = audit.main(["--config", str(tmp_path / "nope.yaml")])

    assert rc == 2
    assert "es" not in calls and "mysql" not in calls  # nothing was collected
    assert capsys.readouterr().err.strip() != ""


def test_main_exit_2_when_config_lacks_dsn(
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
    tmp_path: Path,
) -> None:
    config = tmp_path / "server.yaml"
    config.write_text("server:\n  port: '8081'\n", encoding="utf-8")
    patch_fetchers(monkeypatch, set(), make_state())

    rc = audit.main(["--config", str(config)])
    assert rc == 2


def test_main_reads_dsn_from_config_when_flag_absent(
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
    tmp_path: Path,
) -> None:
    config = tmp_path / "server.yaml"
    config.write_text(
        'database:\n  mysql:\n    dsn: "u:p@tcp(127.0.0.1:3306)/db"\n',
        encoding="utf-8",
    )
    state = make_state(active={"a": "m-a"}, vector_md5s={"m-a"})
    calls = patch_fetchers(monkeypatch, {"a"}, state)

    rc = audit.main(["--config", str(config)])

    assert rc == 0
    assert calls["mysql"]["dsn"] == "u:p@tcp(127.0.0.1:3306)/db"


def test_main_exit_2_when_es_collection_fails(
    monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    def boom(es_url: str, index: str, **kwargs: object) -> set[str]:
        raise RuntimeError("connection refused")

    monkeypatch.setattr(audit, "fetch_es_document_ids", boom)
    monkeypatch.setattr(audit, "fetch_mysql_state", lambda *a, **k: make_state())

    rc = audit.main(["--mysql-dsn", TEST_DSN])

    assert rc == 2
    assert "connection refused" in capsys.readouterr().err


def test_main_exit_2_when_mysql_collection_fails(
    monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    monkeypatch.setattr(audit, "fetch_es_document_ids", lambda *a, **k: set())

    def boom(dsn: str, generation: str, **kwargs: object) -> dict:
        raise RuntimeError("access denied for user")

    monkeypatch.setattr(audit, "fetch_mysql_state", boom)

    rc = audit.main(["--mysql-dsn", TEST_DSN])

    assert rc == 2
    assert "access denied" in capsys.readouterr().err
