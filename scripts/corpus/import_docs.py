"""Corpus importer — dedicated client for the internal corpus entry.

This is the trusted client for the official technical corpus pipeline. Unlike
the public ``/api/v1/upload/*`` path, it talks only to the internal corpus
entrypoints protected by ``X-Internal-Token``:

    POST /internal/orchestrator/knowledge-ingest
         (multipart: file + userId + orgTag + isPublic + six provenance fields)
    GET  /internal/orchestrator/knowledge-documents?generation=&status=

Per document it:
  1. checks whether the document is already ACTIVE (legitimate skip — duplicate
     imports are detected from the ACTIVE polling result, never from fast-upload);
  2. uploads the raw bytes with full corpus provenance (the server recomputes
     md5/sha256 over the stream and rejects a forged source hash);
  3. polls the read-only status endpoint until the document is ACTIVE or FAILED
     (or the polling budget is exhausted -> treated as failed).

Exit codes: 0 = all ACTIVE or legitimate skip; 1 = any document failed (or a
``--file`` pointed at a path the manifest excludes); 2 = parameter / manifest /
preflight error (missing token file, missing staging dir, unparseable manifest,
unknown source, incomplete policy). The internal token is read only from
``--token-file`` and is never written to stdout, logs, or the JSON report.

Usage:
    python scripts/corpus/import_docs.py \\
        --server http://127.0.0.1:8081 --token-file "$TEMP/corpus-token.txt" \\
        --staging "$TEMP/corpus-pins" --source go [--limit 20] [--file doc/asm.html] \\
        [--report results/corpus/go.json]
"""

from __future__ import annotations

import argparse
import hashlib
import json
import math
import re
import sys
import time
from pathlib import Path
from urllib.parse import urlencode

import requests
import yaml

try:
    import pymysql
    _HAS_PYMYSQL = True
except ImportError:
    pymysql = None
    _HAS_PYMYSQL = False

CHUNK_SIZE = 5 * 1024 * 1024
DEFAULT_TARGET_INDEX = "knowledge_base_v2_bge_m3"
DEFAULT_POLL_TIMEOUT_SECONDS = 300
DEFAULT_POLL_INTERVAL_SECONDS = 3
DEFAULT_MYSQL_DSN = ""  # set via --mysql-dsn CLI arg or MYSQL_DSN env var


def _parse_mysql_dsn(dsn: str) -> dict:
    """Parse a Go-style MySQL DSN into pymysql connect kwargs.

    Format: ``user:password@tcp(host:port)/dbname?param1=val1&param2=val2``
    Go-only params (parseTime, loc, readTimeout, writeTimeout, timeout) are
    silently dropped; charset is preserved as the charset key. The password
    may itself contain ``:`` and ``@`` (the separator is the rightmost
    ``@tcp(``). Raises ValueError on malformed input; the DSN is never
    included in error messages because it carries a password.
    """
    _GO_ONLY = frozenset(
        {"parsetime", "loc", "readtimeout", "writetimeout", "timeout"}
    )

    dsn_stripped = (dsn or "").strip()
    sep = dsn_stripped.rfind("@tcp(")
    if sep < 0:
        raise ValueError("not a Go-style MySQL DSN: missing @tcp(host:port)")
    credentials = dsn_stripped[:sep]
    rest = dsn_stripped[sep + len("@tcp("):]  # host:port)/db?params...

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
        port = int(port_text) if port_text else 3306
    except ValueError:
        raise ValueError("not a Go-style MySQL DSN: port is not an integer") from None

    after = rest[close + 1:]  # /dbname?params...
    if not after.startswith("/"):
        raise ValueError(
            "not a Go-style MySQL DSN: missing /database after tcp(...)"
        )
    database, _, query = after[1:].partition("?")
    if not database:
        raise ValueError("not a Go-style MySQL DSN: empty database name")

    charset = "utf8mb4"
    if query:
        for pair in query.split("&"):
            if not pair:
                continue
            key, _, value = pair.partition("=")
            if key.lower() in _GO_ONLY:
                continue
            if key.lower() == "charset":
                charset = value

    return {
        "user": user,
        "password": password,
        "host": host,
        "port": port,
        "database": database,
        "charset": charset,
    }


# --------------------------------------------------------------------------- #
# Hash + URL helpers
# --------------------------------------------------------------------------- #


def md5_of(data: bytes) -> str:
    return hashlib.md5(data).hexdigest()


def sha256_of(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def knowledge_ingest_url(server: str) -> str:
    return f"{server.rstrip('/')}/internal/orchestrator/knowledge-ingest"


def knowledge_documents_url(server: str, generation: str, status: str) -> str:
    query = urlencode({"generation": generation, "status": status})
    return f"{server.rstrip('/')}/internal/orchestrator/knowledge-documents?{query}"


def source_url(repository_url: str, commit: str, rel_path: str) -> str:
    """GitHub-style blob URL: <repository_url>/blob/<commit>/<path>."""
    return f"{repository_url.rstrip('/')}/blob/{commit}/{rel_path}"


def document_id(source_id: str, commit: str, source_path: str) -> str:
    """Mirror model.DocumentID: {source_id}@{commit}:{source_path}."""
    return f"{source_id}@{commit}:{source_path}"


# --------------------------------------------------------------------------- #
# Manifest + path policy (aligned with internal/corpus/manifest.go)
# --------------------------------------------------------------------------- #


def load_manifest(manifest_path: Path) -> dict:
    """Parse the corpus manifest YAML into a dict.

    Raises OSError on read failure and ValueError on parse/shape failure so
    ``main`` can map both to the preflight exit code (2).
    """
    text = manifest_path.read_text(encoding="utf-8")
    try:
        manifest = yaml.safe_load(text)
    except yaml.YAMLError as exc:
        raise ValueError(f"manifest parse error in {manifest_path}: {exc}") from exc
    if not isinstance(manifest, dict):
        raise ValueError(f"manifest {manifest_path} must be a YAML mapping")
    return manifest


def resolve_source(manifest: dict, source: str) -> dict:
    for entry in manifest.get("sources", []):
        if entry.get("source_id") == source:
            return entry
    raise ValueError(f"unknown corpus source: {source}")


def _glob_to_regex(pattern: str) -> str:
    """Translate a glob to a regex where '*' and '?' do not cross '/'.

    This matches Go's ``filepath.Match`` semantics so manifest globs behave the
    same on the importer side as they do on the server side.
    """
    parts: list[str] = []
    for ch in pattern:
        if ch == "*":
            parts.append("[^/]*")
        elif ch == "?":
            parts.append("[^/]")
        else:
            parts.append(re.escape(ch))
    return "".join(parts)


def path_matches(pattern: str, relative_path: str) -> bool:
    """Match a manifest glob against a slash-separated path.

    Mirrors ``internal/corpus/manifest.go pathMatches``: a trailing ``/`` (or
    ``/**``) matches the whole subtree; a plain directory pattern matches the
    directory and its subtree; ``*`` is a segment-local wildcard.
    """
    pattern = pattern.replace("\\", "/").strip()
    if not pattern:
        return False
    if pattern.endswith("/"):
        pattern += "**"
    if pattern == relative_path:
        return True
    if pattern.endswith("/**"):
        prefix = pattern[:-3]
        return relative_path == prefix or relative_path.startswith(f"{prefix}/")
    if "*" not in pattern and relative_path.startswith(f"{pattern}/"):
        return True
    if "*" in pattern and re.fullmatch(_glob_to_regex(pattern), relative_path):
        return True
    return False


def _policy_includes(relative_path: str, includes: list[str], excludes: list[str]) -> bool:
    """Apply manifest include/exclude rules; exclude wins, traversal rejected."""
    cleaned = relative_path.replace("\\", "/")
    if cleaned.startswith("./"):
        cleaned = cleaned[2:]
    # Reject absolute paths and traversal segments defensively.
    if cleaned.startswith("/") or "../" in cleaned or "..\\" in cleaned:
        return False
    for pattern in excludes:
        if path_matches(pattern, cleaned):
            return False
    for pattern in includes:
        if path_matches(pattern, cleaned):
            return True
    return False


def select_files(spec: dict, source_dir: Path) -> list[Path]:
    """Return the deterministic set of files selected by a source policy."""
    includes = [str(p) for p in (spec.get("include_paths") or [])]
    excludes = [str(p) for p in (spec.get("exclude_paths") or [])]
    allowed_formats = {str(ext).lower().lstrip(".") for ext in (spec.get("allowed_formats") or [])}
    if not includes or not allowed_formats:
        raise ValueError(
            f"source {spec.get('source_id', '?')} has incomplete document policy "
            "(include_paths and allowed_formats are both required)"
        )

    selected: list[Path] = []
    for path in sorted(source_dir.rglob("*")):
        if not path.is_file():
            continue
        relative_path = path.relative_to(source_dir).as_posix()
        extension = path.suffix.lower().lstrip(".")
        if extension not in allowed_formats:
            continue
        if not _policy_includes(relative_path, includes, excludes):
            continue
        selected.append(path)
    return selected


def manifest_document_paths(source: str, source_dir: Path, manifest_path: Path) -> list[Path]:
    """Public selector: load manifest, resolve source, apply policy."""
    manifest = load_manifest(manifest_path)
    spec = resolve_source(manifest, source)
    return select_files(spec, source_dir)


# --------------------------------------------------------------------------- #
# Server interaction
# --------------------------------------------------------------------------- #


# --------------------------------------------------------------------------- #
# MySQL-backed fast polling — O(1) status lookup per document instead of
# pulling the full ACTIVE/SKIPPED/FAILED list over HTTP every 3 seconds.
# When the HTTP list grows to thousands of documents (1 MB+) the HTTP polling
# path transfers gigabytes of redundant JSON during a full-source import.
# --------------------------------------------------------------------------- #


def _mysql_poll_status(
    doc_id: str,
    generation: str,
    *,
    timeout_seconds: int,
    interval_seconds: int,
    mysql_dsn: str,
) -> tuple[str, str]:
    """Poll MySQL until the document exits STAGING; returns (status, failure)."""
    attempts = 1
    if timeout_seconds > 0 and interval_seconds > 0:
        attempts = max(1, int(math.ceil(timeout_seconds / interval_seconds)))
    for _ in range(attempts):
        try:
            conn = pymysql.connect(**mysql_dsn)
            cur = conn.cursor()
            cur.execute(
                "SELECT status, last_error FROM knowledge_document "
                "WHERE document_id=%s AND corpus_generation=%s "
                "AND status IN ('ACTIVE','SKIPPED','FAILED') "
                "LIMIT 1",
                (doc_id, generation),
            )
            row = cur.fetchone()
            conn.close()
            if row is not None:
                status, last_error = row
                return (status.lower(), str(last_error or ""))
        except Exception:
            pass  # fall through to sleep and retry
        time.sleep(interval_seconds)
    return ("failed", f"polling timed out after {attempts} attempt(s)")


def list_documents(
    server: str, token: str, generation: str, status: str, *, http_timeout: int = 30
) -> list[dict]:
    """GET the read-only document status list (one status at a time)."""
    resp = requests.get(
        knowledge_documents_url(server, generation, status),
        headers={"X-Internal-Token": token},
        timeout=http_timeout,
    )
    resp.raise_for_status()
    payload = resp.json() or {}
    docs = payload.get("documents")
    return docs if isinstance(docs, list) else []


def is_document_active(
    server: str, token: str, generation: str, doc_id: str, *, http_timeout: int = 30,
    _mysql_dsn: str | None = None,
) -> bool:
    if _HAS_PYMYSQL and _mysql_dsn:
        conn = None
        conn2 = None
        try:
            conn = pymysql.connect(**_mysql_dsn)
            cur = conn.cursor()
            cur.execute(
                "SELECT 1 FROM knowledge_document "
                "WHERE document_id=%s AND corpus_generation=%s AND status='ACTIVE' LIMIT 1",
                (doc_id, generation),
            )
            found = cur.fetchone() is not None
            cur.close()
            conn.close()
            conn = None
            if found:
                return True
            # Also check SKIPPED — legacy docs may be SKIPPED but functionally complete
            conn2 = pymysql.connect(**_mysql_dsn)
            cur2 = conn2.cursor()
            cur2.execute(
                "SELECT 1 FROM knowledge_document "
                "WHERE document_id=%s AND corpus_generation=%s AND status='SKIPPED' LIMIT 1",
                (doc_id, generation),
            )
            found2 = cur2.fetchone() is not None
            cur2.close()
            conn2.close()
            conn2 = None
            return found2
        except Exception:
            pass  # fall through to HTTP
        finally:
            if conn is not None:
                try:
                    conn.close()
                except Exception:
                    pass
            if conn2 is not None:
                try:
                    conn2.close()
                except Exception:
                    pass
    return any(
        d.get("documentId") == doc_id
        for d in list_documents(server, token, generation, "ACTIVE", http_timeout=http_timeout)
    )


def upload_one(
    server: str,
    token: str,
    *,
    path: Path,
    source_id: str,
    source_path: str,
    source_commit: str,
    repository_url: str,
    corpus_generation: str,
    target_index: str,
    loader_user: int,
    run_id: str,
    http_timeout: int = 120,
    org_tag: str = "corpus",
) -> str:
    """Upload one document through the dedicated corpus entry; return documentId.

    Computes md5 + sha256 over the raw staging bytes, posts the multipart form
    with the full provenance chain, and parses the 202 response for documentId.
    Never calls ``/api/v1/upload/fast-upload`` — corpus duplicates are detected
    via the ACTIVE status poll, not the legacy fast-upload probe.

    ``run_id`` pins this upload to a controlled run (import-<unix>); it lands in
    the form's ``runId`` so the server enqueues a run-scoped parse task and the
    consumer's run-aware dedup bypasses any stale historical SUCCESS for this
    file_md5, forcing parse/chunk/embed/index to re-execute.
    """
    data = path.read_bytes()
    content_sha256 = sha256_of(data)

    form = {
        "userId": str(loader_user),
        "orgTag": org_tag,
        "isPublic": "true",
        "sourceId": source_id,
        "sourcePath": source_path,
        "sourceUrl": source_url(repository_url, source_commit, source_path),
        "sourceCommit": source_commit,
        "sourceSha256": content_sha256,
        "targetIndex": target_index,
        "corpusGeneration": corpus_generation,
        "runId": run_id,
    }
    files = {"file": (source_path, data, "application/octet-stream")}
    resp = requests.post(
        knowledge_ingest_url(server),
        headers={"X-Internal-Token": token},
        files=files,
        data=form,
        timeout=http_timeout,
    )
    resp.raise_for_status()
    payload = resp.json() or {}
    data_field = payload.get("data") if isinstance(payload, dict) else None
    if isinstance(data_field, dict) and data_field.get("documentId"):
        return str(data_field["documentId"])
    return document_id(source_id, source_commit, source_path)


def poll_document(
    server: str,
    token: str,
    generation: str,
    doc_id: str,
    *,
    timeout_seconds: int,
    interval_seconds: int,
    http_timeout: int = 30,
    _mysql_dsn: dict | None = None,
) -> tuple[str, str]:
    """Poll until the document is ACTIVE, SKIPPED, or FAILED (or budget exhausted).

    Returns (status, failure) where status is ``active`` / ``skipped`` /
    ``failed``. When pymysql is available and _mysql_dsn is passed, uses a
    single-row MySQL query per poll interval instead of pulling the full
    ACTIVE/SKIPPED/FAILED lists over HTTP — the HTTP path transfers ~1 MB+
    per poll once there are thousands of documents, turning a 928-doc import
    into a multi-hour affair.
    """
    if _HAS_PYMYSQL and _mysql_dsn:
        return _mysql_poll_status(
            doc_id, generation,
            timeout_seconds=timeout_seconds,
            interval_seconds=interval_seconds,
            mysql_dsn=_mysql_dsn,
        )

    attempts = 1
    if timeout_seconds > 0 and interval_seconds > 0:
        attempts = max(1, int(math.ceil(timeout_seconds / interval_seconds)))
    for _ in range(attempts):
        for doc in list_documents(server, token, generation, "ACTIVE", http_timeout=http_timeout):
            if doc.get("documentId") == doc_id:
                return ("active", "")
        for doc in list_documents(server, token, generation, "SKIPPED", http_timeout=http_timeout):
            if doc.get("documentId") == doc_id:
                return ("skipped", "")
        for doc in list_documents(server, token, generation, "FAILED", http_timeout=http_timeout):
            if doc.get("documentId") == doc_id:
                last_error = str(doc.get("lastError") or "").strip()
                return ("failed", last_error or "document reported FAILED")
        time.sleep(interval_seconds)
    return ("failed", f"polling timed out after {attempts} attempt(s)")


def process_one(
    server: str,
    token: str,
    *,
    path: Path,
    source_dir: Path,
    source_id: str,
    source_commit: str,
    repository_url: str,
    generation: str,
    target_index: str,
    loader_user: int,
    run_id: str,
    poll_timeout: int,
    poll_interval: int,
    http_timeout: int,
    _mysql_dsn: dict | None = None,
    force: bool = False,
) -> dict:
    """Run the full per-document lifecycle; return a report file record."""
    rel = path.relative_to(source_dir).as_posix()
    data = path.read_bytes()
    file_md5 = md5_of(data)
    content_sha256 = sha256_of(data)
    base_record = {
        "path": rel,
        "sha256": content_sha256,
        "md5": file_md5,
        "status": "failed",
        "failure": "",
        "documentId": document_id(source_id, source_commit, rel),
    }

    if not force and is_document_active(
        server, token, generation, base_record["documentId"],
        http_timeout=http_timeout, _mysql_dsn=_mysql_dsn,
    ):
        base_record["status"] = "skipped"
        return base_record

    try:
        doc_id = upload_one(
            server,
            token,
            path=path,
            source_id=source_id,
            source_path=rel,
            source_commit=source_commit,
            repository_url=repository_url,
            corpus_generation=generation,
            target_index=target_index,
            loader_user=loader_user,
            run_id=run_id,
            http_timeout=http_timeout,
        )
    except Exception as exc:  # noqa: BLE001 — surface a sanitized per-file failure
        base_record["failure"] = str(exc) or "upload failed"
        return base_record
    base_record["documentId"] = doc_id

    status, failure = poll_document(
        server,
        token,
        generation,
        doc_id,
        timeout_seconds=poll_timeout,
        interval_seconds=poll_interval,
        http_timeout=http_timeout,
        _mysql_dsn=_mysql_dsn,
    )
    base_record["status"] = status
    base_record["failure"] = failure
    return base_record


# --------------------------------------------------------------------------- #
# Report
# --------------------------------------------------------------------------- #


def build_report(
    *,
    run_id: str,
    source: str,
    generation: str,
    commit: str,
    selected: int,
    records: list[dict],
    attempted: int,
    duration_sec: float,
) -> dict:
    active = sum(1 for r in records if r["status"] == "active")
    skipped = sum(1 for r in records if r["status"] == "skipped")
    failed = sum(1 for r in records if r["status"] == "failed")
    queued = active + failed
    return {
        "runId": run_id,
        "source": source,
        "generation": generation,
        "commit": commit,
        "selected": selected,
        "attempted": attempted,
        "queued": queued,
        "active": active,
        "skipped": skipped,
        "failed": failed,
        "durationSec": round(duration_sec, 3),
        "files": records,
    }


def write_report(path: Path, report: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(report, indent=2, sort_keys=True), encoding="utf-8")


# --------------------------------------------------------------------------- #
# CLI
# --------------------------------------------------------------------------- #


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Import official corpus via the dedicated internal entry.")
    parser.add_argument("--server", default="http://127.0.0.1:8081")
    parser.add_argument("--token-file", required=True, type=Path, help="path to a file containing the X-Internal-Token value")
    parser.add_argument("--manifest", type=Path, default=Path("corpus/sources.yaml"))
    parser.add_argument("--staging", required=True, type=Path, help="root staging dir; source checkout at <staging>/<source>")
    parser.add_argument("--source", required=True, help="manifest source_id to import")
    parser.add_argument("--limit", type=int, default=0, help="cap on attempted documents (0 = all)")
    parser.add_argument("--file", dest="selected_file", default="", help="restrict to one manifest-relative source path")
    parser.add_argument("--target-index", default=DEFAULT_TARGET_INDEX)
    parser.add_argument("--loader-user", type=int, default=None, help="override manifest loader_user")
    parser.add_argument("--report", type=Path, default=None, help="write a sanitized JSON report to this path")
    parser.add_argument("--poll-timeout-seconds", type=int, default=DEFAULT_POLL_TIMEOUT_SECONDS)
    parser.add_argument("--poll-interval-seconds", type=int, default=DEFAULT_POLL_INTERVAL_SECONDS)
    parser.add_argument(
        "--http-timeout",
        type=int,
        default=120,
        help="per-request HTTP timeout in seconds (slow-ES resilience; uploads and status polls)",
    )
    parser.add_argument(
        "--mysql-fast-poll",
        action="store_true",
        default=False,
        help="poll document status via MySQL (O(1) per poll) instead of pulling "
        "the full per-status list over HTTP (O(N) per poll)"
    )
    parser.add_argument(
        "--mysql-dsn",
        default="",
        help="Go-style MySQL DSN user:pass@tcp(host:port)/db for --mysql-fast-poll "
        "(falls back to MYSQL_DSN env var)",
    )
    parser.add_argument(
        "--force",
        action="store_true",
        default=False,
        help="re-import documents even when already ACTIVE in MySQL",
    )
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    started = time.monotonic()
    run_id = f"import-{int(time.time())}"

    # Preflight: token file (must exist before we touch the network).
    try:
        token = args.token_file.read_text(encoding="utf-8").strip()
    except OSError as exc:
        print(f"token file unreadable: {exc}", file=sys.stderr)
        return 2
    if not token:
        print("token file is empty", file=sys.stderr)
        return 2

    # MySQL fast-poll DSN — read from CLI or env var so the HTTP polling
    # path is unchanged unless the user opts in with --mysql-fast-poll.
    import os

    mysql_dsn: dict | None = None
    if args.mysql_fast_poll:
        dsn_str = args.mysql_dsn or os.environ.get("MYSQL_DSN", "")
        if not dsn_str:
            print(
                "--mysql-fast-poll requires --mysql-dsn or MYSQL_DSN env var",
                file=sys.stderr,
            )
            return 2
        if _HAS_PYMYSQL:
            mysql_dsn = _parse_mysql_dsn(dsn_str)

    # Preflight: staging source dir.
    source_dir = args.staging / args.source
    if not source_dir.is_dir():
        print(f"source dir not found: {source_dir}", file=sys.stderr)
        return 2

    # Preflight: manifest + source resolution.
    try:
        manifest = load_manifest(args.manifest)
        spec = resolve_source(manifest, args.source)
    except (OSError, ValueError) as exc:
        print(f"manifest error: {exc}", file=sys.stderr)
        return 2

    generation = str(manifest.get("generation", "")).strip()
    source_commit = str(spec.get("source_commit", "")).strip()
    repository_url = str(spec.get("repository_url", "")).strip()
    if not generation or not source_commit:
        print(f"source {args.source} has incomplete provenance metadata", file=sys.stderr)
        return 2
    loader_user = args.loader_user if args.loader_user is not None else int(spec.get("loader_user", 0) or 0)

    try:
        files = select_files(spec, source_dir)
    except ValueError as exc:
        print(f"manifest policy error: {exc}", file=sys.stderr)
        return 2

    if args.selected_file:
        target = args.selected_file.replace("\\", "/")
        if target.startswith("./"):
            target = target[2:]
        files = [p for p in files if p.relative_to(source_dir).as_posix() == target]
        if not files:
            print(
                f"file not selected by manifest policy: {args.source}/{args.selected_file}",
                file=sys.stderr,
            )
            return 1

    selected_count = len(files)
    records: list[dict] = []
    attempted = 0
    print(f"source {args.source}: {selected_count} manifest-selected file(s); generation={generation}")
    for path in files:
        if args.limit and attempted >= args.limit:
            break
        attempted += 1
        rel = path.relative_to(source_dir).as_posix()
        record = process_one(
            args.server,
            token,
            path=path,
            source_dir=source_dir,
            source_id=args.source,
            source_commit=source_commit,
            repository_url=repository_url,
            generation=generation,
            target_index=args.target_index,
            loader_user=loader_user,
            run_id=run_id,
            poll_timeout=args.poll_timeout_seconds,
            poll_interval=args.poll_interval_seconds,
            http_timeout=args.http_timeout,
            _mysql_dsn=mysql_dsn,
            force=args.force,
        )
        records.append(record)
        print(f"  {record['status']:7s} {rel}" + (f" ({record['failure']})" if record["failure"] else ""))

    report = build_report(
        run_id=run_id,
        source=args.source,
        generation=generation,
        commit=source_commit,
        selected=selected_count,
        records=records,
        attempted=attempted,
        duration_sec=time.monotonic() - started,
    )

    if args.report is not None:
        try:
            write_report(args.report, report)
        except OSError as exc:
            print(f"report write failed: {exc}", file=sys.stderr)

    print(
        "done: "
        f"attempted={report['attempted']} queued={report['queued']} "
        f"active={report['active']} skipped={report['skipped']} failed={report['failed']}"
    )
    return 1 if report["failed"] else 0


if __name__ == "__main__":
    sys.exit(main())
