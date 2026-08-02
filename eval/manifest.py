"""Reproducible eval run manifests (plan Task 7.1).

A run manifest pins everything needed to reproduce an eval run: dataset
revision/hash/license/scorer, git SHA + dirty-hash, model/revision, prompt
hash, tool policy, budget, image digest and corpus/index versions.

The manifest is machine-readable and versioned; a floating revision, a
missing hash, or a benchmark/production path overlap is a hard failure.
"""

from __future__ import annotations

import hashlib
import json
import re
import subprocess
from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import Any

# Allowed dataset license identifiers (SPDX-ish). Anything else blocks a run.
ALLOWED_LICENSES = {
    "CC-BY-4.0",
    "CC-BY-SA-4.0",
    "MIT",
    "Apache-2.0",
    "BSD-3-Clause",
    "PSF-2.0",
    "GPL-2.0-only",
    "GPL-3.0-only",
    "PostgreSQL",
    "CC0-1.0",
}

COMMIT_RE = re.compile(r"^[0-9a-f]{7,40}$")
HASH_RE = re.compile(r"^[0-9a-f]{64}$")


@dataclass(frozen=True)
class DatasetPin:
    name: str
    revision: str
    sha256: str
    license_spdx: str
    scorer: str
    source_url: str = ""

    def validate(self) -> list[str]:
        issues: list[str] = []
        if not COMMIT_RE.match(self.revision):
            issues.append(f"{self.name}: revision {self.revision!r} is not a pinned commit")
        if not HASH_RE.match(self.sha256):
            issues.append(f"{self.name}: sha256 {self.sha256!r} is not a 64-char hex digest")
        if self.license_spdx not in ALLOWED_LICENSES:
            issues.append(f"{self.name}: license {self.license_spdx!r} not in allowlist")
        if not self.scorer:
            issues.append(f"{self.name}: scorer required")
        return issues


@dataclass(frozen=True)
class RunPin:
    dataset: DatasetPin
    git_sha: str
    git_dirty_hash: str
    model: str
    model_revision: str
    prompt_hash: str
    tool_policy: str
    budget_tokens: int
    budget_cost: float
    image_digest: str = ""
    corpus_generation: str = ""
    index_alias: str = ""

    def validate(self) -> list[str]:
        issues = self.dataset.validate()
        if not COMMIT_RE.match(self.git_sha):
            issues.append(f"git_sha {self.git_sha!r} is not a commit")
        if self.git_dirty_hash and not HASH_RE.match(self.git_dirty_hash):
            issues.append(f"git_dirty_hash {self.git_dirty_hash!r} is not a 64-char hex digest")
        if not self.model:
            issues.append("model required")
        if not self.prompt_hash:
            issues.append("prompt_hash required")
        if self.budget_tokens <= 0:
            issues.append("budget_tokens must be positive")
        return issues


def git_head(repo_root: str | Path = ".") -> str:
    """Return the current HEAD commit (short) of the repo."""
    out = subprocess.run(
        ["git", "-C", str(repo_root), "rev-parse", "--short", "HEAD"],
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        check=True,
    )
    return out.stdout.strip()


def git_dirty_hash(repo_root: str | Path = ".") -> str:
    """Hash of the working-tree diff; all-zero when clean."""
    out = subprocess.run(
        ["git", "-C", str(repo_root), "diff", "--no-ext-diff"],
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
    )
    diff = out.stdout
    if not diff:
        return "0" * 64
    return hashlib.sha256(diff.encode("utf-8")).hexdigest()


def prompt_hash(prompt: str) -> str:
    return hashlib.sha256(prompt.encode("utf-8")).hexdigest()


def load_manifest(path: str | Path) -> RunPin:
    """Load and validate a run manifest JSON; raises ValueError on issues."""
    payload = json.loads(Path(path).read_text(encoding="utf-8"))
    dataset = DatasetPin(**payload["dataset"])
    run = RunPin(
        dataset=dataset,
        git_sha=payload.get("git_sha", ""),
        git_dirty_hash=payload.get("git_dirty_hash", ""),
        model=payload.get("model", ""),
        model_revision=payload.get("model_revision", ""),
        prompt_hash=payload.get("prompt_hash", ""),
        tool_policy=payload.get("tool_policy", ""),
        budget_tokens=int(payload.get("budget_tokens", 0)),
        budget_cost=float(payload.get("budget_cost", 0.0)),
        image_digest=payload.get("image_digest", ""),
        corpus_generation=payload.get("corpus_generation", ""),
        index_alias=payload.get("index_alias", ""),
    )
    issues = run.validate()
    if issues:
        raise ValueError("manifest invalid:\n  - " + "\n  - ".join(issues))
    return run


def write_manifest(run: RunPin, path: str | Path) -> None:
    out = Path(path)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(asdict(run), indent=2, ensure_ascii=False) + "\n", encoding="utf-8")


def check_path_overlap(benchmark_path: str, production_path: str) -> bool:
    """Detect benchmark/production path overlap (contamination guard).

    Returns True when the benchmark path is inside the production path or
    vice versa — such an overlap must block a run.
    """
    b = Path(benchmark_path).resolve()
    p = Path(production_path).resolve()
    try:
        b.relative_to(p)
        return True
    except ValueError:
        pass
    try:
        p.relative_to(b)
        return True
    except ValueError:
        pass
    return False
