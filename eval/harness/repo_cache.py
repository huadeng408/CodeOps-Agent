"""Local git mirrors so an N-instance run does not clone the same repo N times.

``eval/benchmarks/swebench.py`` cloned the full repository from GitHub for every
instance.  Its comment explains *why* it is a full clone (an old ``base_commit``
is unreachable from a shallow clone of the default branch) and that reasoning is
correct — but it made the clone cost recur per instance.  For astropy that is
60–90s each, so a 20-instance run spent 20–30 minutes on 20 byte-identical
clones, and a paired two-arm experiment doubled it.

This module keeps one bare mirror per repository and creates each per-instance
workspace from it with ``--reference``, so object storage is shared and the
network is touched once per repo instead of once per instance.

Design constraints this obeys:

* **A cache failure must never fail the run.**  Every entry point degrades to
  the original full clone.  A benchmark result must not depend on whether a
  cache directory happened to be writable.
* **The workspace must be indistinguishable from a full clone.**  ``--dissociate``
  copies the borrowed objects into the workspace, so it keeps working if the
  mirror is later deleted, and ``git diff HEAD`` — which is how the patch is
  captured — behaves exactly as before.  Sharing objects would be faster still,
  but a workspace whose history silently depends on an external directory is a
  trap for anyone who later archives the artifact.
* **Concurrency-safe.**  Instances may run in parallel, so mirror creation and
  update take an exclusive lock per repo.
"""

from __future__ import annotations

import contextlib
import os
import shutil
import subprocess
import time
from dataclasses import dataclass
from pathlib import Path

#: Where mirrors live unless overridden.  Kept outside the repository so a
#: ``git clean`` in the project tree cannot silently delete gigabytes of cache.
DEFAULT_CACHE_ENV = "LOCALCODE_REPO_CACHE"
DEFAULT_CACHE_DIR = Path.home() / ".cache" / "localcode" / "repo-mirrors"

#: How long to wait for another process's mirror operation before giving up and
#: falling back to a direct clone.  Long enough for a real astropy mirror fetch.
LOCK_TIMEOUT_SECONDS = 900

CLONE_TIMEOUT_SECONDS = 1800
CHECKOUT_TIMEOUT_SECONDS = 300


@dataclass(frozen=True)
class CloneOutcome:
    """What actually happened, so callers can log and tests can assert."""

    workspace: str
    used_mirror: bool
    mirror_created: bool
    fallback_reason: str = ""

    @property
    def degraded(self) -> bool:
        return not self.used_mirror


def cache_root(explicit: str | os.PathLike[str] | None = None) -> Path:
    """Resolve the mirror cache directory."""
    if explicit is not None:
        return Path(explicit)
    from_env = os.environ.get(DEFAULT_CACHE_ENV, "").strip()
    if from_env:
        return Path(from_env)
    return DEFAULT_CACHE_DIR


def _mirror_dir(root: Path, repo: str) -> Path:
    """Mirror path for ``owner/name``.

    The slash is replaced rather than nested so the cache stays one flat level
    and cannot be confused with a working tree.
    """
    return root / (repo.replace("/", "__") + ".git")


def _run(
    argv: list[str],
    *,
    timeout: int,
    cwd: str | None = None,
) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        argv,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        timeout=timeout,
        cwd=cwd,
    )


@contextlib.contextmanager
def _repo_lock(mirror: Path, timeout: int = LOCK_TIMEOUT_SECONDS):
    """Exclusive per-repo lock via atomic directory creation.

    ``os.mkdir`` is atomic on both NTFS and POSIX, which makes it a portable
    lock primitive without a dependency.  Yields ``True`` when the lock was
    acquired and ``False`` on timeout; the caller then degrades rather than
    corrupting a mirror another process is writing.
    """
    lock = mirror.with_suffix(".lock")
    lock.parent.mkdir(parents=True, exist_ok=True)
    deadline = time.monotonic() + timeout
    acquired = False
    while time.monotonic() < deadline:
        try:
            lock.mkdir()
            acquired = True
            break
        except FileExistsError:
            time.sleep(0.5)
    try:
        yield acquired
    finally:
        if acquired:
            with contextlib.suppress(OSError):
                lock.rmdir()


def ensure_mirror(
    repo: str,
    *,
    cache_dir: str | os.PathLike[str] | None = None,
    update: bool = False,
) -> tuple[Path | None, bool, str]:
    """Ensure a bare mirror of *repo* exists.

    Returns ``(mirror_path, created, failure_reason)``.  ``mirror_path`` is
    ``None`` when no usable mirror could be produced, and ``failure_reason``
    then explains why so the caller can record the degradation instead of
    silently losing the optimisation.
    """
    root = cache_root(cache_dir)
    mirror = _mirror_dir(root, repo)

    if mirror.is_dir() and (mirror / "HEAD").is_file():
        if not update:
            return mirror, False, ""
        with _repo_lock(mirror) as got_lock:
            if not got_lock:
                # Another process is working on it; a slightly stale mirror is
                # still correct for a pinned base_commit, so use it as-is.
                return mirror, False, ""
            result = _run(
                ["git", "--git-dir", str(mirror), "remote", "update", "--prune"],
                timeout=CLONE_TIMEOUT_SECONDS,
            )
            if result.returncode != 0:
                # A stale mirror is usually still fine: base_commit is historical.
                return mirror, False, ""
        return mirror, False, ""

    with _repo_lock(mirror) as got_lock:
        if not got_lock:
            return None, False, "timed out waiting for another process's mirror lock"
        # Re-check under the lock: another process may have finished meanwhile.
        if mirror.is_dir() and (mirror / "HEAD").is_file():
            return mirror, False, ""
        staging = mirror.with_suffix(".partial")
        with contextlib.suppress(Exception):
            if staging.exists():
                _force_rmtree(staging)
        try:
            staging.parent.mkdir(parents=True, exist_ok=True)
            result = _run(
                [
                    "git", "clone", "--mirror",
                    f"https://github.com/{repo}.git",
                    str(staging),
                ],
                timeout=CLONE_TIMEOUT_SECONDS,
            )
            if result.returncode != 0:
                _force_rmtree(staging)
                return None, False, f"mirror clone failed: {result.stderr[-300:]}"
            # Publish atomically so a killed process never leaves a half-mirror
            # that later runs would treat as usable.
            staging.replace(mirror)
            return mirror, True, ""
        except subprocess.TimeoutExpired:
            _force_rmtree(staging)
            return None, False, "mirror clone timed out"
        except Exception as exc:  # noqa: BLE001 - degrade, never fail the run
            _force_rmtree(staging)
            return None, False, f"mirror clone error: {type(exc).__name__}: {exc}"


def clone_at_commit(
    repo: str,
    base_commit: str,
    workspace: str | os.PathLike[str],
    *,
    cache_dir: str | os.PathLike[str] | None = None,
    use_cache: bool = True,
    update_mirror: bool = False,
) -> CloneOutcome:
    """Materialise *repo* at *base_commit* into *workspace*.

    Uses a local mirror when one can be produced, and falls back to a direct
    full clone otherwise.  The resulting workspace is a standalone repository in
    both paths — see the module docstring on ``--dissociate``.
    """
    workspace = str(workspace)
    os.makedirs(workspace, exist_ok=True)

    mirror: Path | None = None
    created = False
    reason = ""
    if use_cache:
        mirror, created, reason = ensure_mirror(
            repo, cache_dir=cache_dir, update=update_mirror
        )

    if mirror is not None:
        result = _run(
            [
                "git", "clone",
                "--reference", str(mirror),
                "--dissociate",
                f"https://github.com/{repo}.git",
                workspace,
            ],
            timeout=CLONE_TIMEOUT_SECONDS,
        )
        if result.returncode == 0:
            checkout = _run(
                ["git", "-C", workspace, "checkout", base_commit],
                timeout=CHECKOUT_TIMEOUT_SECONDS,
            )
            if checkout.returncode == 0:
                return CloneOutcome(
                    workspace=workspace, used_mirror=True, mirror_created=created
                )
            # The mirror may predate base_commit; refresh once, then retry.
            refreshed, _, _ = ensure_mirror(repo, cache_dir=cache_dir, update=True)
            if refreshed is not None:
                fetch = _run(
                    ["git", "-C", workspace, "fetch", "--all", "--tags"],
                    timeout=CLONE_TIMEOUT_SECONDS,
                )
                if fetch.returncode == 0:
                    retry = _run(
                        ["git", "-C", workspace, "checkout", base_commit],
                        timeout=CHECKOUT_TIMEOUT_SECONDS,
                    )
                    if retry.returncode == 0:
                        return CloneOutcome(
                            workspace=workspace,
                            used_mirror=True,
                            mirror_created=created,
                        )
            reason = f"base_commit {base_commit[:12]} not reachable via mirror"
        else:
            reason = f"reference clone failed: {result.stderr[-300:]}"
        # Clear the failed attempt so the fallback starts from an empty dir.
        _reset_dir(workspace)

    return _direct_clone(repo, base_commit, workspace, reason or "cache disabled")


def _force_rmtree(path: str | Path) -> None:
    """Remove *path* even when it contains read-only files.

    Git marks objects in ``.git/objects`` read-only, and on Windows
    ``shutil.rmtree`` cannot unlink a read-only file.  With
    ``ignore_errors=True`` it fails *silently* and leaves the tree partially
    populated — which then makes the next ``git clone`` refuse with "destination
    path already exists and is not an empty directory".  The fallback clone that
    exists precisely to rescue a failed reference clone was therefore guaranteed
    to fail after one.  Observed on a real astropy clone, not hypothesised.
    """

    def _clear_readonly(func, target, _exc_info):
        with contextlib.suppress(Exception):
            os.chmod(target, 0o700)
            func(target)

    if not os.path.exists(path):
        return
    shutil.rmtree(path, onerror=_clear_readonly)


def _reset_dir(path: str) -> None:
    """Empty *path* so a subsequent ``git clone`` will accept it."""
    _force_rmtree(path)
    os.makedirs(path, exist_ok=True)


def _direct_clone(
    repo: str, base_commit: str, workspace: str, reason: str
) -> CloneOutcome:
    """The original behaviour: full clone straight from GitHub.

    Raises on failure, because at this point there is no remaining fallback and
    a silent empty workspace is what made every captured patch empty before.
    """
    result = _run(
        ["git", "clone", f"https://github.com/{repo}.git", workspace],
        timeout=CLONE_TIMEOUT_SECONDS,
    )
    if result.returncode != 0:
        raise RuntimeError(
            f"git clone failed for {repo}: {result.stderr[-500:]}"
        )
    checkout = _run(
        ["git", "-C", workspace, "checkout", base_commit],
        timeout=CHECKOUT_TIMEOUT_SECONDS,
    )
    if checkout.returncode != 0:
        raise RuntimeError(
            f"git checkout {base_commit} failed for {repo}: "
            f"{checkout.stderr[-500:]}"
        )
    return CloneOutcome(
        workspace=workspace,
        used_mirror=False,
        mirror_created=False,
        fallback_reason=reason,
    )
