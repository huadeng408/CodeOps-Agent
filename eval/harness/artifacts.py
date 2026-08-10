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
import tempfile
from pathlib import Path
from typing import Any

#: Name of the SHA-256 manifest written at the end of every run (H3).
CHECKSUM_FILENAME = "checksums.sha256"


class RunArtifacts:
    def __init__(self, run_id: str, root: str | Path) -> None:
        self.run_id = run_id
        self.root = Path(root) / run_id
        self.root.mkdir(parents=True, exist_ok=True)

    def write(self, name: str, payload: Any) -> Path:
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(payload, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
        return path

    def append_line(self, name: str, record: dict[str, Any]) -> None:
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        with path.open("a", encoding="utf-8") as handle:
            handle.write(json.dumps(record, ensure_ascii=False) + "\n")

    def write_manifest(self, manifest: dict[str, Any]) -> Path:
        return self.write("run-manifest.json", manifest)

    def record_instance(self, instance: dict[str, Any]) -> None:
        self.append_line("instances.jsonl", instance)

    def record_prediction(self, prediction: dict[str, Any]) -> None:
        self.append_line("predictions.jsonl", prediction)

    def record_scorer_output(self, name: str, content: str) -> Path:
        """Persist an official scorer's raw output under ``scorer/``.

        H5 (design map §20.6.1) requires the run to *save official raw output*,
        and §20.7's canonical tree puts it under ``scorer/``.  Until this
        existed, the official verdict survived only as the summarised
        ``scorer_status`` string inside ``predictions.jsonl``: the harness's own
        paraphrase, truncated to a couple of hundred characters.  The real
        report stayed in ``logs/run_evaluation/``, outside the artifact tree and
        therefore outside ``checksums.sha256``, so nothing pinned the evidence
        the verdict was derived from.

        Content is written verbatim rather than re-serialised, so the stored
        bytes are what the official harness actually emitted.
        """
        path = self.root / "scorer" / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content, encoding="utf-8")
        return path

    def record_trace(self, name: str, payload: Any) -> Path:
        """Persist trace evidence under ``traces/`` (design map §20.6.4/§20.7).

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
        path = self.root / "traces" / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(
            json.dumps(payload, indent=2, ensure_ascii=False) + "\n", encoding="utf-8"
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
                json.dump(summary, handle, indent=2, ensure_ascii=False)
            os.replace(tmp, path)
        finally:
            if os.path.exists(tmp):
                os.unlink(tmp)
        return path

    def write_environment(self, secret_keys: tuple[str, ...] = ("API_KEY", "SECRET", "TOKEN")) -> Path:
        env = {}
        for key, value in os.environ.items():
            if any(token in key.upper() for token in secret_keys):
                env[key] = "<redacted>"
            else:
                env[key] = value
        payload = {
            "platform": platform.platform(),
            "python": platform.python_version(),
            "environment": env,
        }
        return self.write("environment.txt", payload)

    def _iter_artifact_files(self) -> list[Path]:
        """Every artifact file in the run tree, recursively, sorted by rel path.

        ``checksums.sha256`` is excluded (it cannot pin itself).  Recursion is
        required by design map §20.7: the canonical tree contains ``scorer/``
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
            files.append(fpath)
        return sorted(files, key=lambda p: p.relative_to(self.root).as_posix())

    def write_checksums(self) -> Path:
        """Write ``checksums.sha256`` pinning every artifact file in the tree.

        H3 (design map §20.7): run finalization ends with a SHA-256 pin of
        every artifact so a later run can verify the tree is intact.  Paths are
        recorded relative to the run root with forward slashes so the manifest
        is platform-independent.
        """
        path = self.root / CHECKSUM_FILENAME
        lines: list[str] = []
        for fpath in self._iter_artifact_files():
            rel = fpath.relative_to(self.root).as_posix()
            sha = hashlib.sha256(fpath.read_bytes()).hexdigest()
            lines.append(f"{sha}  {rel}")
        path.write_text("\n".join(lines) + "\n", encoding="utf-8")
        return path

    def verify_checksums(self) -> list[str]:
        """Verify the artifact tree against ``checksums.sha256``.

        Returns a list of human-readable problems; an empty list means the
        tree matches its pin exactly.  Design map §20.7 requires generating
        *and* verifying the checksum file — a pin nobody checks is not
        evidence.  Detects three failure modes:

        * ``missing:`` a pinned file is gone,
        * ``mismatch:`` a pinned file's bytes changed,
        * ``unpinned:`` a file exists in the tree but is absent from the pin.
        """
        path = self.root / CHECKSUM_FILENAME
        if not path.exists():
            return [f"missing:{CHECKSUM_FILENAME}"]

        problems: list[str] = []
        pinned: dict[str, str] = {}
        for line in path.read_text(encoding="utf-8").splitlines():
            if not line.strip():
                continue
            digest, _, rel = line.partition("  ")
            if not rel:
                problems.append(f"malformed:{line!r}")
                continue
            pinned[rel] = digest

        for rel, digest in sorted(pinned.items()):
            fpath = self.root / rel
            if not fpath.exists():
                problems.append(f"missing:{rel}")
                continue
            actual = hashlib.sha256(fpath.read_bytes()).hexdigest()
            if actual != digest:
                problems.append(f"mismatch:{rel}")

        for fpath in self._iter_artifact_files():
            rel = fpath.relative_to(self.root).as_posix()
            if rel not in pinned:
                problems.append(f"unpinned:{rel}")

        return problems
