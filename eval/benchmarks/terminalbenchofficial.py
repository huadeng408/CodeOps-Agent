"""Pinned receipt boundary for real Terminal-Bench official runs.

The official ``terminal_bench.Harness`` remains responsible for execution and
scoring.  This module only validates the fixed public input and preserves its
raw output in the repository's canonical artifact shape.
"""

from __future__ import annotations

import hashlib
import json
import os
import subprocess
from collections.abc import Mapping
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from eval.benchmarks._official_execution import (
    OfficialExecutionEvidence,
    OfficialRunExecution,
)

# Terminal-Bench scores terminal task resolution and has no retrieval stage.
TRACE_CAPABILITIES: tuple[str, ...] = ()


TERMINAL_BENCH_PACKAGE = "terminal-bench"
TERMINAL_BENCH_PACKAGE_VERSION = "0.2.18"
TERMINAL_BENCH_TASK_ID = "break-filter-js-from-html"
TERMINAL_BENCH_DATASET_SHA256 = (
    "4bab83828d145cdb8378eea9f00c2fec5c90b32f3f4cd076c4111d6e4191c353"
)
TERMINAL_BENCH_TASK_TREE_SHA256 = (
    "75c8217ef7411e7fa1894158cb7648a59abe9e3c0a6a1c01aae7a6605609c1be"
)
_HEX64 = frozenset("0123456789abcdef")
_TREE_IGNORED_PARTS = frozenset({".git", "__pycache__"})
_TREE_IGNORED_SUFFIXES = frozenset({".pyc", ".pyo"})
_PROCESS_OK = frozenset({"completed", "complete", "success", "succeeded", "ok", "done", "finished", "exited"})


def task_tree_sha256(root: Path | str) -> str:
    """Return a deterministic hash for the complete Terminal-Bench task tree.

    The digest binds relative POSIX paths and file bytes, rather than relying
    on directory metadata or platform-specific path separators.  Python cache
    files and VCS internals are excluded because they are generated runtime
    state, not benchmark inputs.  Symlinks are represented by their target
    text and are never followed outside the tree.
    """
    digest, _ = _task_tree_snapshot(Path(root))
    return digest


def _task_tree_snapshot(root: Path) -> tuple[str, int]:
    if not root.is_dir():
        raise FileNotFoundError(f"task tree does not exist: {root}")

    entries: list[tuple[str, bytes]] = []
    for path in root.rglob("*"):
        relative = path.relative_to(root)
        if any(part in _TREE_IGNORED_PARTS for part in relative.parts):
            continue
        if path.suffix.lower() in _TREE_IGNORED_SUFFIXES:
            continue
        relative_name = relative.as_posix()
        if path.is_symlink():
            entries.append((relative_name, b"SYMLINK\0" + os.readlink(path).encode("utf-8", "surrogateescape")))
        elif path.is_file():
            entries.append((relative_name, path.read_bytes()))

    entries.sort(key=lambda item: item[0])
    digest = hashlib.sha256()
    for relative_name, payload in entries:
        name_bytes = relative_name.encode("utf-8", "surrogateescape")
        digest.update(len(name_bytes).to_bytes(8, "big"))
        digest.update(name_bytes)
        digest.update(len(payload).to_bytes(8, "big"))
        digest.update(payload)
    return digest.hexdigest(), len(entries)


@dataclass(frozen=True)
class TerminalBenchOfficialConfig:
    dataset_root: Path
    dataset_sha256: str
    package_version: str
    model: str
    task_id: str
    max_concurrency: int = 1
    n_attempts: int = 1
    # The receipt boundary must execute one pre-declared official driver.  A
    # missing pin would let callers attach arbitrary output-producing commands
    # to an otherwise valid benchmark receipt.
    official_command: tuple[str, ...] | None = None
    # Optional expected hash for the entire ``tasks/<task>`` tree.  When it is
    # omitted, collection still computes and records the actual hash.
    task_tree_sha256: str | None = None
    expected_task_tree_sha256: str | None = None
    # Optional immutable upstream revision/tag supplied by the operator.
    upstream_pin: str | None = None


class TerminalBenchOfficialRunner:
    """Validate and archive one fixed official Terminal-Bench result."""

    def __init__(self, config: TerminalBenchOfficialConfig) -> None:
        self.config = config
        self._execution_token = object()

    @property
    def pins(self) -> dict[str, str]:
        pins = {
            "benchmark": "terminal-bench",
            "dataset_name": "terminal-bench-v2 offline public snapshot",
            "dataset_revision": self.config.dataset_sha256,
            "scorer_name": "terminal_bench.Harness results.json",
            "runner_version": self.config.package_version,
        }
        expected_tree_hash = self._expected_task_tree_sha256()
        if expected_tree_hash:
            pins["task_tree_sha256"] = expected_tree_hash
        if self.config.upstream_pin:
            pins["upstream_pin"] = self.config.upstream_pin
        return pins

    def validate(self) -> list[str]:
        problems: list[str] = []
        if not self.config.dataset_root.is_dir():
            problems.append("dataset_root does not exist")
        if len(self.config.dataset_sha256) != 64 or any(
            character not in "0123456789abcdef" for character in self.config.dataset_sha256
        ):
            problems.append("dataset_sha256 must be a lowercase 64-character SHA-256")
        for field_name, value in (
            ("task_tree_sha256", self.config.task_tree_sha256),
            ("expected_task_tree_sha256", self.config.expected_task_tree_sha256),
        ):
            if value is not None and (
                len(value) != 64 or any(character not in _HEX64 for character in value)
            ):
                problems.append(f"{field_name} must be a lowercase 64-character SHA-256")
        if (
            self.config.task_tree_sha256 is not None
            and self.config.expected_task_tree_sha256 is not None
            and self.config.task_tree_sha256 != self.config.expected_task_tree_sha256
        ):
            problems.append("task tree pins must agree")
        if self.config.upstream_pin is not None and not self.config.upstream_pin.strip():
            problems.append("upstream_pin must not be empty")
        if not self.config.package_version:
            problems.append("package_version is required")
        if "/" not in self.config.model:
            problems.append("model must include a LiteLLM provider prefix")
        if not self.config.task_id:
            problems.append("task_id is required")
        if not 1 <= self.config.max_concurrency <= 10:
            problems.append("max_concurrency must be between 1 and 10")
        if not 1 <= self.config.n_attempts <= 10:
            problems.append("n_attempts must be between 1 and 10")
        if self.config.official_command is not None and (
            not self.config.official_command
            or any(
                not isinstance(argument, str) or not argument
                for argument in self.config.official_command
            )
        ):
            problems.append("official_command must contain non-empty strings")
        return problems

    def prepare_run(self, official_run_dir: Path) -> OfficialRunExecution:
        """Create a session that can bind one official Harness invocation."""
        problems = self.validate()
        if problems:
            raise ValueError("; ".join(problems))
        if self.config.official_command is None:
            raise ValueError("official command pin is required")
        run_dir = Path(official_run_dir).absolute()
        return OfficialRunExecution(
            owner=self,
            token=self._execution_token,
            output_path=run_dir / "results.json",
            output_root=run_dir.parent,
            required_output_paths=(run_dir / "run_metadata.json",),
            optional_output_paths=(
                run_dir / "tb.lock",
                run_dir / "b.lock",
                run_dir / "process-status.json",
                run_dir / "process_status.json",
                run_dir / "exit-code.txt",
                run_dir / "exit_code.txt",
            ),
            default_command=self.config.official_command,
            default_cwd=None,
            process_runner=subprocess.run,
        )

    def collect_receipt(
        self,
        execution: object,
        artifact_root: Path,
        **legacy_evidence: object,
    ) -> dict[str, Any]:
        """Copy upstream raw JSON and summarize its official resolution.

        The run directory must have been produced by ``terminal_bench.Harness``.
        Missing or malformed output fails closed; no local scorer is used.
        """
        if legacy_evidence:
            if legacy_evidence.get("result_existed_before") is True:
                raise RuntimeError("official output already existed before this run")
            claimed_returncode = legacy_evidence.get("process_returncode")
            if (
                isinstance(claimed_returncode, int)
                and not isinstance(claimed_returncode, bool)
                and claimed_returncode != 0
            ):
                raise RuntimeError(
                    f"official process exited with non-zero exit code {claimed_returncode}"
                )
            raise ValueError(
                "caller-provided process evidence is not accepted; use runner-issued execution"
            )
        if type(execution) is not OfficialRunExecution:
            raise TypeError(
                "receipt requires runner-issued execution session; process_returncode is not accepted"
            )
        problems = self.validate()
        if problems:
            raise ValueError("; ".join(problems))
        evidence = execution.evidence(owner=self, token=self._execution_token)
        raw_results = evidence.output_path
        official_run_dir = raw_results.parent
        raw_metadata = official_run_dir / "run_metadata.json"
        dataset_hash, tree_hash, tree_file_count = self.validate_input_pins()
        receipt_base = {
            "benchmark": "terminal-bench",
            "package": TERMINAL_BENCH_PACKAGE,
            "package_version": self.config.package_version,
            "dataset_root": self.config.dataset_root.as_posix(),
            "dataset_path": self._task_tree_root().resolve(strict=False).as_posix(),
            "dataset_sha256": self.config.dataset_sha256,
            "dataset_file_sha256": dataset_hash,
            "task_tree_sha256": tree_hash,
            "task_tree_file_count": tree_file_count,
            "model": self.config.model,
            "task_ids": [self.config.task_id],
            "max_concurrency": self.config.max_concurrency,
            "n_attempts": self.config.n_attempts,
            "expected_trial_count": self.config.n_attempts,
        }
        if evidence.returncode != 0 or evidence.missing_required_outputs:
            scorer_dir = artifact_root / "scorer"
            copied_results: Path | None = None
            copied_metadata: Path | None = None
            if evidence.output_bytes is not None:
                scorer_dir.mkdir(parents=True, exist_ok=True)
                copied_results = scorer_dir / "terminalbench-results.json"
                copied_results.write_bytes(evidence.output_bytes)
            try:
                metadata_bytes = evidence.bytes_for(raw_metadata)
            except FileNotFoundError:
                metadata_bytes = None
            if metadata_bytes is not None:
                scorer_dir.mkdir(parents=True, exist_ok=True)
                copied_metadata = scorer_dir / "terminalbench-run-metadata.json"
                copied_metadata.write_bytes(metadata_bytes)
            metadata_name = raw_metadata.relative_to(evidence.output_root).as_posix()
            receipt = {
                **receipt_base,
                "status": "OFFICIAL_INFRA_FAILURE",
                "failure_mode": (
                    "OFFICIAL_PROCESS_EXIT_NONZERO"
                    if evidence.returncode != 0
                    else "OFFICIAL_REQUIRED_OUTPUT_MISSING"
                ),
                "process_status": "failed",
                "process_exit_code": evidence.returncode,
                "observed_trial_count": 0,
                "failed_trial_count": self.config.n_attempts,
                "missing_required_outputs": list(evidence.missing_required_outputs),
                "observed_required_output_sha256": dict(evidence.required_output_sha256),
                "official_output_sha256": evidence.output_sha256,
                "official_metadata_sha256": evidence.required_output_sha256.get(
                    metadata_name
                ),
                "official_output": (
                    copied_results.as_posix() if copied_results is not None else None
                ),
                "official_metadata": (
                    copied_metadata.as_posix() if copied_metadata is not None else None
                ),
            }
            if self.config.upstream_pin is not None:
                receipt["upstream_pin"] = self.config.upstream_pin
            artifact_root.mkdir(parents=True, exist_ok=True)
            (artifact_root / "receipt.json").write_text(
                json.dumps(receipt, indent=2, ensure_ascii=False) + "\n",
                encoding="utf-8",
            )
            _write_checksums(artifact_root)
            return receipt
        if evidence.output_bytes is None or evidence.output_sha256 is None:
            raise RuntimeError("official execution output evidence is incomplete")
        try:
            parsed = json.loads(evidence.output_bytes)
        except (UnicodeDecodeError, json.JSONDecodeError) as exc:
            raise ValueError("official Terminal-Bench results.json is not valid JSON") from exc
        metadata_bytes = evidence.bytes_for(raw_metadata)
        try:
            metadata = json.loads(metadata_bytes)
        except (UnicodeDecodeError, json.JSONDecodeError) as exc:
            raise ValueError("official Terminal-Bench run_metadata.json is not valid JSON") from exc
        if not isinstance(metadata, Mapping):
            raise TypeError("official Terminal-Bench run_metadata.json must be an object")
        lock, lock_file, lock_sha256 = _load_optional_lock(evidence, official_run_dir)
        self._validate_run_identity(metadata, lock, official_run_dir)
        trials = parsed.get("results") if isinstance(parsed, Mapping) else None
        if not isinstance(trials, list):
            raise TypeError("official Terminal-Bench results.json has no results list")
        if any(not isinstance(trial, Mapping) for trial in trials):
            raise ValueError("official Terminal-Bench results contain a malformed trial")
        result_task_ids = {trial.get("task_id") for trial in trials}
        if result_task_ids != {self.config.task_id}:
            raise ValueError("official Terminal-Bench result task ids do not match configured task")
        task_trials = [trial for trial in trials if isinstance(trial, dict) and trial.get("task_id") == self.config.task_id]
        if len(task_trials) != 1:
            raise ValueError("official Terminal-Bench receipt must contain exactly one configured task")

        process_status, process_exit_code = _validate_process_status(
            official_run_dir,
            metadata,
            parsed,
            task_trials[0],
            process_returncode=evidence.returncode,
            sidecar_bytes={
                name: payload
                for name in (
                    "process-status.json",
                    "process_status.json",
                    "exit-code.txt",
                    "exit_code.txt",
                )
                if (payload := evidence.optional_bytes_for(official_run_dir / name))
                is not None
            },
        )

        # Re-read the source tree immediately before copying.  If a task file
        # changed during validation, refuse to create a receipt bound to a
        # moving input.
        current_tree_hash, current_tree_file_count = self._task_tree_pin_snapshot()
        if (current_tree_hash, current_tree_file_count) != (tree_hash, tree_file_count):
            raise ValueError("task tree changed during receipt collection")

        scorer_dir = artifact_root / "scorer"
        scorer_dir.mkdir(parents=True, exist_ok=True)
        copied_results = scorer_dir / "terminalbench-results.json"
        copied_metadata = scorer_dir / "terminalbench-run-metadata.json"
        copied_results.write_bytes(evidence.output_bytes)
        copied_metadata.write_bytes(metadata_bytes)

        trial = task_trials[0]
        status = "OFFICIAL_PASS" if trial.get("is_resolved") is True else "OFFICIAL_FAILURE"
        receipt = {
            **receipt_base,
            "status": status,
            "failure_mode": trial.get("failure_mode"),
            "process_status": process_status,
            "process_exit_code": process_exit_code,
            "observed_trial_count": len(task_trials),
            "failed_trial_count": 0 if status == "OFFICIAL_PASS" else len(task_trials),
            "missing_required_outputs": [],
            "observed_required_output_sha256": dict(evidence.required_output_sha256),
            "official_output_sha256": evidence.output_sha256,
            "official_metadata_sha256": hashlib.sha256(metadata_bytes).hexdigest(),
            "official_output": copied_results.as_posix(),
        }
        if self.config.upstream_pin is not None:
            receipt["upstream_pin"] = self.config.upstream_pin
        if lock_file is not None and lock_sha256 is not None:
            receipt["official_lock_file"] = lock_file
            receipt["official_lock_sha256"] = lock_sha256
        (artifact_root / "receipt.json").write_text(
            json.dumps(receipt, indent=2, ensure_ascii=False) + "\n", encoding="utf-8"
        )
        _write_checksums(artifact_root)
        return receipt

    def validate_input_pins(self) -> tuple[str, str, int]:
        """Validate immutable dataset inputs without starting the benchmark."""
        problems = self.validate()
        if problems:
            raise ValueError("; ".join(problems))
        dataset_hash = self._validate_dataset_pin()
        tree_hash, tree_file_count = self._validate_task_tree_pin()
        return dataset_hash, tree_hash, tree_file_count

    def _task_tree_root(self) -> Path:
        candidate = self.config.dataset_root / "tasks"
        return candidate if candidate.is_dir() else self.config.dataset_root

    def _task_tree_pin_snapshot(self) -> tuple[str, int]:
        try:
            return _task_tree_snapshot(self._task_tree_root())
        except (FileNotFoundError, OSError) as exc:
            raise ValueError("task tree does not exist") from exc

    def _validate_task_tree_pin(self) -> tuple[str, int]:
        actual_hash, file_count = self._task_tree_pin_snapshot()
        expected = self._expected_task_tree_sha256()
        if expected is not None and actual_hash != expected:
            raise ValueError("task tree hash does not match expected pin")
        return actual_hash, file_count

    def _expected_task_tree_sha256(self) -> str | None:
        return self.config.expected_task_tree_sha256 or self.config.task_tree_sha256

    def _validate_dataset_pin(self) -> str:
        roots = [self.config.dataset_root]
        if self.config.dataset_root.name.lower() == "tasks":
            roots.append(self.config.dataset_root.parent)
        candidates = [root / "terminalbench_2.jsonl" for root in roots]
        candidates = [path for path in candidates if path.is_file()]
        if not candidates:
            candidates = sorted(
                path for root in roots for path in root.glob("*.jsonl") if path.is_file()
            )
        if not candidates:
            raise ValueError("Terminal-Bench dataset file is missing")
        actual = _sha256(candidates[0])
        if actual != self.config.dataset_sha256:
            raise ValueError("dataset hash does not match configured pin")
        return actual

    def _validate_run_identity(
        self,
        metadata: Mapping[str, Any],
        lock: Mapping[str, Any] | None,
        official_run_dir: Path,
    ) -> None:
        expected_dataset_path = self._task_tree_root().resolve(strict=False)

        def validate_dataset_path(value: Any, label: str) -> None:
            if not isinstance(value, str) or not value.strip():
                raise ValueError(f"official Terminal-Bench {label} dataset path is invalid")
            observed = Path(value).resolve(strict=False)
            if observed != expected_dataset_path:
                raise ValueError(
                    f"official Terminal-Bench {label} dataset path does not match configured pin"
                )

        run_id = metadata.get("run_id")
        if isinstance(run_id, str) and run_id and run_id != official_run_dir.name:
            raise ValueError("run metadata run_id does not match output directory")

        task_ids = metadata.get("task_ids")
        if task_ids is not None and (
            not isinstance(task_ids, list) or set(task_ids) != {self.config.task_id}
        ):
            raise ValueError("run metadata task ids do not match configured task")
        n_tasks = metadata.get("n_tasks")
        if n_tasks is not None and n_tasks not in (1,):
            raise ValueError("run metadata n_tasks does not match configured task")
        concurrency = metadata.get("n_concurrent_trials")
        if concurrency is not None and concurrency != self.config.max_concurrency:
            raise ValueError("run metadata concurrency does not match configured pin")
        attempts = metadata.get("n_attempts")
        if attempts is not None and attempts != self.config.n_attempts:
            raise ValueError("run metadata attempts do not match configured pin")

        official_shape = any(
            key in metadata for key in ("run_id", "uuid", "start_time", "output_path")
        )
        metadata_dataset_path = metadata.get("dataset_path")
        if metadata_dataset_path is not None:
            validate_dataset_path(metadata_dataset_path, "run metadata")
        elif official_shape:
            raise ValueError("official Terminal-Bench run metadata dataset path is missing")
        if official_shape and lock is None:
            raise ValueError("official Terminal-Bench lock file is missing")

        if lock is not None:
            dataset = lock.get("dataset")
            if not isinstance(dataset, Mapping):
                raise ValueError("official Terminal-Bench lock dataset is invalid")
            lock_task_ids = dataset.get("task_ids")
            if not isinstance(lock_task_ids, list) or set(lock_task_ids) != {
                self.config.task_id
            }:
                raise ValueError("run lock task ids do not match configured task")
            validate_dataset_path(dataset.get("local_path"), "lock")
            run_config = lock.get("run_config")
            if not isinstance(run_config, Mapping):
                raise ValueError("official Terminal-Bench lock run config is invalid")
            if run_config.get("n_concurrent_trials") != self.config.max_concurrency:
                raise ValueError("run lock concurrency does not match configured pin")
            lock_attempts = run_config.get("n_attempts")
            if lock_attempts is not None and lock_attempts != self.config.n_attempts:
                raise ValueError("run lock attempts do not match configured pin")
            local_config = lock.get("local_config")
            if isinstance(local_config, Mapping):
                lock_run_id = local_config.get("run_id")
                if isinstance(lock_run_id, str) and lock_run_id != official_run_dir.name:
                    raise ValueError("run lock run_id does not match output directory")

        model_values = _model_values(metadata, lock)
        if official_shape and not model_values:
            raise ValueError("run metadata model identity is missing")
        if model_values and any(
            not _model_identity_matches(self.config.model, value) for value in model_values
        ):
            raise ValueError("run metadata model does not match configured model")

        package_values, package_names = _package_values(metadata, lock)
        if official_shape and (not package_values or not package_names):
            raise ValueError("run metadata package identity is missing")
        if package_names and any(value != TERMINAL_BENCH_PACKAGE for value in package_names):
            raise ValueError("run metadata package does not match terminal-bench")
        if package_values and any(value != self.config.package_version for value in package_values):
            raise ValueError("run metadata package version does not match configured package")

        if self.config.upstream_pin is not None:
            upstream_values = _upstream_values(metadata, lock)
            if self.config.upstream_pin not in upstream_values:
                raise ValueError("upstream pin does not match configured upstream pin")


def _load_optional_lock(
    evidence: OfficialExecutionEvidence, run_dir: Path
) -> tuple[Mapping[str, Any] | None, str | None, str | None]:
    """Load the official lock when present, without requiring legacy fixtures."""
    for name in ("tb.lock", "b.lock"):
        payload_bytes = evidence.optional_bytes_for(run_dir / name)
        if payload_bytes is None:
            continue
        try:
            payload = json.loads(payload_bytes)
        except (UnicodeDecodeError, json.JSONDecodeError) as exc:
            raise ValueError("official Terminal-Bench lock file is not valid JSON") from exc
        if not isinstance(payload, Mapping):
            raise TypeError("official Terminal-Bench lock file must be an object")
        return payload, name, hashlib.sha256(payload_bytes).hexdigest()
    return None, None, None


def _model_values(metadata: Mapping[str, Any], lock: Mapping[str, Any] | None) -> list[str]:
    values: list[str] = []

    def add(value: Any) -> None:
        if isinstance(value, str) and value.strip():
            values.append(value.strip())

    for key in ("model_name", "model", "model_name_or_path"):
        add(metadata.get(key))
    agent_kwargs = metadata.get("agent_kwargs")
    if isinstance(agent_kwargs, Mapping):
        for key in ("model", "model_name", "model_name_or_path"):
            add(agent_kwargs.get(key))
    if lock is not None:
        agent = lock.get("agent")
        if isinstance(agent, Mapping):
            add(agent.get("model_name"))
            extra_kwargs = agent.get("extra_kwargs")
            if isinstance(extra_kwargs, Mapping):
                for key in ("model", "model_name", "model_name_or_path"):
                    add(extra_kwargs.get(key))
    return values


def _model_identity_matches(expected: str, observed: str) -> bool:
    """Match the 0.2.18 custom-agent model without widening provider identity.

    The receipt config uses LiteLLM's ``provider/model`` form.  The official
    0.2.18 lock stores a custom agent's constructor kwarg as the bare model.
    Only that exact one-prefix omission is accepted; a different provider or a
    different model remains a mismatch.
    """
    if observed == expected:
        return True
    provider, separator, bare_model = expected.partition("/")
    return bool(provider and separator and "/" not in observed and observed == bare_model)


def _package_values(
    metadata: Mapping[str, Any], lock: Mapping[str, Any] | None
) -> tuple[list[str], list[str]]:
    versions: list[str] = []
    names: list[str] = []

    def add_version(value: Any) -> None:
        if isinstance(value, str) and value.strip():
            versions.append(value.strip())

    def add_name(value: Any) -> None:
        if isinstance(value, str) and value.strip():
            names.append(value.strip())

    for key in ("package_version", "terminal_bench_version", "harness_version"):
        add_version(metadata.get(key))
    add_name(metadata.get("package"))
    harness = metadata.get("harness")
    if isinstance(harness, Mapping):
        add_name(harness.get("package"))
        add_version(harness.get("version"))
    if lock is not None:
        lock_harness = lock.get("harness")
        if isinstance(lock_harness, Mapping):
            add_name(lock_harness.get("package"))
            add_version(lock_harness.get("version"))
    return versions, names


def _upstream_values(metadata: Mapping[str, Any], lock: Mapping[str, Any] | None) -> list[str]:
    values: list[str] = []

    def add(value: Any) -> None:
        if isinstance(value, str) and value.strip():
            values.append(value.strip())

    for key in (
        "upstream_pin",
        "upstream_revision",
        "upstream_commit",
        "dataset_revision",
        "dataset_version",
    ):
        add(metadata.get(key))
    if lock is not None:
        dataset = lock.get("dataset")
        if isinstance(dataset, Mapping):
            for key in ("version", "revision", "upstream_pin", "upstream_revision"):
                add(dataset.get(key))
        for key in ("upstream_pin", "upstream_revision", "upstream_commit"):
            add(lock.get(key))
    return values


def _validate_process_status(
    run_dir: Path,
    metadata: Mapping[str, Any],
    results: Mapping[str, Any],
    trial: Mapping[str, Any],
    *,
    process_returncode: int | None,
    sidecar_bytes: Mapping[str, bytes] | None = None,
) -> tuple[str, int | None]:
    """Reject a receipt whose official process failed or is still running."""
    statuses: list[str] = []
    exit_codes: list[int] = []
    if process_returncode is not None:
        exit_codes.append(process_returncode)

    def inspect(value: Any) -> None:
        if isinstance(value, Mapping):
            status = value.get("status")
            if isinstance(status, str) and status.strip():
                statuses.append(status.strip().lower())
            for key in ("exit_code", "return_code", "returncode"):
                code = value.get(key)
                if isinstance(code, int) and not isinstance(code, bool):
                    exit_codes.append(code)

    for source in (metadata, results, trial):
        for key in ("process_status", "process"):
            value = source.get(key) if isinstance(source, Mapping) else None
            if isinstance(value, str) and value.strip():
                statuses.append(value.strip().lower())
            else:
                inspect(value)
        for key in ("exit_code", "return_code", "returncode"):
            code = source.get(key) if isinstance(source, Mapping) else None
            if isinstance(code, int) and not isinstance(code, bool):
                exit_codes.append(code)

    if "start_time" in metadata or "run_id" in metadata:
        end_time = metadata.get("end_time")
        if not isinstance(end_time, str) or not end_time.strip():
            raise ValueError("official Terminal-Bench process status is incomplete")
        statuses.append("completed")

    # Some wrappers write the process result as a tiny sidecar next to the
    # official JSON.  It is optional for compatibility, but authoritative when
    # present.
    for name in ("process-status.json", "process_status.json", "exit-code.txt", "exit_code.txt"):
        payload = (sidecar_bytes or {}).get(name)
        if payload is None:
            continue
        try:
            raw = payload.decode("utf-8").strip()
        except UnicodeDecodeError as exc:
            raise ValueError("official Terminal-Bench process status is unreadable") from exc
        if name.endswith(".json"):
            try:
                sidecar = json.loads(raw)
            except json.JSONDecodeError as exc:
                raise ValueError("official Terminal-Bench process status is invalid JSON") from exc
            if isinstance(sidecar, str):
                statuses.append(sidecar.strip().lower())
            else:
                inspect(sidecar)
        elif raw:
            try:
                exit_codes.append(int(raw))
            except ValueError:
                statuses.append(raw.lower())

    if any(status not in _PROCESS_OK for status in statuses):
        raise ValueError("official Terminal-Bench process status indicates failure")
    if any(code != 0 for code in exit_codes):
        raise ValueError("official Terminal-Bench process status has non-zero exit code")

    failure_mode = trial.get("failure_mode")
    resolved = trial.get("is_resolved")
    if resolved is True and isinstance(failure_mode, str):
        normalized_failure = failure_mode.strip().lower()
        if normalized_failure not in {"", "none", "unset"}:
            raise ValueError("official Terminal-Bench process status indicates trial failure")

    status = statuses[-1] if statuses else "unspecified"
    return status, exit_codes[-1] if exit_codes else None


def assert_fresh_run_result(
    result_path: Path | str,
    *,
    returncode: int | None,
    existed_before: bool,
) -> Path:
    """Fail before parsing an output that may be stale or from a failed run."""
    path = Path(result_path)
    if existed_before:
        raise RuntimeError("official Terminal-Bench results.json already existed before this run")
    if returncode is None:
        raise RuntimeError("official Terminal-Bench process exit code is unavailable")
    if returncode != 0:
        raise RuntimeError(f"official Terminal-Bench process exited with non-zero exit code {returncode}")
    if not path.is_file():
        raise RuntimeError("official Terminal-Bench process produced no fresh results.json")
    return path


def _sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def _write_checksums(root: Path) -> None:
    entries = [
        f"{_sha256(path)}  {path.relative_to(root).as_posix()}"
        for path in sorted(root.rglob("*"))
        if path.is_file() and path.name != "checksums.sha256"
    ]
    (root / "checksums.sha256").write_text("\n".join(entries) + "\n", encoding="utf-8")
