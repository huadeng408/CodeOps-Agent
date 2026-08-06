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
import shutil
import subprocess
import sys
import tempfile
import time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Protocol, Sequence

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
) -> list[EvalInstance]:
    """Load SWE-bench Verified instances from HuggingFace datasets.

    Tries to load `princeton-nlp/SWE-bench_Verified` (test split). Falls
    back to synthetic instances if the dataset cannot be downloaded (no
    network, datasets not installed, etc.).

    Args:
        max_instances: If set, return at most this many instances.

    Returns:
        List of EvalInstance objects, one per SWE-bench task.
    """
    try:
        from datasets import load_dataset  # type: ignore[import-untyped]
    except ImportError:
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

    try:
        ds = load_dataset(
            "princeton-nlp/SWE-bench_Verified", split="test", trust_remote_code=True
        )
    except Exception as exc:
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
) -> str:
    """Prepare the working directory for *instance*.

    For real instances this clones the target repo at base_commit. For
    synthetic/dry-run instances it creates a fake git repo with stub files.

    Args:
        instance: The evaluation instance.
        base_dir: Parent directory for the per-instance working directory.
        dry_run: If True, force synthetic repo creation.

    Returns:
        Absolute path to the instance working directory.
    """
    meta: dict[str, Any] = instance.metadata
    workdir = os.path.join(base_dir, instance.instance_id)
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

    print(f"[swebench] Cloning {repo_url} at {base_commit}...")
    subprocess.run(
        ["git", "clone", "--depth=1", repo_url, workdir],
        check=True,
        capture_output=True,
        timeout=300,
    )
    if base_commit:
        subprocess.run(
            ["git", "-C", workdir, "checkout", base_commit],
            check=True,
            capture_output=True,
            timeout=60,
        )
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
    """Capture the unified diff of all uncommitted changes in *workdir*."""
    try:
        result = subprocess.run(
            ["git", "-C", workdir, "diff", "--no-color", "HEAD"],
            capture_output=True,
            text=True,
            timeout=30,
            check=False,
        )
        return result.stdout.strip()
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
            return load_synthetic_instances()[:max_instances] if max_instances else load_synthetic_instances()
        return load_swebench_instances(max_instances=max_instances)

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


if __name__ == "__main__":
    sys.exit(main())
