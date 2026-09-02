"""Eval run artifacts (plan Task 8.1).

Canonical artifact tree per run:

    eval_results/<run_id>/
      run-manifest.json      # reproducible pins (eval.manifest.RunPin)
      instances.jsonl        # the instances evaluated
      predictions.jsonl      # per-instance predictions
      events.jsonl           # per-instance status transitions
      failures.jsonl         # classified failures (never silently dropped)
      summary.json           # atomic final summary
      environment.txt        # env + machine summary (secrets redacted)
      checksums.sha256       # SHA-256 of every file above (last, after all)

Summary writes are atomic (temp file + rename) so an interrupted run never
leaves a half-written summary.
"""

from __future__ import annotations

import hashlib
import json
import os
import platform
import re
import tempfile
import threading
from collections.abc import Iterable
from pathlib import Path, PurePosixPath, PureWindowsPath
from typing import Any

from eval.harness.redaction import redact_credential_text, redact_credential_value

#: Name of the SHA-256 manifest written at the end of every run (H3).
CHECKSUM_FILENAME = "checksums.sha256"


class RunArtifacts:
    def __init__(self, run_id: str, root: str | Path) -> None:
        self.run_id = run_id
        base_root = Path(root).resolve()
        run_path = _validated_relative_path(run_id, label="run_id")
        if len(run_path.parts) != 1:
            raise ValueError("run_id must name exactly one run directory")
        self.root = (base_root / run_path).resolve()
        if not self.root.is_relative_to(base_root):
            raise ValueError("run_id must stay within the artifact root")
        self.root.mkdir(parents=True, exist_ok=True)
        self._append_lock = threading.Lock()

    def _artifact_path(self, name: str | Path) -> Path:
        relative = _validated_relative_path(name, label="artifact path")
        path = (self.root / relative).resolve()
        if not path.is_relative_to(self.root):
            raise ValueError("artifact path must stay within the run root")
        return path

    def _checksum_manifest_path(self) -> Path:
        lexical_path = self.root / CHECKSUM_FILENAME
        if lexical_path.is_symlink():
            raise ValueError("checksum manifest must not be a symlink")
        return self._artifact_path(CHECKSUM_FILENAME)

    def write(self, name: str, payload: Any) -> Path:
        path = self._artifact_path(name)
        _atomic_write_text(
            path,
            json.dumps(redact_credential_value(payload), indent=2, ensure_ascii=False)
            + "\n",
        )
        return path

    def append_line(self, name: str, record: dict[str, Any]) -> None:
        path = self._artifact_path(name)
        line = json.dumps(redact_credential_value(record), ensure_ascii=False) + "\n"
        with self._append_lock:
            existing = ""
            try:
                with path.open("r", encoding="utf-8") as handle:
                    if os.fstat(handle.fileno()).st_nlink != 1:
                        raise ValueError(
                            "artifact append target must have exactly one hard link"
                        )
                    existing = handle.read()
            except FileNotFoundError:
                pass
            _atomic_write_text(path, existing + line)

    def write_jsonl(self, name: str, records: Iterable[dict[str, Any]]) -> Path:
        """Atomically replace a JSONL artifact in caller-provided order."""

        path = self._artifact_path(name)
        lines = [
            json.dumps(redact_credential_value(record), ensure_ascii=False)
            for record in records
        ]
        _atomic_write_text(path, "\n".join(lines) + ("\n" if lines else ""))
        return path

    def write_manifest(self, manifest: dict[str, Any]) -> Path:
        return self.write("run-manifest.json", manifest)

    def record_instance(self, instance: dict[str, Any]) -> None:
        self.append_line("instances.jsonl", instance)

    def record_prediction(self, prediction: dict[str, Any]) -> None:
        self.append_line("predictions.jsonl", prediction)

    def record_scorer_output(self, name: str, content: str) -> Path:
        """Persist an official scorer's raw output under ``scorer/``.

        The H5 artifact contract requires the run to *save official raw output*,
        and §20.7's canonical tree puts it under ``scorer/``.  Until this
        existed, the official verdict survived only as the summarised
        ``scorer_status`` string inside ``predictions.jsonl``: the harness's own
        paraphrase, truncated to a couple of hundred characters.  The real
        report stayed in ``logs/run_evaluation/``, outside the artifact tree and
        therefore outside ``checksums.sha256``, so nothing pinned the evidence
        the verdict was derived from.

        Content retains its original formatting but credential-shaped values
        are redacted before persistence. Raw evidence is not authority to store
        provider secrets.
        """
        path = self._artifact_path(Path("scorer") / name)
        _atomic_write_text(path, redact_credential_text(content))
        return path

    def record_trace(self, name: str, payload: Any) -> Path:
        """Persist trace evidence under ``traces/`` for the OTel acceptance contract.

        The canonical tree requires ``traces/trace-summary.json`` (what spans the
        run actually produced) and ``traces/span-assertion.json`` (the verdict
        from ``eval.harness.trace_contract``).  Both live under ``traces/`` so
        the recursive :meth:`_iter_artifact_files` pins them in
        ``checksums.sha256`` — unpinned trace evidence is not evidence.

        Mirrors :meth:`record_scorer_output`'s subtree pattern, but takes a
        payload rather than raw text: unlike an official scorer's output, these
        two files are produced by this repository, so there are no foreign bytes
        to preserve verbatim.
        """
        path = self._artifact_path(Path("traces") / name)
        _atomic_write_text(
            path,
            json.dumps(redact_credential_value(payload), indent=2, ensure_ascii=False)
            + "\n",
        )
        return path

    def record_event(self, instance_id: str, status: str, detail: str = "") -> None:
        self.append_line(
            "events.jsonl",
            {"instance_id": instance_id, "status": status, "detail": detail},
        )

    def record_failure(self, instance_id: str, category: str, message: str) -> None:
        self.append_line(
            "failures.jsonl",
            {"instance_id": instance_id, "category": category, "message": message},
        )

    def write_summary(self, summary: dict[str, Any]) -> Path:
        """Atomic summary write: temp file + rename, never half-written."""
        path = self.root / "summary.json"
        fd, tmp = tempfile.mkstemp(dir=self.root, suffix=".tmp")
        try:
            with os.fdopen(fd, "w", encoding="utf-8") as handle:
                json.dump(
                    redact_credential_value(summary),
                    handle,
                    indent=2,
                    ensure_ascii=False,
                )
            os.replace(tmp, path)
        finally:
            if os.path.exists(tmp):
                os.unlink(tmp)
        return path

    def write_environment(self) -> Path:
        """Record stable runtime metadata without serializing process env.

        Model and endpoint pins belong in the manifest.  Copying the ambient
        process environment adds no reproducibility value and creates a broad
        secret/configuration disclosure surface, even when known key values
        are redacted.
        """
        payload = {
            "platform": platform.platform(),
            "python": platform.python_version(),
            "machine": platform.machine(),
            "processor": platform.processor(),
        }
        return self.write("environment.txt", payload)

    def _iter_artifact_files(self) -> list[Path]:
        """Every artifact file in the run tree, recursively, sorted by rel path.

        ``checksums.sha256`` is excluded (it cannot pin itself).  Recursion is
        required by the artifact contract: the canonical tree contains ``scorer/``
        and ``traces/`` subdirectories whose contents are release evidence, so
        a top-level-only pin would leave the official scorer output and trace
        assertions unhashed.
        """
        files: list[Path] = []
        for fpath in self.root.rglob("*"):
            if fpath.is_dir():
                continue
            if fpath.relative_to(self.root).as_posix() == CHECKSUM_FILENAME:
                continue
            if not fpath.resolve().is_relative_to(self.root):
                raise ValueError(
                    f"artifact path must stay within the run root: {fpath}"
                )
            files.append(fpath)
        return sorted(files, key=lambda p: p.relative_to(self.root).as_posix())

    def write_checksums(self) -> Path:
        """Write ``checksums.sha256`` pinning every artifact file in the tree.

        H3 artifact finalization ends with a SHA-256 pin of
        every artifact so a later run can verify the tree is intact.  Paths are
        recorded relative to the run root with forward slashes so the manifest
        is platform-independent.
        """
        path = self._checksum_manifest_path()
        lines: list[str] = []
        for fpath in self._iter_artifact_files():
            rel = fpath.relative_to(self.root).as_posix()
            sha = hashlib.sha256(fpath.read_bytes()).hexdigest()
            lines.append(f"{sha}  {rel}")
        fd, temporary = tempfile.mkstemp(dir=self.root, suffix=".checksums.tmp")
        try:
            with os.fdopen(fd, "w", encoding="utf-8") as handle:
                handle.write("\n".join(lines) + "\n")
            os.replace(temporary, path)
        finally:
            if os.path.exists(temporary):
                os.unlink(temporary)
        return path

    def verify_checksums(self) -> list[str]:
        """Verify the artifact tree against ``checksums.sha256``.

        Returns a list of human-readable problems; an empty list means the
        tree matches its pin exactly.  The artifact contract requires generating
        *and* verifying the checksum file — a pin nobody checks is not
        evidence.  Detects three failure modes:

        * ``missing:`` a pinned file is gone,
        * ``mismatch:`` a pinned file's bytes changed,
        * ``unpinned:`` a file exists in the tree but is absent from the pin.
        """
        try:
            path = self._checksum_manifest_path()
        except ValueError:
            return [f"unsafe:{CHECKSUM_FILENAME}"]
        if not path.exists():
            return [f"missing:{CHECKSUM_FILENAME}"]

        problems: list[str] = []
        pinned: dict[str, tuple[str, str]] = {}
        for line in path.read_text(encoding="utf-8").splitlines():
            if not line.strip():
                continue
            digest, _, rel = line.partition("  ")
            if not rel:
                problems.append(f"malformed:{line!r}")
                continue
            if re.fullmatch(r"[0-9a-f]{64}", digest) is None:
                problems.append(f"malformed-digest:{rel}")
                continue
            try:
                self._artifact_path(rel)
            except ValueError:
                problems.append(f"unsafe:{rel}")
                continue
            canonical_rel = _validated_relative_path(
                rel, label="artifact path"
            ).as_posix()
            canonical_key = _manifest_path_key(canonical_rel)
            if canonical_key in pinned:
                problems.append(f"duplicate:{canonical_rel}")
                continue
            pinned[canonical_key] = (canonical_rel, digest)

        for rel, digest in sorted(pinned.values()):
            fpath = self._artifact_path(rel)
            if not fpath.exists():
                problems.append(f"missing:{rel}")
                continue
            actual = hashlib.sha256(fpath.read_bytes()).hexdigest()
            if actual != digest:
                problems.append(f"mismatch:{rel}")

        for fpath in self._iter_artifact_files():
            rel = fpath.relative_to(self.root).as_posix()
            if _manifest_path_key(rel) not in pinned:
                problems.append(f"unpinned:{rel}")

        return problems


def _validated_relative_path(value: str | Path, *, label: str) -> Path:
    """Return one canonical relative path under both Windows and POSIX rules."""
    raw = str(value)
    normalized = raw.replace("\\", "/")
    posix = PurePosixPath(normalized)
    windows = PureWindowsPath(raw)
    parts = tuple(part for part in posix.parts if part not in ("", "."))
    if (
        not raw
        or "\x00" in raw
        or posix.is_absolute()
        or windows.is_absolute()
        or bool(windows.drive)
        or not parts
        or any(part == ".." or ":" in part for part in parts)
    ):
        raise ValueError(f"{label} must be a safe relative path")
    return Path(*parts)


def _manifest_path_key(relative_path: str) -> str:
    """Return the platform-canonical key used for manifest path uniqueness."""
    return os.path.normcase(relative_path).replace("\\", "/")


def _atomic_write_text(path: Path, content: str) -> None:
    """Replace one artifact entry without modifying a pre-existing shared inode."""
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(dir=path.parent, suffix=".tmp")
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            handle.write(content)
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)
