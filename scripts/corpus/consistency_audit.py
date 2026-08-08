"""Corpus consistency audit — cross-checks the three stores one import touches.

After a corpus import lands, three stores must agree:

  ES  ``knowledge_base_v2_bge_m3``    chunk documents carry ``document_id``
  MySQL ``knowledge_document``        one row per document (status/file_md5)
                                      for the corpus generation under audit
  MySQL ``document_vectors``          vector rows keyed by ``file_md5``

The audit pulls the unique ``document_id`` set out of the ES index via the
scroll API (``_source=["document_id"]`` only — chunk bodies are never
transferred), reads the generation's rows from ``knowledge_document``
(ACTIVE set + four-status counts + document_id→status map), and verifies that
every ACTIVE document's ``file_md5`` has at least one ``document_vectors``
row (batched ``IN`` queries, 500 md5s per batch).

Findings:
  orphan      — ES has chunks but the document is not ACTIVE in MySQL
                (the row's status, e.g. FAILED, is reported; null when the
                document_id is unknown to knowledge_document)
  missing     — MySQL lists the document ACTIVE but ES holds no chunks
  vector gap  — an ACTIVE document whose file_md5 has no document_vectors row

Exit codes: 0 = fully consistent; 1 = orphans / missing documents / vector
gaps found; 2 = parameter, connection, or collection error (no DSN available,
ES unreachable, MySQL unreachable, malformed DSN). The DSN is never echoed to
stdout/stderr or the JSON report — it carries a password.

Usage:
    python scripts/corpus/consistency_audit.py \\
        [--es-url http://127.0.0.1:9200] [--index knowledge_base_v2_bge_m3] \\
        [--mysql-dsn "user:pass@tcp(host:port)/db?charset=utf8mb4"] \\
        [--config configs/server.yaml] [--generation techdocs-2026-07-30-v1] \\
        [--list-limit 50]
"""

from __future__ import annotations

import argparse
import json
import sys
from collections.abc import Callable, Iterable
from pathlib import Path

import requests
import yaml

DEFAULT_ES_URL = "http://127.0.0.1:9200"
DEFAULT_INDEX = "knowledge_base_v2_bge_m3"
DEFAULT_CONFIG = "configs/server.yaml"
DEFAULT_GENERATION = "techdocs-2026-07-30-v1"
DEFAULT_LIST_LIMIT = 50
VECTOR_BATCH_SIZE = 500
DEFAULT_MYSQL_PORT = 3306
ES_SCROLL_KEEPALIVE = "2m"
ES_SCROLL_PAGE_SIZE = 1000

# go-sql-driver/mysql query parameters pymysql cannot consume; they are
# dropped by parse_go_dsn. Everything else (e.g. charset) is preserved.
_GO_ONLY_DSN_PARAMS = {
    "parsetime",
    "loc",
    "interpolateparams",
    "allownativepasswords",
    "allowoldpasswords",
    "allowcleartextpasswords",
    "collation",
    "timeout",
    "readtimeout",
    "writetimeout",
}


# --------------------------------------------------------------------------- #
# Go-style DSN + config resolution
# --------------------------------------------------------------------------- #


def parse_go_dsn(dsn: str) -> dict:
    """Parse ``user:pass@tcp(host:port)/db?params`` into connection fields.

    Returns ``{"user", "password", "host", "port", "database", "params"}``.
    Go-driver-only query parameters (parseTime, loc, ...) are dropped from
    ``params``; a missing port defaults to 3306; the password may itself
    contain ``:`` and ``@`` (the separator is the rightmost ``@tcp(``).
    Raises ValueError on malformed input; the DSN is never included in error
    messages because it carries a password.
    """
    dsn = (dsn or "").strip()
    sep = dsn.rfind("@tcp(")
    if sep < 0:
        raise ValueError("not a Go-style MySQL DSN: missing @tcp(host:port) segment")
    credentials, rest = dsn[:sep], dsn[sep + len("@tcp("):]
    if ":" not in credentials:
        raise ValueError("not a Go-style MySQL DSN: credentials must be user:password")
    user, password = credentials.split(":", 1)
    if not user:
        raise ValueError("not a Go-style MySQL DSN: empty user")
    close = rest.find(")")
    if close < 0 or not rest[:close]:
        raise ValueError("not a Go-style MySQL DSN: missing host in tcp(...)")
    host, _, port_text = rest[:close].partition(":")
    if not host:
        raise ValueError("not a Go-style MySQL DSN: empty host")
    try:
        port = int(port_text) if port_text else DEFAULT_MYSQL_PORT
    except ValueError as exc:
        raise ValueError("not a Go-style MySQL DSN: port is not an integer") from exc
    after = rest[close + 1:]
    if not after.startswith("/"):
        raise ValueError("not a Go-style MySQL DSN: missing /database after tcp(...)")
    database, _, query = after[1:].partition("?")
    if not database:
        raise ValueError("not a Go-style MySQL DSN: empty database name")
    params: dict[str, str] = {}
    for pair in query.split("&"):
        if not pair:
            continue
        key, _, value = pair.partition("=")
        if key.lower() in _GO_ONLY_DSN_PARAMS:
            continue
        params[key] = value
    return {
        "user": user,
        "password": password,
        "host": host,
        "port": port,
        "database": database,
        "params": params,
    }


def _expand_env_default(value: str) -> str:
    """Expand ``${ENV_VAR:default}`` placeholders using os.environ.

    This mirrors the Go server's ``expandEnvBind()`` so that Python scripts
    can read ``configs/server.yaml`` after its credentials were de-hardcoded
    to ``${ENV:default}`` placeholders (Phase 2 credential security fix).
    """
    import os as _os

    if not isinstance(value, str) or not value.startswith("${"):
        return value
    if not value.endswith("}"):
        return value
    inner = value[2:-1]
    if ":" not in inner:
        return _os.environ.get(inner, value)
    env_key, default = inner.split(":", 1)
    return _os.environ.get(env_key, default)


def resolve_mysql_dsn(explicit_dsn: str, config_path: Path) -> str:
    """Pick the MySQL DSN: an explicit flag wins; otherwise read
    ``database.mysql.dsn`` from server YAML and expand ``${ENV:default}``
    placeholders. Raises ValueError when neither source yields a DSN.
    """
    if explicit_dsn:
        return explicit_dsn
    try:
        text = config_path.read_text(encoding="utf-8")
    except OSError as exc:
        raise ValueError(f"config file unreadable: {config_path} ({exc})") from exc
    try:
        config = yaml.safe_load(text)
    except yaml.YAMLError as exc:
        raise ValueError(f"config file is not valid YAML: {config_path} ({exc})") from exc
    if not isinstance(config, dict):
        raise ValueError(f"config file must be a YAML mapping: {config_path}")
    mysql = (config.get("database") or {}).get("mysql") or {}
    dsn = mysql.get("dsn")
    if not dsn:
        raise ValueError(f"no database.mysql.dsn in config: {config_path}")
    dsn = str(dsn)
    if dsn.startswith("${"):
        dsn = _expand_env_default(dsn)
        if not dsn or dsn.startswith("${"):
            raise ValueError(
                f"config DSN uses ${{ENV:default}} placeholder but env var is not set; "
                f"set the env var or pass --mysql-dsn explicitly"
            )
    return dsn


# --------------------------------------------------------------------------- #
# ES collection (scroll, ID-only)
# --------------------------------------------------------------------------- #


def fetch_es_document_ids(
    es_url: str,
    index: str,
    *,
    scroll: str = ES_SCROLL_KEEPALIVE,
    size: int = ES_SCROLL_PAGE_SIZE,
    http_timeout: int = 60,
) -> set[str]:
    """Scroll the whole index pulling only ``document_id``; return unique IDs.

    Pages are deduplicated into a set as they arrive. The scroll context is
    cleared best-effort once exhausted. Non-2xx responses raise
    ``requests.HTTPError`` so callers can map collection failures to exit 2.
    """
    base = es_url.rstrip("/")
    resp = requests.post(
        f"{base}/{index}/_search?scroll={scroll}",
        json={"_source": ["document_id"], "size": size, "query": {"match_all": {}}},
        timeout=http_timeout,
    )
    resp.raise_for_status()
    payload = resp.json() or {}

    ids: set[str] = set()
    scroll_id = payload.get("_scroll_id")
    hits = (payload.get("hits") or {}).get("hits") or []
    while hits:
        for hit in hits:
            doc_id = (hit.get("_source") or {}).get("document_id")
            if doc_id:
                ids.add(str(doc_id))
        if not scroll_id:
            break
        resp = requests.post(
            f"{base}/_search/scroll",
            json={"scroll": scroll, "scroll_id": scroll_id},
            timeout=http_timeout,
        )
        resp.raise_for_status()
        payload = resp.json() or {}
        scroll_id = payload.get("_scroll_id")
        hits = (payload.get("hits") or {}).get("hits") or []

    if scroll_id:
        try:
            requests.delete(
                f"{base}/_search/scroll",
                json={"scroll_id": [scroll_id]},
                timeout=http_timeout,
            )
        except requests.RequestException:
            pass  # cleanup is best-effort; the context expires on its own
    return ids


# --------------------------------------------------------------------------- #
# MySQL collection (knowledge_document states + document_vectors coverage)
# --------------------------------------------------------------------------- #


def query_vector_md5s(
    query: Callable[[str, tuple], Iterable[object]],
    md5s: Iterable[str],
    *,
    batch_size: int = VECTOR_BATCH_SIZE,
) -> set[str]:
    """Return the subset of ``md5s`` that have at least one document_vectors row.

    Issues ``SELECT DISTINCT file_md5 ... WHERE file_md5 IN (...)`` in batches
    of at most ``batch_size`` placeholders. ``query(sql, params)`` must return
    rows whose first column (or ``file_md5`` key) is a file_md5. Empty md5s
    are deduplicated away and never queried.
    """
    unique = sorted({m for m in md5s if m})
    covered: set[str] = set()
    for start in range(0, len(unique), batch_size):
        batch = unique[start:start + batch_size]
        placeholders = ", ".join(["%s"] * len(batch))
        sql = (
            "SELECT DISTINCT file_md5 FROM document_vectors"
            f" WHERE file_md5 IN ({placeholders})"
        )
        for row in query(sql, tuple(batch)):
            if isinstance(row, dict):
                covered.add(str(row.get("file_md5", "")))
            else:
                covered.add(str(row[0]))
    return covered


def fetch_mysql_state(
    dsn: str, generation: str, *, vector_batch_size: int = VECTOR_BATCH_SIZE
) -> dict:
    """Collect the MySQL side of the audit for one corpus generation.

    Returns ``{"counts", "statuses", "active_ids", "active_md5s",
    "vector_md5s"}``: the four-status counts, the document_id→status map over
    every knowledge_document row of the generation, the ACTIVE document set
    with its file_md5s, and the md5s covered by document_vectors. pymysql is
    imported lazily so the audit's pure pieces stay importable without it.
    """
    import pymysql  # lazy: offline unit tests never reach this import

    cfg = parse_go_dsn(dsn)
    conn = pymysql.connect(
        host=cfg["host"],
        port=cfg["port"],
        user=cfg["user"],
        password=cfg["password"],
        database=cfg["database"],
        charset=cfg["params"].get("charset", "utf8mb4"),
        cursorclass=pymysql.cursors.DictCursor,
        connect_timeout=10,
    )
    try:
        with conn.cursor() as cursor:
            cursor.execute(
                "SELECT document_id, status, file_md5 FROM knowledge_document"
                " WHERE corpus_generation = %s",
                (generation,),
            )
            rows = cursor.fetchall()

            def query(sql: str, params: tuple) -> list:
                cursor.execute(sql, params)
                return list(cursor.fetchall())

            counts = {"ACTIVE": 0, "FAILED": 0, "SKIPPED": 0, "STAGED": 0}
            statuses: dict[str, str] = {}
            active_ids: set[str] = set()
            active_md5s: dict[str, str] = {}
            for row in rows:
                doc_id = str(row["document_id"])
                status = str(row["status"])
                file_md5 = str(row["file_md5"] or "")
                statuses[doc_id] = status
                if status in counts:
                    counts[status] += 1
                if status == "ACTIVE":
                    active_ids.add(doc_id)
                    active_md5s[doc_id] = file_md5
            vector_md5s = query_vector_md5s(
                query, active_md5s.values(), batch_size=vector_batch_size
            )
    finally:
        conn.close()
    return {
        "counts": counts,
        "statuses": statuses,
        "active_ids": active_ids,
        "active_md5s": active_md5s,
        "vector_md5s": vector_md5s,
    }


# --------------------------------------------------------------------------- #
# Report (pure set algebra)
# --------------------------------------------------------------------------- #


def build_report(es_ids: Iterable[str], mysql_state: dict) -> dict:
    """Compute orphans / missing / vector gaps from the two collected states.

    orphans: ES-only document_ids; ``mysqlStatus`` is the knowledge_document
    status when the row exists (e.g. FAILED) and None otherwise.
    missing: ACTIVE knowledge_document rows with no ES chunks.
    vectorGaps: ACTIVE documents whose file_md5 (possibly empty) has no
    document_vectors row; documents sharing an md5 are covered by one row.
    """
    es_set = {str(d) for d in es_ids if d}
    active_ids = set(mysql_state.get("active_ids") or set())
    statuses = dict(mysql_state.get("statuses") or {})
    active_md5s = dict(mysql_state.get("active_md5s") or {})
    vector_md5s = set(mysql_state.get("vector_md5s") or set())

    orphans = [
        {"documentId": doc_id, "mysqlStatus": statuses.get(doc_id)}
        for doc_id in sorted(es_set - active_ids)
    ]
    missing = sorted(active_ids - es_set)
    vector_gaps = [
        {"documentId": doc_id, "fileMd5": active_md5s.get(doc_id, "")}
        for doc_id in sorted(active_ids)
        if active_md5s.get(doc_id, "") not in vector_md5s
    ]
    return {
        "esUniqueDocumentIds": len(es_set),
        "mysqlCounts": dict(mysql_state.get("counts") or {}),
        "mysqlActiveDocuments": len(active_ids),
        "orphans": orphans,
        "orphanCount": len(orphans),
        "missing": missing,
        "missingCount": len(missing),
        "vectorGaps": vector_gaps,
        "vectorGapCount": len(vector_gaps),
        "consistent": not orphans and not missing and not vector_gaps,
    }


def truncate_report_lists(report: dict, limit: int) -> dict:
    """Return the report with orphan/missing lists capped at ``limit``.

    ``limit <= 0`` means unlimited. Counts are preserved so the true totals
    remain visible after truncation.
    """
    if limit <= 0:
        return report
    truncated = dict(report)
    for key in ("orphans", "missing"):
        values = truncated.get(key) or []
        if len(values) > limit:
            truncated[key] = values[:limit]
    return truncated


def exit_code_for(report: dict) -> int:
    """0 when the stores are consistent, 1 when any inconsistency remains."""
    return 0 if report.get("consistent") else 1


# --------------------------------------------------------------------------- #
# CLI
# --------------------------------------------------------------------------- #


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Audit ES <-> MySQL consistency for one corpus generation."
    )
    parser.add_argument("--es-url", default=DEFAULT_ES_URL)
    parser.add_argument("--index", default=DEFAULT_INDEX)
    parser.add_argument(
        "--mysql-dsn",
        default="",
        help="Go-style DSN user:pass@tcp(host:port)/db?params; overrides --config",
    )
    parser.add_argument(
        "--config",
        default=DEFAULT_CONFIG,
        help="server YAML consulted for database.mysql.dsn when --mysql-dsn is absent",
    )
    parser.add_argument("--generation", default=DEFAULT_GENERATION)
    parser.add_argument(
        "--list-limit",
        type=int,
        default=DEFAULT_LIST_LIMIT,
        help="cap orphan/missing lists in the JSON report (0 = unlimited)",
    )
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)

    try:
        dsn = resolve_mysql_dsn(args.mysql_dsn, Path(args.config))
    except ValueError as exc:
        print(f"mysql dsn unavailable: {exc}", file=sys.stderr)
        return 2

    try:
        es_ids = fetch_es_document_ids(args.es_url, args.index)
    except Exception as exc:  # noqa: BLE001 — any collection failure maps to exit 2
        print(f"es collection failed: {exc}", file=sys.stderr)
        return 2

    try:
        mysql_state = fetch_mysql_state(dsn, args.generation)
    except Exception as exc:  # noqa: BLE001 — any collection failure maps to exit 2
        print(f"mysql collection failed: {exc}", file=sys.stderr)
        return 2

    report = build_report(es_ids, mysql_state)
    report = truncate_report_lists(report, args.list_limit)
    output = {"generation": args.generation, "esIndex": args.index, **report}
    print(json.dumps(output, indent=2, sort_keys=True))

    verdict = "CONSISTENT" if report["consistent"] else "INCONSISTENT"
    print(
        f"audit {verdict}: es_docs={report['esUniqueDocumentIds']} "
        f"mysql_active={report['mysqlActiveDocuments']} "
        f"orphans={report['orphanCount']} missing={report['missingCount']} "
        f"vector_gaps={report['vectorGapCount']}",
        file=sys.stderr,
    )
    return exit_code_for(report)


if __name__ == "__main__":
    sys.exit(main())
