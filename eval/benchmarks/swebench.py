"""
SWE-bench Verified adapter for the Code Agent evaluation framework.

This module:
1. Loads SWE-bench Verified instances (with synthetic fallback for pipeline testing).
2. Runs each instance through the AgentAdapter (imported from eval.adapter).
3. Produces predictions.jsonl for official SWE-bench scoring.

Usage:
    # Dry-run with synthetic instances (no Docker/real repos needed)
    python -m eval.benchmarks.swebench --dry-run

    # Full run with real SWE-bench instances (requires datasets + Docker)
    python -m eval.benchmarks.swebench --max-instances 10

    # Smoke test (1 synthetic instance, verify pipeline)
    python -m eval.benchmarks.swebench --smoke-test

    # ===================================================================
    # ACTUAL EVALUATION (manual step — user runs this after predictions):
    # ===================================================================
    # python -m swebench.harness.run_evaluation \
    #     --dataset_name princeton-nlp/SWE-bench_Verified \
    #     --predictions_path predictions.jsonl \
    #     --max_workers 4 \
    #     --run_id code-agent-test
    #
    # Requirements for the evaluation step:
    #   - Docker installed and running
    #   - pip install swebench
    #   - ~120 GB free disk space (Docker images + repos)
    #   - The run takes 30-120 minutes depending on instance count and hardware
    # ===================================================================

Design notes:
  - eval/adapter.py is created by the core agent in parallel. This module
    imports from it gracefully with a fallback when the adapter is not yet
    available (e.g. when running smoke tests standalone).
  - The dry-run mode uses lightweight synthetic instances that exercise the
    full pipeline without Docker or external network access.
  - Each instance is run in an isolated temporary directory. The agent's
    working directory is set to a fresh git clone (or synthetic repo) so
    tool invocations (Read, Edit, Bash, etc.) operate on the target codebase.
  - The model_patch is captured via `git diff` after the agent finishes.
"""

from __future__ import annotations

import argparse
import json
import os
import platform
import shlex
import shutil
import subprocess
import sys
import tempfile
import time
import uuid
from dataclasses import dataclass, field, replace
from pathlib import Path
from typing import Any, Protocol, Sequence

# The harness owns the scorer contract; the adapter implements it.  Verified
# acyclic: eval/harness/ imports nothing from eval/benchmarks/.
from eval.harness.runner import SCORER_RAW_OUTPUT_KEY

#: Trace capabilities this benchmark exercises (see
#: ``eval.harness.trace_contract.ALL_CAPABILITIES``).  Solving a SWE-bench
#: instance means reading and patching an already-checked-out repository: there
#: is no retrieval and no reranking anywhere in the path, so requiring
#: ``rag.retrieve``/``embedding`` spans of this benchmark would make the trace
#: contract's PASS unreachable and its verdict uninformative.  Declared here,
#: next to the implementation that either does or does not retrieve, rather
#: than by whoever reports the results.
TRACE_CAPABILITIES: tuple[str, ...] = ()

# ---------------------------------------------------------------------------
# Graceful import from eval.adapter (created in parallel by core agent).
# Falls back to local definitions when eval/ is a standalone benchmark harness.
# ---------------------------------------------------------------------------
try:
    from eval.adapter import AgentAdapter, EvalInstance, EvalResult
except ImportError:
    # Fallback definitions — kept compatible with the protocol defined in
    # eval/adapter.py so the core driver can plug in seamlessly later.
    @dataclass
    class EvalInstance:
        """A single evaluation instance (one SWE-bench task)."""

        instance_id: str
        task_description: str
        metadata: dict = field(default_factory=dict)

    @dataclass
    class EvalResult:
        """Result from running one evaluation instance."""

        instance_id: str = ""
        model_patch: str = ""
        answer: str = ""
        cost: float = 0.0
        tokens_in: int = 0
        tokens_out: int = 0
        trace_id: str = ""
        error: str = ""

    class AgentAdapter(Protocol):
        """Protocol for an agent that can solve evaluation instances.

        The headless driver (eval/driver.py) instantiates an AgentAdapter
        and calls solve_instance() for each EvalInstance.
        """

        def solve_instance(
            self, instance: EvalInstance, working_dir: str, **kwargs: Any
        ) -> EvalResult: ...


# AgentBenchmark base class (unified prepare -> solve -> score lifecycle).
# Imported at module level here — eval.benchmarks.base only imports
# eval.retrieval.metrics at runtime, so there is no import cycle.
from eval.benchmarks.base import AgentBenchmark


# ---------------------------------------------------------------------------
# Synthetic SWE-bench instances — used when the real dataset is unavailable
# or when running --dry-run / --smoke-test.
# ---------------------------------------------------------------------------

SYNTHETIC_INSTANCES: list[dict[str, Any]] = [
    {
        "instance_id": "synthetic__django-1234",
        "repo": "https://github.com/django/django.git",
        "base_commit": "abc123def456",
        "issue_title": "Fix ValueError when parsing empty query parameters in QueryDict",
        "issue_body": (
            "## Description\n\n"
            "When a user passes an empty query string to `QueryDict`, "
            "the parser raises an unhandled `ValueError` instead of returning "
            "an empty `QueryDict` instance.\n\n"
            "## Steps to Reproduce\n\n"
            "```python\n"
            "from django.http import QueryDict\n"
            "qd = QueryDict('')\n"
            "# ValueError: not enough values to unpack (expected 2, got 1)\n"
            "```\n\n"
            "## Expected Behavior\n\n"
            "`QueryDict('')` should return an empty QueryDict without raising.\n\n"
            "## Affected File\n\n"
            "`django/http/request.py` in the `QueryDict.__init__` method.\n\n"
            "## Proposed Fix\n\n"
            "Add a guard clause at the top of `__init__` to return early "
            "when `query_string` is empty or only whitespace."
        ),
        "hints_to_generated_patch": (
            "In `QueryDict.__init__`, check for an empty or whitespace-only "
            "`query_string` parameter and return early (call `super().__init__()` "
            "with empty data) before the parsing loop that iterates over "
            "`query_string.split('&')`."
        ),
        "test_patch": (
            "diff --git a/tests/httpwrappers/tests.py b/tests/httpwrappers/tests.py\n"
            "--- a/tests/httpwrappers/tests.py\n"
            "+++ b/tests/httpwrappers/tests.py\n"
            "@@ -1,3 +1,8 @@\n"
            " from django.http import QueryDict\n\n"
            "+def test_empty_query_string():\n"
            "+    qd = QueryDict('')\n"
            "+    assert len(qd) == 0\n"
            "+    qd = QueryDict('   ')\n"
            "+    assert len(qd) == 0\n"
        ),
    },
    {
        "instance_id": "synthetic__flask-5678",
        "repo": "https://github.com/pallets/flask.git",
        "base_commit": "def789abc012",
        "issue_title": "KeyError when accessing request.json on empty body with charset=utf-8",
        "issue_body": (
            "## Description\n\n"
            "When a client sends a POST request with `Content-Type: application/json; "
            "charset=utf-8` and an empty body, `request.json` raises a `KeyError` "
            "instead of returning `None`.\n\n"
            "## Steps to Reproduce\n\n"
            "```python\n"
            "from flask import Flask, request\n"
            "app = Flask(__name__)\n\n"
            "@app.route('/test', methods=['POST'])\n"
            "def test():\n"
            "    data = request.json  # KeyError on empty body\n"
            "    return str(data)\n\n"
            "with app.test_client() as c:\n"
            "    c.post('/test', headers={'Content-Type': 'application/json; charset=utf-8'})\n"
            "```\n\n"
            "## Expected Behavior\n\n"
            "`request.json` should return `None` for an empty body rather than raising.\n\n"
            "## Affected File\n\n"
            "`flask/wrappers/json.py` in the JSON body parsing logic."
        ),
        "hints_to_generated_patch": (
            "In the JSON body parser, catch `KeyError` and return `None` "
            "when the body is empty. Also check `content_length == 0` early."
        ),
        "test_patch": (
            "diff --git a/tests/test_json.py b/tests/test_json.py\n"
            "--- a/tests/test_json.py\n"
            "+++ b/tests/test_json.py\n"
            "@@ -1,3 +1,9 @@\n"
            " from flask import Flask\n\n"
            "+def test_empty_json_body():\n"
            "+    app = Flask(__name__)\n"
            "+    with app.test_client() as c:\n"
            "+        rv = c.post('/test', content_type='application/json; charset=utf-8')\n"
            "+        assert rv.status_code == 200\n"
            "+        # request.json should be None for empty body, not raise\n"
        ),
    },
    {
        "instance_id": "synthetic__requests-9012",
        "repo": "https://github.com/psf/requests.git",
        "base_commit": "ghi345jkl678",
        "issue_title": "Connection pool leaks socket FDs when keep-alive timeout fires",
        "issue_body": (
            "## Description\n\n"
            "Under high concurrency, the `urllib3` connection pool inside "
            "`requests` leaks socket file descriptors when a keep-alive "
            "connection times out between checking it out of the pool and "
            "actually using it.\n\n"
            "## Steps to Reproduce\n\n"
            "```python\n"
            "import requests\n"
            "session = requests.Session()\n"
            "for _ in range(1000):\n"
            "    session.get('http://localhost:8080/', timeout=1)\n"
            "# File descriptor count keeps growing\n"
            "```\n\n"
            "## Expected Behavior\n\n"
            "Socket FDs should be properly closed when connections timeout.\n\n"
            "## Affected File\n\n"
            "`requests/adapters.py` in the `HTTPAdapter.send` method."
        ),
        "hints_to_generated_patch": (
            "In `HTTPAdapter.send`, wrap the connection acquisition in a try/finally "
            "block that calls `conn.close()` in the finally clause if an exception "
            "was raised after acquiring the connection but before returning the "
            "response. Also verify the connection is still alive before using it."
        ),
        "test_patch": (
            "diff --git a/tests/test_requests.py b/tests/test_requests.py\n"
            "--- a/tests/test_requests.py\n"
            "+++ b/tests/test_requests.py\n"
            "@@ -1,3 +1,8 @@\n"
            " import requests\n\n"
            "+def test_no_fd_leak_on_timeout():\n"
            "+    import resource, os\n"
            "+    before = resource.getrlimit(resource.RLIMIT_NOFILE)\n"
            "+    s = requests.Session()\n"
            "+    for _ in range(100):\n"
            "+        s.get('http://localhost:0/', timeout=0.001)\n"
            "+    after = resource.getrlimit(resource.RLIMIT_NOFILE)\n"
            "+    assert after == before\n"
        ),
    },
]


# ---------------------------------------------------------------------------
# Instance loading
# ---------------------------------------------------------------------------


def _build_issue_text(inst: dict[str, Any]) -> str:
    """Construct the full issue text from title + body."""
    title = inst.get("issue_title", "")
    body = inst.get("issue_body", "")
    return f"# {title}\n\n{body}" if title else body


def _augment_for_uplift(instance: EvalInstance, workdir: str) -> EvalInstance:
    """Return *instance* with the optimized arm's prompt additions, if enabled.

    A no-op in the baseline arm, which is the property that makes the two arms
    comparable.  Inputs are the issue text and the checked-out repository only —
    the same two things a human would have — so this cannot become a channel for
    the answer fields; :mod:`eval.harness.leakage` asserts that structurally.
    """
    from eval.harness.uplift import augment_with_record, uplift_config

    config = uplift_config()
    if not config.enabled:
        return instance
    augmented, localization = augment_with_record(
        instance.task_description, workdir, config=config
    )
    if localization:
        # Stashed on the shared metadata dict, which the harness records after
        # solve returns, so what localization proposed lands in the artifacts
        # next to the patch it was supposed to help produce. Contains only file
        # paths, symbol names, line ranges and BM25 scores — all derived from the
        # repository the agent could read anyway.
        instance.metadata["localization"] = localization
    if augmented == instance.task_description:
        return instance
    return replace(instance, task_description=augmented)


def _retry_if_disqualified(
    instance: EvalInstance,
    result: EvalResult,
    workdir: Path,
    adapter: AgentAdapter,
    **kwargs: Any,
) -> EvalResult:
    """Give the agent one more attempt when its patch provably cannot pass.

    Only fires in the optimized arm, and only when validation *disqualifies* the
    patch — empty, not a diff, or tests-only. Those are the cases where the
    official scorer's verdict is already determined and re-running it would spend
    a container to learn nothing. The baseline submitted 9 empty patches out of
    10 and paid for all 10 verdicts.

    Deliberately one retry, not a loop. The feedback here is about the *shape* of
    the submission, not its correctness, so a second failure means the agent did
    not understand the delivery contract and a third attempt would not change
    that. Looping on a signal that cannot improve is how a retry budget turns
    into a bill.

    The feedback text contains only what the harness observed about the agent's
    own output. No test names, no expected behaviour, nothing from the dataset.
    """
    from eval.harness.uplift import uplift_config
    from eval.harness.validate import validate_patch

    config = uplift_config()
    if not (config.enabled and config.validation):
        return result

    report = validate_patch(result.model_patch, str(workdir))
    if not report.disqualified:
        return result

    reasons = "\n".join(f"- {check.detail}" for check in report.blocking)
    print(f"[uplift] retrying {instance.instance_id}: {report.summary}")
    retry_instance = replace(
        instance,
        task_description=(
            f"{instance.task_description}\n\n"
            "## Your previous attempt was not submittable\n\n"
            "An automated check of the working directory found:\n\n"
            f"{reasons}\n\n"
            "This is about the form of your submission, not whether your "
            "diagnosis was right. Apply your fix to the source file with the "
            "editing tools now, then run `git diff` to confirm it is present."
        ),
    )
    retried = adapter.solve_instance(
        retry_instance, working_dir=str(workdir), **kwargs
    )
    if not retried.model_patch:
        retried.model_patch = _capture_git_diff(str(workdir))
    # Keep the retry only if it produced something the first attempt did not.
    # A retry that came back empty must not erase a first attempt that did not.
    if validate_patch(retried.model_patch, str(workdir)).disqualified and result.model_patch:
        return result
    if not retried.instance_id:
        retried.instance_id = instance.instance_id
    return retried


def load_synthetic_instances() -> list[EvalInstance]:
    """Build EvalInstance objects from the bundled synthetic dataset.

    Returns 3 lightweight instances suitable for pipeline and smoke testing.
    """
    instances: list[EvalInstance] = []
    for meta in SYNTHETIC_INSTANCES:
        instances.append(
            EvalInstance(
                instance_id=meta["instance_id"],
                task_description=_build_issue_text(meta),
                metadata={
                    "repo": meta["repo"],
                    "base_commit": meta["base_commit"],
                    "test_patch": meta.get("test_patch", ""),
                    "hints_to_generated_patch": meta.get(
                        "hints_to_generated_patch", ""
                    ),
                    "synthetic": True,
                },
            )
        )
    return instances


def load_swebench_instances(
    max_instances: int | None = None,
    *,
    allow_synthetic: bool = False,
) -> list[EvalInstance]:
    """Load SWE-bench Verified instances from HuggingFace datasets.

    Tries to load ``princeton-nlp/SWE-bench_Verified`` (test split).  If the
    dataset cannot be downloaded and *allow_synthetic* is ``True``, falls back
    to bundled synthetic instances for pipeline testing.  When
    *allow_synthetic* is ``False`` (the default for real evaluation), any
    dataset-load failure is raised immediately: the official evaluation contract forbids synthetic
    instances must never be silently mixed into official results.

    Args:
        max_instances: If set, return at most this many instances.
        allow_synthetic: If True, synthetic fallback is permitted (dry-run only).

    Returns:
        List of EvalInstance objects, one per SWE-bench task.

    Raises:
        ImportError: ``datasets`` package is not installed and synthetic is
            not allowed.
        RuntimeError: The dataset could not be loaded and synthetic is not
            allowed.
    """
    try:
        from datasets import load_dataset  # type: ignore[import-untyped]
    except ImportError as exc:
        if allow_synthetic:
            print(
                "[swebench] 'datasets' package not installed. "
                "Install with: pip install datasets",
                file=sys.stderr,
            )
            print(
                "[swebench] Falling back to synthetic instances for pipeline testing.",
                file=sys.stderr,
            )
            instances = load_synthetic_instances()
            if max_instances is not None:
                instances = instances[:max_instances]
            return instances
        raise ImportError(
            "'datasets' package is required to load SWE-bench Verified instances. "
            "Install with: pip install datasets"
        ) from exc

    try:
        ds = load_dataset(
            "princeton-nlp/SWE-bench_Verified", split="test", trust_remote_code=True
        )
    except Exception as exc:
        if allow_synthetic:
            print(
                f"[swebench] Failed to load SWE-bench Verified dataset: {exc}",
                file=sys.stderr,
            )
            print(
                "[swebench] Falling back to synthetic instances for pipeline testing.",
                file=sys.stderr,
            )
            instances = load_synthetic_instances()
            if max_instances is not None:
                instances = instances[:max_instances]
            return instances
        raise RuntimeError(
            f"Failed to load SWE-bench Verified dataset: {exc}"
        ) from exc

    instances: list[EvalInstance] = []
    for row in ds:
        instance_id: str = row.get("instance_id", "")
        problem_statement: str = row.get("problem_statement", "")
        repo: str = row.get("repo", "")
        base_commit: str = row.get("base_commit", "")
        test_patch: str = row.get("test_patch", "")
        hints_text: str = row.get("hints_text", "")
        version: str = row.get("version", "")

        instances.append(
            EvalInstance(
                instance_id=instance_id,
                task_description=problem_statement,
                metadata={
                    "repo": repo,
                    "base_commit": base_commit,
                    "test_patch": test_patch,
                    "hints_text": hints_text,
                    "version": version,
                    "synthetic": False,
                },
            )
        )

        if max_instances is not None and len(instances) >= max_instances:
            break

    print(f"[swebench] Loaded {len(instances)} SWE-bench Verified instances.")
    return instances


# ---------------------------------------------------------------------------
# Synthetic repo builder — creates a minimal repo that looks like the target
# so the agent has files to read and edit during dry-run testing.
# ---------------------------------------------------------------------------

#: Files seeded into every synthetic repo so the agent has code to work with.
_SYNTHETIC_REPO_TEMPLATES: dict[str, str] = {
    # Map synthetic instance_id prefixes to repo-relative starter files.
    "synthetic__django-1234": """\
# django/http/request.py (stub)

class QueryDict(dict):
    def __init__(self, query_string=None, mutable=False, encoding=None):
        super().__init__()
        self._mutable = mutable
        self.encoding = encoding or "utf-8"
        if query_string is None:
            return
        if isinstance(query_string, dict):
            for key, value in query_string.items():
                self.appendlist(key, value)
            return
        # BUG: empty string causes ValueError in split parsing below
        for pair in query_string.split("&"):
            if not pair:
                continue
            if "=" in pair:
                key, val = pair.split("=", 1)
            else:
                key, val = pair, ""
            self.appendlist(key, val)

    def appendlist(self, key, value):
        if key in self:
            existing = list(self[key])
            existing.append(value)
            dict.__setitem__(self, key, existing)
        else:
            dict.__setitem__(self, key, [value])

    def __repr__(self):
        return f"<QueryDict: {dict.__repr__(self)}>"
""",
    "synthetic__flask-5678": """\
# flask/wrappers/json.py (stub)

import json


class JSONMixin:
    json_decoder = json.JSONDecoder()

    def on_json_loading_failed(self, e):
        return None

    def _load_json_data(self, data):
        if not data:
            # BUG: should return None here, but sometimes raises on edge cases
            pass
        try:
            return json.loads(data)
        except Exception as e:
            return self.on_json_loading_failed(e)
""",
    "synthetic__requests-9012": """\
# requests/adapters.py (stub)

class HTTPAdapter:
    def send(self, request, **kwargs):
        conn = self.get_connection(request.url)
        timeout = kwargs.get("timeout", None)
        if timeout:
            conn.timeout = timeout
        # BUG: if conn goes stale between get_connection and send,
        # the socket FD is never released
        response = conn.send(request)
        return response

    def get_connection(self, url):
        from urllib3 import PoolManager
        return PoolManager().connection_from_url(url)
""",
}


def _build_synthetic_repo(instance: EvalInstance, workdir: str) -> None:
    """Seed *workdir* with starter files matching the synthetic instance.

    For real instances this is a no-op — the caller should `git clone` the
    actual repo at base_commit instead.
    """
    meta: dict[str, Any] = instance.metadata
    if not meta.get("synthetic"):
        return

    root = Path(workdir)
    instance_id: str = instance.instance_id

    # Pick the best-matching template by prefix.
    template_content: str | None = None
    for prefix, content in _SYNTHETIC_REPO_TEMPLATES.items():
        if instance_id.startswith(prefix):
            template_content = content
            break

    if template_content is None:
        # Generic fallback: create a minimal Python file with a documented bug.
        target_file = root / "src" / "main.py"
        target_file.parent.mkdir(parents=True, exist_ok=True)
        target_file.write_text(
            "# Minimal project stub for SWE-bench synthetic instance.\n"
            f"# Instance: {instance_id}\n"
            f"# Issue:\n"
            + "\n".join(f"#   {line}" for line in instance.task_description.splitlines())
            + "\n\n"
            "def buggy_function():\n"
            '    """Placeholder — fix me based on the issue above."""\n'
            "    raise NotImplementedError('Fix this bug')\n",
            encoding="utf-8",
        )
    else:
        # Locate the first line comment to determine the file path.
        lines = template_content.strip().splitlines()
        target_rel = None
        for line in lines:
            if line.startswith("# ") and not line.startswith("# BUG"):
                # e.g. "# django/http/request.py (stub)"
                candidate = line[2:].strip()
                # Remove trailing "(stub)" etc.
                for suffix in (" (stub)", " (stub)", "(stub)"):
                    if candidate.endswith(suffix):
                        candidate = candidate[: -len(suffix)].strip()
                target_rel = candidate
                break

        if target_rel is None:
            target_rel = f"src/{instance_id}/main.py"

        target_file = root / target_rel
        target_file.parent.mkdir(parents=True, exist_ok=True)
        target_file.write_text(template_content, encoding="utf-8")

    # Always create a minimal setup so git can work.
    (root / "README.md").write_text(
        f"# Synthetic repo for {instance_id}\n\n"
        "This is a lightweight stub for pipeline/smoke testing.\n",
        encoding="utf-8",
    )


# ---------------------------------------------------------------------------
# Repository setup for a single instance
# ---------------------------------------------------------------------------


def _setup_workdir(
    instance: EvalInstance,
    base_dir: str,
    *,
    dry_run: bool = False,
    nest: bool = True,
) -> str:
    """Prepare the working directory for *instance*.

    For real instances this clones the target repo at base_commit. For
    synthetic/dry-run instances it creates a fake git repo with stub files.

    Args:
        instance: The evaluation instance.
        base_dir: Parent directory for the per-instance working directory,
            or the working directory itself when *nest* is False.
        dry_run: If True, force synthetic repo creation.
        nest: When True (the legacy CLI, which shares one base dir across
            instances) the repo is created at ``base_dir/<instance_id>``.
            When False the repo is created directly in *base_dir*, which is
            what the unified Harness needs: it allocates a fresh temp
            workspace per instance and passes that exact path to the agent as
            ``working_dir``.  Nesting there put the repo one level below the
            agent's working_dir, so ``git diff HEAD`` ran outside the
            repository and every captured patch was empty.

    Returns:
        Absolute path to the instance working directory.
    """
    meta: dict[str, Any] = instance.metadata
    workdir = os.path.join(base_dir, instance.instance_id) if nest else base_dir
    os.makedirs(workdir, exist_ok=True)

    if dry_run or meta.get("synthetic"):
        _build_synthetic_repo(instance, workdir)
        _init_git_repo(workdir)
        return workdir

    # Real clone path: git clone the repo at base_commit.
    repo_url: str = meta.get("repo", "")
    base_commit: str = meta.get("base_commit", "")

    if not repo_url:
        raise ValueError(
            f"No repo URL in metadata for instance {instance.instance_id}"
        )

    if not base_commit:
        raise ValueError(
            f"No base_commit in metadata for instance {instance.instance_id}"
        )

    print(f"[swebench] Cloning https://github.com/{repo_url}.git at {base_commit}...")
    # Still a full clone in the worst case — base_commit is typically an old SHA
    # that a shallow clone of the default-branch tip cannot resolve — but served
    # from a local mirror when one is available.  Without the mirror this cost
    # recurred per instance (~60-90s for astropy), which is most of the wall
    # clock of an N-instance run and made a paired two-arm experiment
    # impractical.  A cache failure degrades to the original clone rather than
    # failing the run; the outcome is printed so a degraded run is visible
    # instead of just being slow for unexplained reasons.
    from eval.harness.repo_cache import clone_at_commit

    outcome = clone_at_commit(repo_url, base_commit, workdir)
    if outcome.degraded:
        print(
            f"[swebench] repo cache not used ({outcome.fallback_reason}); "
            "fell back to a full clone"
        )
    elif outcome.mirror_created:
        print(f"[swebench] created local mirror for {repo_url}")
    return workdir


def _init_git_repo(path: str) -> None:
    """Initialize a git repository in *path* with an initial commit."""
    subprocess.run(
        ["git", "-C", path, "init"],
        check=True,
        capture_output=True,
        timeout=30,
    )
    subprocess.run(
        ["git", "-C", path, "config", "user.email", "eval@swebench.local"],
        check=True,
        capture_output=True,
        timeout=10,
    )
    subprocess.run(
        ["git", "-C", path, "config", "user.name", "SWE-bench Eval"],
        check=True,
        capture_output=True,
        timeout=10,
    )
    subprocess.run(
        ["git", "-C", path, "add", "-A"],
        check=True,
        capture_output=True,
        timeout=30,
    )
    subprocess.run(
        ["git", "-C", path, "commit", "-m", "Initial commit (synthetic)"],
        check=True,
        capture_output=True,
        timeout=30,
    )


def _capture_git_diff(workdir: str) -> str:
    """Capture the unified diff of all uncommitted changes in *workdir*.

    The encoding is pinned to UTF-8 rather than left to ``text=True``, which
    would decode with the locale codec: on a zh-CN Windows that is gbk, and a
    diff containing a smart quote or CJK text raised ``UnicodeDecodeError`` in
    the subprocess reader thread.  The bare ``except`` below then returned an
    empty string, so an encoding fault was indistinguishable from an agent that
    produced no patch.  ``errors="replace"`` keeps one undecodable byte from
    costing the whole diff.
    """
    try:
        result = subprocess.run(
            ["git", "-C", workdir, "diff", "--no-color", "HEAD"],
            capture_output=True,
            encoding="utf-8",
            errors="replace",
            timeout=30,
            check=False,
        )
        return (result.stdout or "").strip()
    except Exception:
        return ""


# ---------------------------------------------------------------------------
# Main runner
# ---------------------------------------------------------------------------


class SWEBenchRunner:
    """Orchestrates running SWE-bench instances through an AgentAdapter.

    Usage::

        from eval.adapter import MyAgentAdapter

        adapter = MyAgentAdapter()
        runner = SWEBenchRunner(adapter, output_dir="./swebench-results")
        runner.run_all(max_instances=10)
    """

    def __init__(
        self,
        adapter: "AgentAdapter",
        output_dir: str = ".",
        *,
        dry_run: bool = False,
        verbose: bool = True,
    ) -> None:
        self._adapter = adapter
        self._output_dir = Path(output_dir)
        self._dry_run = dry_run
        self._verbose = verbose

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    def load_instances(
        self, max_instances: int | None = None
    ) -> list[EvalInstance]:
        """Load instances — real SWE-bench if available, else synthetic."""
        if self._dry_run:
            if self._verbose:
                print("[swebench] Dry-run mode: using synthetic instances.")
            instances = load_synthetic_instances()
            if max_instances is not None:
                instances = instances[:max_instances]
            return instances
        return load_swebench_instances(
            max_instances=max_instances,
            allow_synthetic=False,
        )

    def run_all(
        self,
        max_instances: int | None = None,
    ) -> list[EvalResult]:
        """Load and run all (or *max_instances*) instances.

        Returns a list of EvalResult, one per completed instance.
        """
        instances = self.load_instances(max_instances=max_instances)
        results: list[EvalResult] = []

        if self._verbose:
            print(f"[swebench] Running {len(instances)} instance(s)...")

        with tempfile.TemporaryDirectory(
            prefix="swebench-work-"
        ) as _base_dir:
            for idx, instance in enumerate(instances):
                if self._verbose:
                    print(
                        f"[swebench] [{idx + 1}/{len(instances)}] "
                        f"{instance.instance_id}"
                    )
                result = self.run_instance(instance, base_dir=_base_dir)
                results.append(result)

        self._write_predictions(results)
        return results

    def run_instance(
        self,
        instance: EvalInstance,
        *,
        base_dir: str | None = None,
    ) -> EvalResult:
        """Run a single instance and return its EvalResult.

        Args:
            instance: The evaluation instance to run.
            base_dir: Parent directory for per-instance working directories.
                      If None, a temporary directory is created and cleaned up.

        Returns:
            EvalResult with model_patch populated from git diff.
        """
        own_tmp = base_dir is None
        if own_tmp:
            base_dir = tempfile.mkdtemp(prefix="swebench-instance-")

        try:
            workdir = _setup_workdir(
                instance,
                base_dir,
                dry_run=self._dry_run,
            )

            if self._verbose:
                print(f"  workdir: {workdir}")

            start_time = time.monotonic()
            result = self._adapter.solve_instance(
                instance, working_dir=workdir, dry_run=self._dry_run
            )
            elapsed = time.monotonic() - start_time

            # Capture the patch the agent produced.
            if not result.model_patch:
                result.model_patch = _capture_git_diff(workdir)

            if self._verbose:
                patch_len = len(result.model_patch)
                cost_str = f"${result.cost:.4f}" if result.cost else "n/a"
                print(
                    f"  done in {elapsed:.1f}s | "
                    f"patch={patch_len}B | "
                    f"cost={cost_str}"
                )
                if result.error:
                    print(f"  ERROR: {result.error}")

            # Fill in instance_id if the adapter didn't.
            if not result.instance_id:
                result.instance_id = instance.instance_id

            return result

        finally:
            if own_tmp and base_dir:
                shutil.rmtree(base_dir, ignore_errors=True)

    # ------------------------------------------------------------------
    # Output
    # ------------------------------------------------------------------

    def _write_predictions(self, results: list[EvalResult]) -> Path:
        """Write predictions.jsonl in SWE-bench format."""
        self._output_dir.mkdir(parents=True, exist_ok=True)
        output_path = self._output_dir / "predictions.jsonl"
        model_name = os.environ.get(
            "SWEBENCH_MODEL_NAME", "code-agent-default"
        )

        lines_written = 0
        with open(output_path, "w", encoding="utf-8") as fh:
            for result in results:
                if result.error and not result.model_patch:
                    if self._verbose:
                        print(
                            f"[swebench] Skipping {result.instance_id}: "
                            f"error and no patch ({result.error})"
                        )
                    continue
                entry = {
                    "instance_id": result.instance_id,
                    "model_name_or_path": model_name,
                    "model_patch": result.model_patch,
                }
                fh.write(json.dumps(entry, ensure_ascii=False) + "\n")
                lines_written += 1

        if self._verbose:
            print(
                f"[swebench] Wrote {lines_written} prediction(s) to {output_path}"
            )
        return output_path


# ---------------------------------------------------------------------------
# Stub adapter for smoke testing (no real agent required)
# ---------------------------------------------------------------------------


class _StubAdapter:
    """Minimal AgentAdapter stub that produces a fixed patch.

    This is used for --dry-run and --smoke-test to validate the pipeline
    without a real agent or orchestrator. It simulates what an agent would
    produce after reading and editing the codebase.

    The stub reads the synthetic repo's starter file, applies a trivial fix
    (adds the guard clause described in hints_to_generated_patch), and
    produces a git diff.
    """

    def solve_instance(
        self, instance: EvalInstance, working_dir: str = "", **kwargs: Any
    ) -> EvalResult:
        """Simulate solving the instance by writing a fix to the repo."""
        meta: dict[str, Any] = instance.metadata
        hints: str = meta.get("hints_to_generated_patch", "")
        synthetic: bool = meta.get("synthetic", False)

        if not synthetic or not hints:
            # For real instances where we have no hints, return a stub.
            return EvalResult(
                instance_id=instance.instance_id,
                model_patch=_capture_git_diff(working_dir),
                error="" if working_dir else "No working directory provided",
            )

        # Apply the hinted fix to the synthetic repo.
        _apply_hinted_fix(instance, working_dir, hints)

        return EvalResult(
            instance_id=instance.instance_id,
            model_patch=_capture_git_diff(working_dir),
            cost=0.0,
            tokens_in=len(instance.task_description) // 4,
            tokens_out=120,
        )


def _apply_hinted_fix(
    instance: EvalInstance, workdir: str, _hints: str
) -> None:
    """Apply a simple fix to the synthetic repo based on the hints.

    This simulates what a real agent would do: read files, find the bug,
    and apply a targeted edit. For each synthetic instance type we apply
    the documented fix so the resulting git diff is non-empty.
    """
    if not workdir or not os.path.isdir(workdir):
        return

    root = Path(workdir)
    instance_id = instance.instance_id

    if instance_id.startswith("synthetic__django-1234"):
        target = root / "django" / "http" / "request.py"
        if target.exists():
            content = target.read_text(encoding="utf-8")
            # Add empty-string guard before the split loop.
            old = (
                "        # BUG: empty string causes ValueError in split parsing below\n"
                "        for pair in query_string.split"
            )
            new = (
                "        # BUG: empty string causes ValueError in split parsing below\n"
                "        if not query_string or not query_string.strip():\n"
                "            return\n"
                "        for pair in query_string.split"
            )
            if old in content:
                content = content.replace(old, new)
                target.write_text(content, encoding="utf-8")

    elif instance_id.startswith("synthetic__flask-5678"):
        target = root / "flask" / "wrappers" / "json.py"
        if target.exists():
            content = target.read_text(encoding="utf-8")
            old = (
                "        if not data:\n"
                "            # BUG: should return None here, but sometimes raises on edge cases\n"
                "            pass"
            )
            new = (
                "        if not data:\n"
                "            # FIX: return None for empty body\n"
                "            return None"
            )
            if old in content:
                content = content.replace(old, new)
                target.write_text(content, encoding="utf-8")

    elif instance_id.startswith("synthetic__requests-9012"):
        target = root / "requests" / "adapters.py"
        if target.exists():
            content = target.read_text(encoding="utf-8")
            old = (
                "        # BUG: if conn goes stale between get_connection and send,\n"
                "        # the socket FD is never released\n"
                "        response = conn.send(request)\n"
                "        return response"
            )
            new = (
                "        # FIX: close connection on failure to avoid FD leak\n"
                "        try:\n"
                "            response = conn.send(request)\n"
                "            return response\n"
                "        except Exception:\n"
                "            conn.close()\n"
                "            raise"
            )
            if old in content:
                content = content.replace(old, new)
                target.write_text(content, encoding="utf-8")


# ---------------------------------------------------------------------------
# Module-level load_instances() -- eval/run.py HarnessRun path
# ---------------------------------------------------------------------------


def load_instances(
    limit: int | None = None,
    instance_ids: "Sequence[str] | None" = None,
    **kwargs: Any,
) -> list[EvalInstance]:
    """Module-level instance loader for the HarnessRun path in ``eval/run.py``.

    Loads real SWE-bench Verified instances from HuggingFace datasets.  Does
    **not** fall back to synthetic data: synthetic
    instances must never be mixed into official results.

    Args:
        limit: If set, return at most this many instances.
        instance_ids: If set, return exactly these instances, in the order
            given.  Used to run a pinned subset so two arms of a paired
            experiment are measured on identical instances.  A requested id that
            the dataset does not contain is an error rather than a silent
            omission: a paired comparison whose two arms quietly ran different
            denominators is worse than no comparison.

    Returns:
        List of :class:`EvalInstance` objects.

    Raises:
        ImportError: ``datasets`` is not installed.
        RuntimeError: The dataset could not be loaded.
        ValueError: *instance_ids* names an instance the dataset lacks.
    """
    instances = load_swebench_instances(
        max_instances=None if instance_ids else limit,
        allow_synthetic=False,
    )
    if instance_ids:
        by_id = {inst.instance_id: inst for inst in instances}
        missing = [iid for iid in instance_ids if iid not in by_id]
        if missing:
            raise ValueError(
                "requested instance_ids not present in SWE-bench Verified: "
                f"{missing}"
            )
        instances = [by_id[iid] for iid in instance_ids]
        if limit is not None:
            instances = instances[:limit]
        print(f"[swebench] Restricted to {len(instances)} pinned instance(s).")
    return instances


# ---------------------------------------------------------------------------
# Module-level run() -- eval/run.py CLI contract
# ---------------------------------------------------------------------------


def run(
    driver: "AgentAdapter",
    limit: int | None = None,
    **kwargs: Any,
) -> list[EvalResult]:
    """Module-level runner aligned with the ``eval.run`` CLI contract.

    Builds a :class:`SWEBenchRunner` around *driver* and returns the
    per-instance :class:`EvalResult` list from ``run_all()``.  The default
    path loads real SWE-bench Verified data (synthetic instances only on an
    explicit ``dry_run=True``), and predictions.jsonl is written for the
    official harness.

    Extra keyword arguments are forwarded to the runner: ``dry_run``,
    ``output_dir``, ``verbose``.
    """
    runner = SWEBenchRunner(
        adapter=driver,
        output_dir=kwargs.pop("output_dir", "."),
        dry_run=bool(kwargs.pop("dry_run", False)),
        verbose=bool(kwargs.pop("verbose", True)),
    )
    return runner.run_all(max_instances=limit)


# ---------------------------------------------------------------------------
# Validation
# ---------------------------------------------------------------------------


def validate_predictions_file(path: str) -> tuple[bool, str]:
    """Validate that *path* is a well-formed predictions.jsonl.

    Checks:
      - File exists and is non-empty.
      - Each line is valid JSON.
      - Required keys present: instance_id, model_name_or_path, model_patch.

    Returns:
        (ok, message) tuple.
    """
    if not os.path.isfile(path):
        return False, f"File not found: {path}"

    required_keys = {"instance_id", "model_name_or_path", "model_patch"}

    with open(path, encoding="utf-8") as fh:
        for line_num, line in enumerate(fh, start=1):
            line = line.strip()
            if not line:
                return False, f"Line {line_num} is empty"
            try:
                entry = json.loads(line)
            except json.JSONDecodeError as exc:
                return False, f"Line {line_num} is not valid JSON: {exc}"

            missing = required_keys - set(entry.keys())
            if missing:
                return (
                    False,
                    f"Line {line_num} missing required keys: {missing}",
                )

            if not entry["instance_id"]:
                return False, f"Line {line_num}: empty instance_id"

    with open(path, encoding="utf-8") as fh:
        line_count = sum(1 for _ in fh)

    return True, f"Valid predictions.jsonl with {line_count} entries"


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------


def _build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="SWE-bench Verified adapter for Code Agent evaluation",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=(
            "Examples:\n"
            "  # Dry-run with synthetic instances\n"
            "  python -m eval.benchmarks.swebench --dry-run\n\n"
            "  # Smoke test (1 instance, strict validation)\n"
            "  python -m eval.benchmarks.swebench --smoke-test\n\n"
            "  # Run with real SWE-bench instances (requires datasets + Docker)\n"
            "  python -m eval.benchmarks.swebench --max-instances 10\n"
        ),
    )

    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Use synthetic instances without Docker/real repos.",
    )
    parser.add_argument(
        "--smoke-test",
        action="store_true",
        help="Run exactly 1 synthetic instance and validate predictions.jsonl.",
    )
    parser.add_argument(
        "--max-instances",
        type=int,
        default=None,
        help="Limit to at most N instances (default: all).",
    )
    parser.add_argument(
        "--output-dir",
        type=str,
        default=".",
        help="Directory for predictions.jsonl (default: current directory).",
    )
    parser.add_argument(
        "--verbose",
        action=argparse.BooleanOptionalAction,
        default=True,
        help="Print progress information (default: --verbose).",
    )
    return parser


def main(argv: Sequence[str] | None = None) -> int:
    """CLI entry point for `python -m eval.benchmarks.swebench`."""
    parser = _build_parser()
    args = parser.parse_args(argv)

    dry_run = args.dry_run or args.smoke_test
    max_instances = args.max_instances
    if args.smoke_test:
        max_instances = 1

    runner = SWEBenchRunner(
        adapter=_StubAdapter(),
        output_dir=args.output_dir,
        dry_run=dry_run,
        verbose=args.verbose,
    )

    results = runner.run_all(max_instances=max_instances)

    # Validate output
    predictions_path = os.path.join(args.output_dir, "predictions.jsonl")
    ok, msg = validate_predictions_file(predictions_path)
    if args.verbose:
        print(f"[swebench] Validation: {'PASS' if ok else 'FAIL'} — {msg}")

    # Smoke test enforces strict pass/fail.
    if args.smoke_test:
        if not ok:
            print(
                f"SMOKE TEST FAILED: {msg}",
                file=sys.stderr,
            )
            return 1
        # Additionally verify there is exactly 1 entry with a non-empty patch.
        with open(predictions_path, encoding="utf-8") as fh:
            entries = [json.loads(line) for line in fh if line.strip()]
        if len(entries) != 1:
            print(
                f"SMOKE TEST FAILED: expected 1 prediction, got {len(entries)}",
                file=sys.stderr,
            )
            return 1
        if not entries[0].get("model_patch"):
            print(
                "SMOKE TEST FAILED: model_patch is empty",
                file=sys.stderr,
            )
            return 1
        print("SMOKE TEST PASSED")
        return 0

    # Failure to produce a non-empty predictions file is always an error.
    if not ok:
        return 1

    if all(
        not r.model_patch and r.error for r in results
    ):
        print(
            "[swebench] All instances failed — check errors above.",
            file=sys.stderr,
        )
        return 1

    return 0


# ---------------------------------------------------------------------------
# Official scoring — automated on Linux+Docker, WSL2 on Windows, fail-closed
# ---------------------------------------------------------------------------


def _can_score_official() -> tuple[bool, str]:
    """Check whether the official swebench scorer can run in this environment.

    Returns (available, detail) where detail explains why scoring is or isn't
    available.
    """
    # Linux: check Docker + swebench
    if platform.system() == "Linux":
        try:
            import resource  # noqa: F401
            import swebench.harness.run_evaluation  # noqa: F401
            import docker  # noqa: F401
            return True, "Linux + Docker + swebench available"
        except ImportError as e:
            return False, f"Linux but missing dependency: {e}"
        except Exception as e:
            return False, f"Linux but check failed: {e}"

    # Windows: check WSL2 availability
    if platform.system() == "Windows":
        try:
            result = subprocess.run(
                ["wsl.exe", "-d", "Ubuntu-24.04", "--", "bash", "-c",
                 "python3 -c 'import docker; print(\"ok\")' 2>/dev/null || echo 'no-docker'"],
                capture_output=True, text=True, encoding="utf-8", errors="replace",
                timeout=30,
            )
            if "ok" in result.stdout:
                return True, "Windows + WSL2 Ubuntu-24.04 + Docker available"
            else:
                return False, f"Windows + WSL2 but Docker not available in WSL: {result.stdout.strip()}"
        except Exception as e:
            return False, f"Windows but WSL2 check failed: {e}"

    return False, f"unsupported platform: {platform.system()}"


# "Caller said nothing", which must stay distinguishable from an explicit
# ``namespace=None`` (a deliberate request to build images locally).
_UNSET = object()


def _resolve_namespace() -> "str | None":
    """Which Docker namespace the official harness should take images from.

    ``None`` means "build every instance image locally".  Both scoring call
    sites used to hardcode that, silently overriding upstream's own default of
    ``"swebench"``, and it is why scoring failed on the first instance that ever
    reached it: a local build runs ``setup_repo.sh``, which must ``git clone``
    the project's entire history *inside the build container*.  For astropy that
    transfer ran ten minutes and then died on ``curl 92 HTTP/2 stream 0 was not
    closed cleanly: CANCEL``, so the image never built and no verdict existed.

    Pulling the maintainers' prebuilt image skips that clone entirely.  It is
    also the more canonical choice: those images are what the official harness
    uses by default, published by the same project as the ``swebench`` package
    we already execute, so it adds no new trust boundary.

    Override with ``SWEBENCH_NAMESPACE``; ``none`` or an empty value restores
    local building for an air-gapped host.
    """
    raw = os.environ.get("SWEBENCH_NAMESPACE", "swebench").strip()
    if not raw or raw.lower() == "none":
        return None
    return raw


def _run_official_scoring(
    predictions_path: str,
    output_dir: str,
    dataset_name: str = "princeton-nlp/SWE-bench_Verified",
    split: str = "test",
    max_workers: int = 4,
    run_id: str = "code-agent-eval",
    timeout: int = 3600,
    namespace: "str | None | object" = _UNSET,
) -> tuple[bool, str]:
    """Invoke the official swebench scoring harness.

    On Linux, runs directly. On Windows, delegates to WSL2. Returns
    (success, detail).
    """
    if namespace is _UNSET:
        namespace = _resolve_namespace()
    if platform.system() == "Windows":
        return _run_official_scoring_wsl(
            predictions_path, output_dir, dataset_name, split,
            max_workers, run_id, timeout, namespace,
        )
    else:
        return _run_official_scoring_local(
            predictions_path, output_dir, dataset_name, split,
            max_workers, run_id, timeout, namespace,
        )


def _run_official_scoring_local(
    predictions_path: str,
    output_dir: str,
    dataset_name: str,
    split: str,
    max_workers: int,
    run_id: str,
    timeout: int,
    namespace: "str | None" = None,
) -> tuple[bool, str]:
    """Run swebench.harness.run_evaluation locally (Linux only)."""
    import resource  # noqa: F401
    from swebench.harness.run_evaluation import main as run_eval_main

    try:
        run_eval_main(
            dataset_name=dataset_name,
            split=split,
            instance_ids=[],
            predictions_path=predictions_path,
            max_workers=max_workers,
            force_rebuild=False,
            cache_level="env",
            clean=False,
            open_file_limit=4096,
            run_id=run_id,
            timeout=timeout,
            namespace=namespace,
            rewrite_reports=False,
            modal=False,
            report_dir=output_dir,
        )
        return True, f"official scoring completed for {run_id}"
    except Exception as e:
        return False, f"official scoring failed: {e}"


_REPO_ROOT = Path(__file__).resolve().parents[2]


def _to_wsl_path(path: "str | Path") -> str:
    """Translate a host path into the WSL2 path that names the same file.

    The previous implementation blindly prefixed ``/mnt/d/vscode/localcode/``
    onto whatever it was handed.  That is only correct for a path relative to
    the repo root; the unified Harness passes an **absolute** workspace path,
    which produced nonsense such as
    ``/mnt/d/vscode/localcode/D:\\vscode\\localcode\\...`` and made the
    official scorer either fail or report on nothing.

    Handled cases:

    - absolute Windows path (``D:\\a\\b`` or ``D:/a/b``) -> ``/mnt/d/a/b``
    - already-POSIX absolute path (``/mnt/d/...``, ``/home/...``) -> unchanged
    - repo-relative path (``eval_results/run/x.jsonl``, ``./x``) -> anchored
      under the repo root inside WSL

    Raises :class:`ValueError` on an empty path rather than guessing.
    """
    raw = str(path).strip()
    if not raw:
        raise ValueError("cannot translate an empty path to a WSL path")

    normalized = raw.replace("\\", "/")

    # Already a POSIX absolute path (/mnt/..., /home/..., /tmp/...).
    if normalized.startswith("/"):
        return normalized

    # Absolute Windows path with a drive letter: D:/a/b -> /mnt/d/a/b
    if len(normalized) >= 2 and normalized[1] == ":" and normalized[0].isalpha():
        drive = normalized[0].lower()
        remainder = normalized[2:].lstrip("/")
        return f"/mnt/{drive}/{remainder}" if remainder else f"/mnt/{drive}"

    # Repo-relative path: anchor it under the repo root inside WSL.
    while normalized.startswith("./"):
        normalized = normalized[2:]
    normalized = normalized.lstrip("/")
    repo_root = _to_wsl_path(_REPO_ROOT).rstrip("/")
    return f"{repo_root}/{normalized}"


#: Labels the official SWE-bench harness prints in its closing summary.  These
#: are the lines a human looks for; a blind tail slice is what used to lose them.
_SUMMARY_LABELS: tuple[str, ...] = (
    "Instances submitted",
    "Instances completed",
    "Instances incomplete",
    "Instances resolved",
    "Instances unresolved",
    "Instances with empty patches",
    "Instances with errors",
    "Unstopped containers",
    "Unremoved images",
)


def _summarise_official_stdout(stdout: str, limit: int = 800) -> str:
    """Extract whole labelled summary lines instead of slicing bytes.

    Defect 8 in the portfolio: this used to be ``stdout[-200:]``, a tail slice
    that cut the official summary mid-line.  After the encoding fault was fixed
    the ``Instances resolved`` label was *still* missing from ``scorer_status``,
    because 200 characters of tail simply did not reach it.

    This string is a convenience label and nothing more.  The verdict is read
    from the official ``report.json`` by ``_read_official_resolution()``, and
    the raw output is pinned verbatim under ``scorer/``.  A paraphrase must
    never be the evidence — that is the whole lesson of defects 3, 5 and 8.  So
    when the labels cannot be found this says so explicitly rather than
    returning a plausible-looking fragment.
    """
    if not stdout:
        return "no stdout captured"
    matched = [
        line.strip()
        for line in stdout.splitlines()
        if any(label in line for label in _SUMMARY_LABELS)
    ]
    if not matched:
        tail = stdout.strip().splitlines()[-3:]
        return (
            "official summary labels not found; last lines: "
            + " | ".join(part.strip() for part in tail if part.strip())
        )[:limit]
    summary = "; ".join(matched)
    if len(summary) > limit:
        # Truncate on a line boundary and say so, rather than silently cutting.
        kept: list[str] = []
        used = 0
        for line in matched:
            if used + len(line) + 2 > limit - 40:
                break
            kept.append(line)
            used += len(line) + 2
        omitted = len(matched) - len(kept)
        summary = "; ".join(kept) + f"; [+{omitted} more summary lines omitted]"
    return summary


def _run_official_scoring_wsl(
    predictions_path: str,
    output_dir: str,
    dataset_name: str,
    split: str,
    max_workers: int,
    run_id: str,
    timeout: int,
    namespace: "str | None" = None,
) -> tuple[bool, str]:
    """Run official scoring via WSL2 Ubuntu-24.04."""
    wsl_preds = _to_wsl_path(predictions_path)
    wsl_output = _to_wsl_path(output_dir)
    wsl_repo_root = _to_wsl_path(_REPO_ROOT)

    # WSL inherits no Windows proxy variables (WSLENV is empty), so a proxy the
    # scorer needs has to be exported inside the WSL command.  On a mirrored-mode
    # WSL with DNS tunneling, raw.githubusercontent.com can resolve to "::" and
    # refuse instantly, which fails the official scorer while it fetches the
    # environment requirements; through the host proxy the same URL returns 200.
    # Read from the environment rather than hardcoded, and omitted entirely when
    # unset so a working-DNS environment is untouched.  Loopback stays direct so
    # the Docker socket and local services are not routed through the proxy.
    proxy = os.environ.get("SWEBENCH_WSL_PROXY", "").strip()
    proxy_prefix = ""
    if proxy:
        proxy_prefix = (
            f"export http_proxy={proxy} https_proxy={proxy} "
            f"no_proxy=127.0.0.1,localhost,::1 && "
        )

    cmd = (
        f"cd {shlex.quote(wsl_repo_root)} && "
        f"{proxy_prefix}"
        f"python3 -c \""
        f"import sys; sys.path.insert(0, '.'); "
        f"from swebench.harness.run_evaluation import main; "
        f"main(dataset_name='{dataset_name}', split='{split}', "
        f"instance_ids=[], predictions_path='{wsl_preds}', "
        f"max_workers={max_workers}, force_rebuild=False, "
        f"cache_level='env', clean=False, open_file_limit=4096, "
        f"run_id='{run_id}', timeout={timeout}, namespace={namespace!r}, "
        f"rewrite_reports=False, modal=False, "
        f"report_dir='{wsl_output}')\""
    )

    attempts = 2  # one retry, for transport faults only -- see _is_transient_network
    for attempt in range(1, attempts + 1):
        try:
            result = subprocess.run(
                ["wsl.exe", "-d", "Ubuntu-24.04", "--", "bash", "-c", cmd],
                # Pin UTF-8: this call's stdout becomes ``scorer_status`` evidence.
                # With bare ``text=True`` a zh-CN Windows decodes it as gbk, and the
                # official harness emits ✔/✗ and box-drawing characters, so the
                # reader thread raised UnicodeDecodeError and the verdict text was
                # lost -- an encoding fault masquerading as a scorer failure.
                capture_output=True, text=True, encoding="utf-8", errors="replace",
                timeout=timeout + 600,
            )
        except subprocess.TimeoutExpired:
            return False, f"WSL2 scoring timed out after {timeout}s"
        except Exception as exc:
            return False, f"WSL2 scoring error: {exc}"
        if result.returncode == 0:
            return True, f"WSL2 official scoring completed: {_summarise_official_stdout(result.stdout)}"
        combined = f"{result.stderr}\n{result.stdout}"
        if attempt < attempts and _is_transient_network(combined):
            # The official harness fetches each instance's environment spec from
            # raw.githubusercontent.com before any test runs. That fetch died on
            # `SSLEOFError` on one instance per run while the same URL returned
            # 200 six times out of six when retried by hand -- a transport
            # flake, not a property of the patch.
            #
            # Retrying transport is not re-rolling a measurement: the prediction
            # file is unchanged, no test has executed yet, and the alternative is
            # an instance recorded as unmeasured for reasons that have nothing to
            # do with the agent. A non-network failure is never retried, so a
            # genuinely failing scorer still fails once and loudly.
            print(
                f"[scorer] transient network failure, retrying once: "
                f"{_first_network_signature(combined)}"
            )
            continue
        return False, f"WSL2 scoring failed (exit {result.returncode}): {result.stderr[-500:]}"
    # Unreachable: the loop either returns or continues, and the final attempt
    # cannot continue. Present so a future edit to `attempts` cannot fall through
    # to an implicit None return, which the caller would unpack and crash on.
    return False, "WSL2 scoring produced no result"


# ---------------------------------------------------------------------------
# AgentBenchmark implementation (unified harness lifecycle)
# ---------------------------------------------------------------------------


class OfficialScorerUnavailable(RuntimeError):
    """The official scorer did not produce a verdict.

    Raised when the scorer could not run (no Docker/WSL), crashed, or ran but
    left no report.  It must NOT be used for a scorer that ran and reported
    ``resolved=False`` — that is a real measurement.

    This is an exception rather than a ``resolved: False`` return value on
    purpose: the harness merges a returned dict into the prediction and counts
    the instance as completed, so returning here made "nothing was measured"
    indistinguishable from "the patch did not fix the bug" and let a run whose
    scorer never executed still exit 0.  Raising routes it through the
    harness's scorer-callback wrapper into ERROR_SCORER instead.
    """


class SWEBenchAdapter(AgentBenchmark):
    """SWE-bench Verified adapter for the unified :class:`AgentBenchmark`
    contract (prepare -> solve -> score).

    Reuses the existing SWE-bench helpers:

    - :meth:`prepare` clones the repo (or seeds a synthetic repo) via
      :func:`_setup_workdir`; the git worktree lives at
      ``workspace/<instance_id>``.
    - :meth:`solve` runs the agent with the repo as ``working_dir`` and
      falls back to :func:`_capture_git_diff` when the adapter did not fill
      ``model_patch``.
    - :meth:`score` writes ``predictions.jsonl`` for the instance and
      invokes the OFFICIAL swebench scorer when available
      (:func:`_can_score_official`).  Without official scoring the instance
      is reported unresolved — the official scorer is never faked.

    The pins reflect exactly what the loader (:func:`load_swebench_instances`)
    and the official scorer use: the ``test`` split of
    ``princeton-nlp/SWE-bench_Verified`` and ``swebench.harness.run_evaluation``.
    """

    name = "swebench"

    #: Dataset consumed by :func:`load_swebench_instances`.
    _DATASET_NAME = "princeton-nlp/SWE-bench_Verified"
    #: Split loaded by :func:`load_swebench_instances`.  The loader does not
    #: pin a dataset git revision, so the split is the honest revision pin.
    _DATASET_SPLIT = "test"
    #: Official scorer invoked by :func:`_run_official_scoring`.
    _SCORER_NAME = "swebench.harness.run_evaluation"

    # ------------------------------------------------------------------
    # AgentBenchmark contract
    # ------------------------------------------------------------------

    def prepare(self, instance: EvalInstance, workspace: Path) -> None:
        """Clone the repo into the workspace (or seed a synthetic repo).

        Delegates to :func:`_setup_workdir` with ``nest=False`` so *workspace*
        itself becomes the git root.  The Harness allocates a fresh temp
        workspace per instance and passes that same path to the agent as
        ``working_dir``, so a nested repo left the agent's ``git diff HEAD``
        running outside the repository and every captured patch was empty.

        Arm B's prompt augmentation happens here, not in :meth:`solve`.
        ``HarnessRun`` calls ``adapter.solve_instance()`` on the *driver* and
        uses this class only for ``prepare`` and ``score`` — so :meth:`solve` is
        never invoked on the runner path, and an augmentation placed there ran
        for nobody.  The first arm B run produced a complete artifact tree with
        zero localization records: a second baseline wearing the optimized arm's
        directory name.  ``prepare`` is the earliest hook the runner does call
        that already has the populated worktree, which is what localization
        needs to read.

        Mutating *instance* in place rather than returning a copy, because the
        runner ignores this method's return value and hands its own reference to
        the driver.  ``EvalInstance`` is a non-frozen dataclass, so the driver
        and the artifacts both observe the change.
        """
        _setup_workdir(instance, str(workspace), nest=False)
        workdir = _instance_workdir(instance, Path(workspace))
        augmented = _augment_for_uplift(instance, str(workdir))
        if augmented.task_description != instance.task_description:
            instance.task_description = augmented.task_description

    def solve(
        self,
        instance: EvalInstance,
        workspace: Path,
        adapter: AgentAdapter,
        **kwargs: Any,
    ) -> EvalResult:
        """Run the agent on the prepared worktree and return the result.

        The agent receives the git worktree as ``working_dir``.  When the
        adapter leaves ``model_patch`` empty the patch is captured from the
        worktree via :func:`_capture_git_diff`.
        """
        workdir = _instance_workdir(instance, workspace)
        # Arm B only: prepend BM25 localization candidates and the grading
        # contract.  Done here rather than in load_instances because it needs the
        # prepared worktree, and done by replacing the field on a copy so the
        # instance the artifacts recorded stays the dataset's own text.
        instance = _augment_for_uplift(instance, str(workdir))
        result = adapter.solve_instance(
            instance, working_dir=str(workdir), **kwargs
        )
        if not result.model_patch:
            result.model_patch = _capture_git_diff(str(workdir))
        result = _retry_if_disqualified(
            instance, result, workdir, adapter, **kwargs
        )
        if not result.instance_id:
            result.instance_id = instance.instance_id
        return result

    def score(
        self,
        result: EvalResult,
        instance: EvalInstance,
        workspace: Path,
    ) -> dict[str, Any]:
        """Write predictions and invoke the official scorer (fail-closed).

        Writes a single-entry ``predictions.jsonl`` for *instance* into
        *workspace*, then runs the OFFICIAL swebench scorer when
        :func:`_can_score_official` reports it available.  ``resolved`` is
        only ever ``True`` when the official scorer's report says so.

        Returns ``{"resolved": bool, "scorer_status": str}`` only when the
        official scorer actually produced a verdict.  When it could not run,
        crashed, or left no report, this raises
        :class:`OfficialScorerUnavailable` so the harness records ERROR_SCORER
        rather than merging an unmeasured ``resolved: False`` into the
        prediction and counting the instance as completed.
        """
        predictions_path = workspace / "predictions.jsonl"
        model_name = os.environ.get("SWEBENCH_MODEL_NAME", "code-agent-default")
        workspace.mkdir(parents=True, exist_ok=True)
        with predictions_path.open("w", encoding="utf-8") as handle:
            handle.write(
                json.dumps(
                    {
                        "instance_id": result.instance_id
                        or instance.instance_id,
                        "model_name_or_path": model_name,
                        "model_patch": result.model_patch,
                    },
                    ensure_ascii=False,
                )
                + "\n"
            )

        available, reason = _can_score_official()
        if not available:
            raise OfficialScorerUnavailable(f"unavailable: {reason}")

        run_id = _scoring_run_id(instance.instance_id)
        # Recorded before the scorer is invoked, so ``_read_official_resolution``
        # can reject any report that predates this request.  See
        # :data:`_SCORING_SESSION` for why both guards exist.
        requested_at = time.time()
        ok, detail = _run_official_scoring(
            str(predictions_path),
            str(workspace),
            dataset_name=self._DATASET_NAME,
            split=self._DATASET_SPLIT,
            run_id=run_id,
        )
        if not ok:
            raise OfficialScorerUnavailable(f"failed: {detail}")

        resolved = _read_official_resolution(
            instance_id=result.instance_id or instance.instance_id,
            run_id=run_id,
            model_name=model_name,
            not_before=requested_at,
        )
        if resolved is None:
            raise OfficialScorerUnavailable(
                f"official run ok but report not found: {detail}"
            )
        payload: dict[str, Any] = {
            "resolved": resolved,
            "scorer_status": f"official: resolved={resolved} ({detail})",
        }
        raw = _collect_official_raw_output(
            instance_id=result.instance_id or instance.instance_id,
            run_id=run_id,
            model_name=model_name,
        )
        if raw:
            payload[SCORER_RAW_OUTPUT_KEY] = raw
        return payload

    # ------------------------------------------------------------------
    # Pins
    # ------------------------------------------------------------------

    @property
    def pins(self) -> dict[str, str]:
        """Reproducibility pins for this benchmark/dataset/scorer."""
        return {
            "benchmark": self.name,
            "dataset_name": self._DATASET_NAME,
            "dataset_revision": self._DATASET_SPLIT,
            "scorer_name": self._SCORER_NAME,
        }


def _instance_workdir(instance: EvalInstance, workspace: Path) -> Path:
    """Return the git worktree for *instance* under *workspace*.

    :func:`_setup_workdir` always nests the repo at ``<base>/<instance_id>``,
    so a harness-provided temp *workspace* is the base dir and the worktree
    is ``workspace/<instance_id>``.  If the driver already passed the
    instance worktree itself, it is returned unchanged.
    """
    nested = workspace / instance.instance_id
    if nested.is_dir():
        return nested
    return workspace


def _collect_official_raw_output(
    instance_id: str,
    run_id: str,
    model_name: str,
) -> dict[str, str]:
    """Read the official harness's own output files, verbatim.

    H5 requires saving official raw output.  These files are what the verdict
    is actually derived from, and the swebench harness writes them relative to
    its working directory rather than into our artifact tree, so without this
    they stayed unpinned by ``checksums.sha256``.

    Returns ``{filename: text}``, skipping anything unreadable — a missing file
    must not mask a verdict that was otherwise obtained, and the caller records
    resolution independently.
    """
    model_safe = model_name.replace("/", "__")
    roots = [Path.cwd(), Path(__file__).resolve().parent.parent.parent]
    collected: dict[str, str] = {}

    for root in roots:
        for candidate in (
            root / "logs" / "run_evaluation" / run_id / model_safe / instance_id,
            root / "logs" / "run_evaluation" / run_id / instance_id,
        ):
            for filename in ("report.json", "run_instance.log", "test_output.txt"):
                fpath = candidate / filename
                if filename in collected or not fpath.is_file():
                    continue
                try:
                    collected[filename] = fpath.read_text(
                        encoding="utf-8", errors="replace"
                    )
                except OSError:
                    continue

    for root in roots:
        summary_path = root / f"{model_safe}.{run_id}.json"
        name = "run-summary.json"
        if name in collected or not summary_path.is_file():
            continue
        try:
            collected[name] = summary_path.read_text(
                encoding="utf-8", errors="replace"
            )
        except OSError:
            continue

    return collected


#: Signatures of a transport fault, i.e. the scorer never reached the point of
#: running a test. Deliberately narrow: anything not listed here is treated as a
#: real scorer failure and reported once, without a retry. Widening this list
#: converts real failures into silently-retried ones, which is how a flaky
#: measurement starts looking like a stable one.
_TRANSIENT_NETWORK_SIGNATURES: tuple[str, ...] = (
    "SSLEOFError",
    "SSLError",
    "UNEXPECTED_EOF_WHILE_READING",
    "Max retries exceeded",
    "ConnectionResetError",
    "ConnectionError",
    "Temporary failure in name resolution",
    "Connection aborted",
    "RemoteDisconnected",
)


def _is_transient_network(output: str) -> bool:
    """Whether *output* shows a transport fault rather than a scoring result."""
    return any(sig in output for sig in _TRANSIENT_NETWORK_SIGNATURES)


def _first_network_signature(output: str) -> str:
    for sig in _TRANSIENT_NETWORK_SIGNATURES:
        if sig in output:
            return sig
    return "unknown"


#: Per-process token mixed into every official scoring ``run_id``.
#:
#: The official harness derives its output paths from ``run_id``, so a ``run_id``
#: that depends only on the instance makes ``logs/run_evaluation/`` a location two
#: different runs *share*.  Sharing it is what let an empty-patch run read an
#: earlier run's ``resolved=True``.  This token makes each process's scoring tree
#: disjoint; :func:`_read_official_resolution`'s ``not_before`` check is the
#: second, independent guard, kept because path isolation alone would silently
#: stop protecting anything the moment someone reintroduces a stable run_id.
_SCORING_SESSION: str = uuid.uuid4().hex[:8]


def _scoring_run_id(instance_id: str) -> str:
    """Return the official scorer's ``run_id`` for *instance_id*, per process."""
    safe = instance_id.replace("/", "__")
    return f"swebench-{safe}-{_SCORING_SESSION}"


def _read_official_resolution(
    instance_id: str,
    run_id: str,
    model_name: str,
    not_before: float | None = None,
) -> bool | None:
    """Read the instance resolution from the official scorer's reports.

    The swebench harness writes, relative to its working directory:

    - ``logs/run_evaluation/<run_id>/<model>/<instance_id>/report.json``
      (per-instance, authoritative ``{"resolved": bool, ...}``), and
    - ``<model>.<run_id>.json`` (run summary with ``resolved_ids``).

    Returns ``True``/``False`` when official evidence is found and ``None``
    when no report exists (the caller treats missing evidence as
    unresolved — fail-closed).

    *not_before* is the wall-clock time at which this run asked for the verdict.
    Any report older than that is evidence about a **different** run and is
    ignored.  Before this existed the scoring ``run_id`` was
    ``f"swebench-{instance_id}"`` — constant across runs — so every run of an
    instance read and overwrote the same ``logs/run_evaluation`` directory, and a
    run whose agent produced *nothing* inherited an earlier run's success:
    ``astropy__astropy-12907`` was recorded ``resolved=True`` with a zero-byte
    patch from a report written hours earlier, while the official summary in the
    same record said ``empty_patch_ids: [12907]``.  For a before/after
    experiment that is fatal, not cosmetic: the arms silently share verdicts.
    """
    model_safe = model_name.replace("/", "__")
    roots = [
        Path.cwd(),
        Path(__file__).resolve().parent.parent.parent,  # repo root
    ]

    def _fresh(path: Path) -> bool:
        """Whether *path* was written for the request now in flight."""
        if not_before is None:
            return True
        try:
            # 2s of slack absorbs filesystem timestamp granularity and the
            # WSL/Windows clock boundary; a stale report is hours old, so this
            # tolerance cannot readmit one.
            return path.stat().st_mtime >= (not_before - 2.0)
        except OSError:
            return False

    # Per-instance report — the authoritative source.
    for root in roots:
        for candidate in (
            root / "logs" / "run_evaluation" / run_id / model_safe / instance_id,
            root / "logs" / "run_evaluation" / run_id / instance_id,
        ):
            report_path = candidate / "report.json"
            if not report_path.is_file() or not _fresh(report_path):
                continue
            try:
                report = json.loads(report_path.read_text(encoding="utf-8"))
            except (json.JSONDecodeError, OSError):
                continue
            entry = report.get(instance_id)
            if isinstance(entry, dict) and "resolved" in entry:
                return bool(entry["resolved"])

    # Run summary fallback.
    for root in roots:
        summary_path = root / f"{model_safe}.{run_id}.json"
        if not summary_path.is_file() or not _fresh(summary_path):
            continue
        try:
            summary = json.loads(summary_path.read_text(encoding="utf-8"))
        except (json.JSONDecodeError, OSError):
            continue
        if not isinstance(summary, dict):
            continue
        if instance_id in summary.get("resolved_ids", []):
            return True
        # error_ids is NOT a verdict: the harness crashed (image build failure,
        # container error) before any test ran, so nothing was measured.
        # Reporting False here claimed the agent's patch had failed when the
        # evaluation never happened.
        if instance_id in summary.get("error_ids", []):
            raise OfficialScorerUnavailable(
                f"official evaluation errored for {instance_id}: the instance is in "
                "error_ids, so no verdict exists (check "
                "logs/run_evaluation/<run_id>/... and logs/build_images/... for the "
                "underlying failure)"
            )
        # These two ARE measurements: the tests ran and the patch did not fix
        # the bug, or the agent submitted nothing to test.
        for key in ("unresolved_ids", "empty_patch_ids"):
            if instance_id in summary.get(key, []):
                return False
    return None


if __name__ == "__main__":
    sys.exit(main())
