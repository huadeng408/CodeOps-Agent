"""Contract tests for the rebuilt corpus importer (Task 8).

The importer is the dedicated client for the internal corpus entry:
  POST /internal/orchestrator/knowledge-ingest   (X-Internal-Token, multipart)
  GET  /internal/orchestrator/knowledge-documents?generation=&status=

All tests are offline: ``requests.post``/``requests.get`` are monkeypatched
against an in-process fake, and the manifest/staging/token inputs are temp
files. No real network, no real server.
"""

from __future__ import annotations

import hashlib
import json
import re
from pathlib import Path

import pytest
import requests

from scripts.corpus import import_docs

GO_REPO = "https://github.com/golang/go"
GO_COMMIT = "5d29d80b6c9960c3fc39e14e3e8b9a0f52041fed"
GENERATION = "techdocs-2026-07-30-v1"


def write_manifest(path: Path, source: str = "go") -> None:
    path.write_text(
        f'''schema_version: "1"
generation: "{GENERATION}"
sources:
  - source_id: "{source}"
    repository_url: "{GO_REPO}"
    source_commit: "{GO_COMMIT}"
    include_paths: ["doc/"]
    exclude_paths: ["doc/private", "doc/*.txt"]
    allowed_formats: ["md", "html"]
    loader_user: 1
''',
        encoding="utf-8",
    )


class FakeResp:
    def __init__(self, status_code: int, payload: dict | None = None) -> None:
        self.status_code = status_code
        self._payload = payload or {}

    def json(self) -> dict:
        return self._payload

    def raise_for_status(self) -> None:
        if self.status_code >= 400:
            raise requests.HTTPError(f"status {self.status_code}")


class FakeServer:
    """In-process stand-in for the RAG server's corpus endpoints.

    Default behaviour: a POST to the ingest endpoint accepts the document
    (202) and immediately reflects it as ACTIVE in the status list, so the
    importer's post-upload poll observes ACTIVE. Per-test hooks (``on_post``)
    can redirect the document to FAILED or stall it to exercise the
    failed / timeout paths.
    """

    def __init__(self) -> None:
        self.active: list[dict] = []
        self.failed: list[dict] = []
        self.skipped: list[dict] = []
        self.post_urls: list[str] = []
        self.post_calls: list[dict] = []
        self.get_count = 0
        self.get_status_queries: list[str] = []
        self.on_post = None

    def get(self, url: str, headers=None, timeout=None):
        self.get_count += 1
        if "status=ACTIVE" in url:
            docs = self.active
            self.get_status_queries.append("ACTIVE")
        elif "status=FAILED" in url:
            docs = self.failed
            self.get_status_queries.append("FAILED")
        elif "status=SKIPPED" in url:
            docs = self.skipped
            self.get_status_queries.append("SKIPPED")
        else:
            docs = []
        return FakeResp(200, {"documents": list(docs)})

    def post(self, url: str, headers=None, files=None, data=None, timeout=None):
        self.post_urls.append(url)
        self.post_calls.append({"url": url, "headers": headers, "files": files, "data": data, "timeout": timeout})
        if self.on_post is not None:
            return self.on_post(url, headers, files, data)
        form = dict(data or {})
        doc_id = import_docs.document_id(
            form.get("sourceId", ""),
            form.get("sourceCommit", ""),
            form.get("sourcePath", ""),
        )
        self.active.append(self._doc(form, doc_id, "ACTIVE"))
        return FakeResp(202, {"data": {"documentId": doc_id}})

    def fail_on_post(self, url, headers, files, data):
        form = dict(data or {})
        doc_id = import_docs.document_id(
            form.get("sourceId", ""),
            form.get("sourceCommit", ""),
            form.get("sourcePath", ""),
        )
        self.failed.append(self._doc(form, doc_id, "FAILED", "chunk stage failed: truncated"))
        return FakeResp(202, {"data": {"documentId": doc_id}})

    def stall_on_post(self, url, headers, files, data):
        form = dict(data or {})
        doc_id = import_docs.document_id(
            form.get("sourceId", ""),
            form.get("sourceCommit", ""),
            form.get("sourcePath", ""),
        )
        # 202 accepted but never reaches ACTIVE/FAILED -> importer poll times out.
        return FakeResp(202, {"data": {"documentId": doc_id}})

    def skip_on_post(self, url, headers, files, data):
        form = dict(data or {})
        doc_id = import_docs.document_id(
            form.get("sourceId", ""),
            form.get("sourceCommit", ""),
            form.get("sourcePath", ""),
        )
        # 202 accepted, parse returned empty text -> server marks it SKIPPED
        # (graceful skip, not a failure).
        self.skipped.append(self._doc(form, doc_id, "SKIPPED", "parse: empty content after parse"))
        return FakeResp(202, {"data": {"documentId": doc_id}})

    @staticmethod
    def _doc(form: dict, doc_id: str, status: str, last_error: str = "") -> dict:
        return {
            "documentId": doc_id,
            "sourceId": form.get("sourceId", ""),
            "sourcePath": form.get("sourcePath", ""),
            "sourceCommit": form.get("sourceCommit", ""),
            "contentSha256": form.get("sourceSha256", ""),
            "status": status,
            "lastError": last_error,
            "targetIndex": form.get("targetIndex", ""),
            "corpusGeneration": form.get("corpusGeneration", ""),
        }


def stage_go_doc(staging: Path, rel: str = "doc/README.md", body: str = "body") -> Path:
    """Create a file under <staging>/go/<rel> and return the file path."""
    path = staging / "go" / rel
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(body, encoding="utf-8")
    return path


def patch_server(monkeypatch: pytest.MonkeyPatch, fake: FakeServer) -> None:
    monkeypatch.setattr(import_docs.requests, "get", fake.get)
    monkeypatch.setattr(import_docs.requests, "post", fake.post)
    monkeypatch.setattr(import_docs.time, "sleep", lambda *_a, **_k: None)


# ---------------------------------------------------------------------------
# Scenario (a): manifest_document_paths applies include/exclude/allowed_formats,
#                rejects unknown source, rejects empty policy.
# ---------------------------------------------------------------------------


def test_manifest_document_paths_apply_excludes_and_allowed_formats(tmp_path: Path) -> None:
    manifest = tmp_path / "sources.yaml"
    write_manifest(manifest)
    source_dir = tmp_path / "go"
    (source_dir / "doc" / "private").mkdir(parents=True)
    (source_dir / "doc" / "README.md").write_text("keep", encoding="utf-8")
    (source_dir / "doc" / "guide.html").write_text("keep", encoding="utf-8")
    (source_dir / "doc" / "private" / "secret.md").write_text("exclude", encoding="utf-8")
    (source_dir / "doc" / "notes.txt").write_text("exclude", encoding="utf-8")
    (source_dir / "other.md").write_text("exclude", encoding="utf-8")

    files = import_docs.manifest_document_paths("go", source_dir, manifest)

    assert [path.relative_to(source_dir).as_posix() for path in files] == [
        "doc/guide.html",
        "doc/README.md",
    ]


def test_manifest_document_paths_reject_unknown_source(tmp_path: Path) -> None:
    manifest = tmp_path / "sources.yaml"
    write_manifest(manifest)

    with pytest.raises(ValueError, match="unknown corpus source"):
        import_docs.manifest_document_paths("missing", tmp_path, manifest)


def test_manifest_document_paths_reject_empty_policy(tmp_path: Path) -> None:
    manifest = tmp_path / "sources.yaml"
    manifest.write_text(
        f'''schema_version: "1"
generation: "{GENERATION}"
sources:
  - source_id: "go"
    repository_url: "{GO_REPO}"
    source_commit: "{GO_COMMIT}"
    include_paths: []
    exclude_paths: []
    allowed_formats: []
    loader_user: 1
''',
        encoding="utf-8",
    )
    source_dir = tmp_path / "go"
    (source_dir / "doc").mkdir(parents=True)
    (source_dir / "doc" / "README.md").write_text("x", encoding="utf-8")

    with pytest.raises(ValueError, match="incomplete document policy"):
        import_docs.manifest_document_paths("go", source_dir, manifest)


def test_path_matches_aligns_with_go_semantics() -> None:
    # trailing /** subtree, plain directory subtree, * glob are exercised at the
    # path_matches level too; '*' does not cross '/' (Go filepath.Match parity).
    assert import_docs.path_matches("doc/", "doc/README.md") is True
    assert import_docs.path_matches("doc/*.txt", "doc/notes.txt") is True
    assert import_docs.path_matches("doc/*.txt", "doc/sub/notes.txt") is False
    assert import_docs.path_matches("doc/articles", "doc/articles/dep.md") is True


# ---------------------------------------------------------------------------
# Scenario (b): --file must select a manifest-selected path; excluded -> exit 1.
# ---------------------------------------------------------------------------


def test_file_flag_rejects_path_excluded_by_manifest(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    manifest = tmp_path / "sources.yaml"
    write_manifest(manifest)
    staging = tmp_path / "staging"
    stage_go_doc(staging, rel="doc/private/secret.md", body="secret")  # excluded by doc/private
    token_file = tmp_path / "token.txt"
    token_file.write_text("secret-token", encoding="utf-8")

    fake = FakeServer()
    patch_server(monkeypatch, fake)

    assert (
        import_docs.main(
            [
                "--manifest",
                str(manifest),
                "--token-file",
                str(token_file),
                "--staging",
                str(staging),
                "--source",
                "go",
                "--file",
                "doc/private/secret.md",
            ]
        )
        == 1
    )
    assert fake.post_urls == []  # never uploaded


# ---------------------------------------------------------------------------
# Scenario (c): --limit bounds attempts, not successes.
# ---------------------------------------------------------------------------


def test_limit_counts_attempts_not_successes(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    manifest = tmp_path / "sources.yaml"
    write_manifest(manifest)
    staging = tmp_path / "staging"
    stage_go_doc(staging, rel="doc/a.md", body="a")
    stage_go_doc(staging, rel="doc/b.md", body="b")
    token_file = tmp_path / "token.txt"
    token_file.write_text("tok", encoding="utf-8")
    report_path = tmp_path / "report.json"

    fake = FakeServer()
    fake.on_post = lambda url, headers, files, data: FakeResp(500)  # every upload fails
    patch_server(monkeypatch, fake)

    rc = import_docs.main(
        [
            "--manifest",
            str(manifest),
            "--token-file",
            str(token_file),
            "--staging",
            str(staging),
            "--source",
            "go",
            "--limit",
            "1",
            "--report",
            str(report_path),
        ]
    )

    assert rc == 1
    report = json.loads(report_path.read_text(encoding="utf-8"))
    assert report["attempted"] == 1
    assert report["failed"] == 1
    assert report["active"] == 0
    assert len(fake.post_urls) == 1


# ---------------------------------------------------------------------------
# Scenario (d): upload request shape (URL, header, multipart, provenance,
#                sourceUrl, sourceSha256 == raw bytes sha256).
# Scenario (e): fast-upload is never used by the corpus importer.
# ---------------------------------------------------------------------------


def test_upload_request_shape_and_no_fast_upload(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    manifest = tmp_path / "sources.yaml"
    write_manifest(manifest)
    staging = tmp_path / "staging"
    body = "# Go README\n"
    staged_file = stage_go_doc(staging, rel="doc/README.md", body=body)
    token_file = tmp_path / "token.txt"
    token_file.write_text("secret-token-xyz", encoding="utf-8")

    fake = FakeServer()
    patch_server(monkeypatch, fake)

    rc = import_docs.main(
        [
            "--manifest",
            str(manifest),
            "--token-file",
            str(token_file),
            "--staging",
            str(staging),
            "--source",
            "go",
        ]
    )

    assert rc == 0
    assert len(fake.post_calls) == 1
    call = fake.post_calls[0]

    # (d) URL is the dedicated internal corpus entry.
    assert call["url"].endswith("/internal/orchestrator/knowledge-ingest")
    # (d) X-Internal-Token comes from the token file.
    assert call["headers"]["X-Internal-Token"] == "secret-token-xyz"

    form = call["data"]
    # (d) file + userId (=manifest loader_user) + isPublic=true.
    assert "file" in call["files"]
    assert form["userId"] == "1"
    assert form["isPublic"] == "true"
    assert form["orgTag"]  # non-empty corpus org tag

    # (d) six provenance fields.
    assert form["sourceId"] == "go"
    assert form["sourcePath"] == "doc/README.md"
    assert form["sourceCommit"] == GO_COMMIT
    assert form["corpusGeneration"] == GENERATION
    assert form["targetIndex"] == "knowledge_base_v2_bge_m3"
    # (d) sourceUrl = <repository_url>/blob/<commit>/<path>.
    assert form["sourceUrl"] == f"{GO_REPO}/blob/{GO_COMMIT}/doc/README.md"
    # (d) sourceSha256 == sha256 of the raw staging bytes (read from the file so
    # the assertion is independent of the host's newline translation).
    assert form["sourceSha256"] == hashlib.sha256(staged_file.read_bytes()).hexdigest()
    # (d) runId pins this upload to a run so the consumer's run-aware dedup
    # bypasses any stale historical SUCCESS for this file_md5. main generates
    # import-<unix> and threads it into every upload.
    assert re.fullmatch(r"import-\d+", form["runId"])

    # (e) corpus never touches fast-upload (or any /api/v1/upload/* path).
    assert not any("/api/v1/upload/" in u for u in fake.post_urls), fake.post_urls


# ---------------------------------------------------------------------------
# Scenario (f): polling ACTIVE -> active; FAILED -> exit 1; timeout -> exit 1.
# ---------------------------------------------------------------------------


def _run_single_doc(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    fake: FakeServer,
    extra_args: list[str] | None = None,
) -> tuple[int, dict]:
    manifest = tmp_path / "sources.yaml"
    write_manifest(manifest)
    staging = tmp_path / "staging"
    stage_go_doc(staging, rel="doc/README.md", body="body")
    token_file = tmp_path / "token.txt"
    token_file.write_text("tok", encoding="utf-8")
    report_path = tmp_path / "report.json"
    patch_server(monkeypatch, fake)

    args = [
        "--manifest",
        str(manifest),
        "--token-file",
        str(token_file),
        "--staging",
        str(staging),
        "--source",
        "go",
        "--report",
        str(report_path),
    ]
    if extra_args:
        args.extend(extra_args)
    rc = import_docs.main(args)
    return rc, json.loads(report_path.read_text(encoding="utf-8"))


def test_poll_active_counts_as_active(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    fake = FakeServer()  # default: POST -> ACTIVE
    rc, report = _run_single_doc(tmp_path, monkeypatch, fake)

    assert rc == 0
    assert report["active"] == 1
    assert report["failed"] == 0
    assert report["skipped"] == 0
    assert fake.get_count >= 1


def test_poll_failed_yields_exit_1(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    fake = FakeServer()
    fake.on_post = fake.fail_on_post
    rc, report = _run_single_doc(tmp_path, monkeypatch, fake)

    assert rc == 1
    assert report["failed"] == 1
    assert report["active"] == 0
    assert report["files"][0]["status"] == "failed"
    assert report["files"][0]["failure"]  # sanitized lastError surfaced


def test_poll_timeout_yields_exit_1(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    fake = FakeServer()
    fake.on_post = fake.stall_on_post
    rc, report = _run_single_doc(
        tmp_path,
        monkeypatch,
        fake,
        extra_args=["--poll-timeout-seconds", "1", "--poll-interval-seconds", "5"],
    )

    assert rc == 1
    assert report["failed"] == 1
    assert report["files"][0]["status"] == "failed"


# ---------------------------------------------------------------------------
# Scenario (f-skipped): a document the server marked SKIPPED (empty content
#                after parse) is a graceful skip — poll_document returns
#                success and the importer exits 0. The poll must consult ACTIVE
#                then SKIPPED (then FAILED) so the importer discovers the skip.
# ---------------------------------------------------------------------------


def test_poll_document_returns_success_when_server_marks_skipped(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    fake = FakeServer()
    doc_id = import_docs.document_id("go", GO_COMMIT, "content/en/docs/_index.md")
    fake.skipped.append(
        {
            "documentId": doc_id,
            "sourceId": "go",
            "sourcePath": "content/en/docs/_index.md",
            "sourceCommit": GO_COMMIT,
            "contentSha256": "deadbeef",
            "status": "SKIPPED",
            "lastError": "parse: empty content after parse",
            "targetIndex": "knowledge_base_v2_bge_m3",
            "corpusGeneration": GENERATION,
        }
    )
    patch_server(monkeypatch, fake)

    status, failure = import_docs.poll_document(
        "http://127.0.0.1:8081",
        "tok",
        GENERATION,
        doc_id,
        timeout_seconds=1,
        interval_seconds=5,
    )

    # Graceful skip is a success status, not a failure.
    assert status == "skipped"
    assert failure == ""
    # The poll discovers the skip by querying ACTIVE then SKIPPED.
    assert "ACTIVE" in fake.get_status_queries
    assert "SKIPPED" in fake.get_status_queries


def test_poll_skipped_yields_exit_0(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    fake = FakeServer()
    fake.on_post = fake.skip_on_post
    rc, report = _run_single_doc(tmp_path, monkeypatch, fake)

    assert rc == 0
    assert report["skipped"] == 1
    assert report["failed"] == 0
    assert report["active"] == 0
    assert report["files"][0]["status"] == "skipped"


# ---------------------------------------------------------------------------
# Scenario (g): exit codes -- duplicate ACTIVE -> skipped -> 0; preflight
#                errors (staging missing, token missing, manifest parse) -> 2.
# ---------------------------------------------------------------------------


def test_already_active_document_is_skipped_exit_0(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    manifest = tmp_path / "sources.yaml"
    write_manifest(manifest)
    staging = tmp_path / "staging"
    stage_go_doc(staging, rel="doc/README.md", body="body")
    token_file = tmp_path / "token.txt"
    token_file.write_text("tok", encoding="utf-8")
    report_path = tmp_path / "report.json"

    fake = FakeServer()
    # The document is already ACTIVE before this run -> legitimate skip.
    doc_id = import_docs.document_id("go", GO_COMMIT, "doc/README.md")
    fake.active.append(
        {
            "documentId": doc_id,
            "sourceId": "go",
            "sourcePath": "doc/README.md",
            "sourceCommit": GO_COMMIT,
            "status": "ACTIVE",
            "lastError": "",
            "targetIndex": "knowledge_base_v2_bge_m3",
            "corpusGeneration": GENERATION,
        }
    )
    patch_server(monkeypatch, fake)

    rc = import_docs.main(
        [
            "--manifest",
            str(manifest),
            "--token-file",
            str(token_file),
            "--staging",
            str(staging),
            "--source",
            "go",
            "--report",
            str(report_path),
        ]
    )

    assert rc == 0
    report = json.loads(report_path.read_text(encoding="utf-8"))
    assert report["skipped"] == 1
    assert report["active"] == 0
    assert report["attempted"] == 1
    # Already-active docs are not re-uploaded.
    assert fake.post_urls == []


def test_missing_staging_returns_exit_2(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    manifest = tmp_path / "sources.yaml"
    write_manifest(manifest)
    token_file = tmp_path / "token.txt"
    token_file.write_text("tok", encoding="utf-8")
    fake = FakeServer()
    patch_server(monkeypatch, fake)

    rc = import_docs.main(
        [
            "--manifest",
            str(manifest),
            "--token-file",
            str(token_file),
            "--staging",
            str(tmp_path / "does-not-exist"),
            "--source",
            "go",
        ]
    )
    assert rc == 2
    assert fake.post_urls == []


def test_missing_token_file_returns_exit_2(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    manifest = tmp_path / "sources.yaml"
    write_manifest(manifest)
    staging = tmp_path / "staging"
    stage_go_doc(staging, rel="doc/README.md", body="body")
    fake = FakeServer()
    patch_server(monkeypatch, fake)

    rc = import_docs.main(
        [
            "--manifest",
            str(manifest),
            "--token-file",
            str(tmp_path / "no-token.txt"),
            "--staging",
            str(staging),
            "--source",
            "go",
        ]
    )
    assert rc == 2
    assert fake.post_urls == []


def test_unparseable_manifest_returns_exit_2(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    manifest = tmp_path / "sources.yaml"
    manifest.write_text(": : : not yaml [[", encoding="utf-8")
    staging = tmp_path / "staging"
    stage_go_doc(staging, rel="doc/README.md", body="body")
    token_file = tmp_path / "token.txt"
    token_file.write_text("tok", encoding="utf-8")
    fake = FakeServer()
    patch_server(monkeypatch, fake)

    rc = import_docs.main(
        [
            "--manifest",
            str(manifest),
            "--token-file",
            str(token_file),
            "--staging",
            str(staging),
            "--source",
            "go",
        ]
    )
    assert rc == 2
    assert fake.post_urls == []


# ---------------------------------------------------------------------------
# Scenario (h): --report writes the full JSON shape and never leaks the token.
# ---------------------------------------------------------------------------


def test_report_shape_and_token_absence(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    manifest = tmp_path / "sources.yaml"
    write_manifest(manifest)
    staging = tmp_path / "staging"
    stage_go_doc(staging, rel="doc/README.md", body="body")
    token_file = tmp_path / "token.txt"
    token_file.write_text("super-secret-token-12345", encoding="utf-8")
    report_path = tmp_path / "report.json"

    fake = FakeServer()
    patch_server(monkeypatch, fake)

    rc = import_docs.main(
        [
            "--manifest",
            str(manifest),
            "--token-file",
            str(token_file),
            "--staging",
            str(staging),
            "--source",
            "go",
            "--report",
            str(report_path),
        ]
    )

    assert rc == 0
    raw = report_path.read_text(encoding="utf-8")
    assert "super-secret-token-12345" not in raw

    report = json.loads(raw)
    for key in (
        "runId",
        "source",
        "generation",
        "commit",
        "selected",
        "attempted",
        "queued",
        "active",
        "skipped",
        "failed",
        "durationSec",
        "files",
    ):
        assert key in report, f"report missing {key}"

    assert report["source"] == "go"
    assert report["generation"] == GENERATION
    assert report["commit"] == GO_COMMIT
    assert report["selected"] == 1
    assert report["attempted"] == 1
    assert report["queued"] == 1
    assert report["active"] == 1

    # The report's runId is the same import-<unix> value main threaded into the
    # upload form, so an operator can correlate a STAGED/FAILED document back to
    # the exact run that enqueued it.
    assert re.fullmatch(r"import-\d+", report["runId"])
    assert fake.post_calls, "expected at least one upload"
    assert report["runId"] == fake.post_calls[0]["data"]["runId"]

    entry = report["files"][0]
    for key in ("path", "sha256", "md5", "status", "failure"):
        assert key in entry, f"file entry missing {key}"
    assert entry["path"] == "doc/README.md"
    assert entry["status"] == "active"
    assert entry["sha256"] == hashlib.sha256(b"body").hexdigest()


# ---------------------------------------------------------------------------
# Unit coverage: upload_one returns documentId, raises on validation 4xx.
# ---------------------------------------------------------------------------


def test_upload_one_returns_document_id(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    path = tmp_path / "doc" / "README.md"
    path.parent.mkdir(parents=True)
    path.write_text("body", encoding="utf-8")
    fake = FakeServer()
    patch_server(monkeypatch, fake)

    doc_id = import_docs.upload_one(
        "http://127.0.0.1:8081",
        "tok",
        path=path,
        source_id="go",
        source_path="doc/README.md",
        source_commit=GO_COMMIT,
        repository_url=GO_REPO,
        corpus_generation=GENERATION,
        target_index="knowledge_base_v2_bge_m3",
        loader_user=1,
        run_id="import-test",
    )
    assert doc_id == import_docs.document_id("go", GO_COMMIT, "doc/README.md")


def test_upload_one_raises_on_validation_error(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    path = tmp_path / "doc" / "README.md"
    path.parent.mkdir(parents=True)
    path.write_text("body", encoding="utf-8")
    fake = FakeServer()
    fake.on_post = lambda url, headers, files, data: FakeResp(400)
    patch_server(monkeypatch, fake)

    with pytest.raises(Exception):
        import_docs.upload_one(
            "http://127.0.0.1:8081",
            "tok",
            path=path,
            source_id="go",
            source_path="doc/README.md",
            source_commit=GO_COMMIT,
            repository_url=GO_REPO,
            corpus_generation=GENERATION,
            target_index="knowledge_base_v2_bge_m3",
            loader_user=1,
            run_id="import-test",
        )


def test_upload_one_form_carries_run_id(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """The importer must pin every upload to a run so the server enqueues a
    run-scoped task and the consumer's run-aware dedup bypasses any stale
    historical SUCCESS for this file_md5. The run_id the caller passes must
    land verbatim in the multipart form's ``runId`` field."""
    path = tmp_path / "doc" / "README.md"
    path.parent.mkdir(parents=True)
    path.write_text("body", encoding="utf-8")
    fake = FakeServer()
    patch_server(monkeypatch, fake)

    import_docs.upload_one(
        "http://127.0.0.1:8081",
        "tok",
        path=path,
        source_id="go",
        source_path="doc/README.md",
        source_commit=GO_COMMIT,
        repository_url=GO_REPO,
        corpus_generation=GENERATION,
        target_index="knowledge_base_v2_bge_m3",
        loader_user=1,
        run_id="import-1700000000",
    )

    assert len(fake.post_calls) == 1
    form = fake.post_calls[0]["data"]
    assert form["runId"] == "import-1700000000"




def test_upload_one_honors_http_timeout(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """upload_one must pass the configured http_timeout to the requests.post
    call so slow-ES environments do not hit the default 30s ReadTimeout."""
    path = tmp_path / "doc" / "README.md"
    path.parent.mkdir(parents=True)
    path.write_text("body", encoding="utf-8")
    fake = FakeServer()
    patch_server(monkeypatch, fake)

    import_docs.upload_one(
        "http://127.0.0.1:8081",
        "tok",
        path=path,
        source_id="go",
        source_path="doc/README.md",
        source_commit=GO_COMMIT,
        repository_url=GO_REPO,
        corpus_generation=GENERATION,
        target_index="knowledge_base_v2_bge_m3",
        loader_user=1,
        run_id="import-test",
        http_timeout=222,
    )
    assert fake.post_calls and fake.post_calls[0]["timeout"] == 222, fake.post_calls


def test_list_documents_honors_http_timeout(monkeypatch: pytest.MonkeyPatch) -> None:
    """list_documents must pass the configured http_timeout to requests.get."""
    seen: dict[str, object] = {}

    def fake_get(url, headers=None, timeout=None):
        seen["timeout"] = timeout
        return FakeResp(200, {"documents": []})

    monkeypatch.setattr(import_docs.requests, "get", fake_get)
    import_docs.list_documents("http://x", "tok", GENERATION, "ACTIVE", http_timeout=333)
    assert seen.get("timeout") == 333, seen
