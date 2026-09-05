"""Eval harness runner (plan Task 8.1 / Phase 4 rewrite).

Drives one instance at a time with: independent workspace (temp dir per
instance), budget enforcement, checkpoint resume (completed instances are
never re-run), error classification (timeout/OOM/infra/agent/scorer) and
canonical artifacts. Network is OFF by default; the benchmark explicitly
enables it via allowlist when needed.

Phase 4 (2026-08-08): Replaced dead ``InstanceRunner(dict)`` protocol with
``AgentAdapter.solve_instance(EvalInstance, working_dir, **kwargs) →
EvalResult``.  Added scorer callback, pre-start budget check, fail-closed
on missing instance_id, workspace preservation on failure, and reachable
ERROR_SCORER.

H4 (2026-08-09): WORKSPACE_PRESERVED markers on failure, advisory
max_processes budget enforcement (budget.py), and best-effort
HTTP(S)_PROXY pinning in :func:`_block_network` (real isolation remains
the benchmark's Docker/container responsibility).
"""

from __future__ import annotations

import contextlib
import hashlib
import json
import os
import shutil
import subprocess
import tempfile
import threading
import time
from collections.abc import Callable, Iterator, Sequence
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Protocol

from eval.adapter import AgentAdapter, EvalInstance, EvalResult
from eval.harness.artifacts import RunArtifacts
from eval.harness.budget import (
    Budget,
    BudgetExceeded,
    BudgetUsage,
    budget_contract,
    budget_contract_sha256,
    check_budget,
)
from eval.harness.pin_contract import evaluate_pins, system_prompt_pin
from eval.harness.redaction import redact_credential_text
from eval.harness.trace_capture import TraceCapture
from eval.harness.trace_contract import (
    SPAN_EVAL_INSTANCE,
    SPAN_EVAL_RUN,
    SPAN_SCORER_OFFICIAL,
    evaluate_trace_contract,
)
from eval.harness.trace_join import eval_join_context

# Error taxonomy — infra failures are never counted as model failures and are
# never silently removed from the denominator.
ERROR_TIMEOUT = "timeout"
ERROR_OOM = "oom"
ERROR_INFRA = "infra"
ERROR_AGENT = "agent"
ERROR_SCORER = "scorer"
#: The harness stopped the agent to protect a budget. This is emphatically NOT
#: ``ERROR_AGENT``: the agent did not try and fail, it was cut off. Routing token
#: exhaustion to ``ERROR_AGENT`` produced a 20-instance run that reported
#: "5 ok, 15 failed, agent: 14" when what actually happened was that a run-level
#: 500k token cap — sized for a single instance — ran out after the sixth. Read
#: literally, that summary said the model failed 14 times; it never saw 14 of
#: them. Same shape as every other defect in this family: a harness-side
#: condition wearing a verdict's clothing.
ERROR_BUDGET = "budget"

# Reserved key a scorer callback may return to hand the harness the official
# harness's raw output as ``{filename: content}``.  It is popped from the
# scorer result and written under ``scorer/`` so ``checksums.sha256`` pins it;
# it never appears as a field in ``predictions.jsonl``.
SCORER_RAW_OUTPUT_KEY = "scorer_raw_output"

# Marker files written into an instance workspace to record execution policy
# and post-mortem state (post-mortem tooling greps for these by name).
NETWORK_DISABLED_MARKER = "NETWORK_DISABLED"
WORKSPACE_PRESERVED_MARKER = "WORKSPACE_PRESERVED"

# O2 trace artifact filenames under ``traces/``.
TRACE_SUMMARY_FILENAME = "trace-summary.json"
SPAN_ASSERTION_FILENAME = "span-assertion.json"

# Scorer callback: called after each successful solve_instance, receiving
# the instance workspace as well (where the benchmark adapter writes its own
# artifacts, e.g. predictions.jsonl / score.json sidecars).  Returns a dict
# that gets merged into the prediction artifact.
class ScorerCallback(Protocol):
    def __call__(
        self,
        result: EvalResult,
        instance: EvalInstance,
        workspace: Path,
        *,
        timeout_s: float,
    ) -> dict[str, Any]: ...

# Workspace setup callback: called before each solve_instance to populate
# the working directory (e.g. clone a repo, checkout a commit).  Receives
# the instance and the temp directory path the harness created.
WorkspaceSetup = Callable[[EvalInstance, str], None]


class AdapterResultError(RuntimeError):
    """An adapter returned an explicit failure instead of raising it."""


CHECKPOINT_CONTRACT_KIND = "checkpoint-contract"
CHECKPOINT_COMPLETED_KIND = "completed"
CHECKPOINT_VERSION = 1
CHECKPOINT_LOCK_TIMEOUT_SECONDS = 30.0


def _stable_json_sha256(value: Any) -> str:
    payload = json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=True)
    return hashlib.sha256(payload.encode("utf-8")).hexdigest()


@contextlib.contextmanager
def _exclusive_checkpoint_lock(path: Path) -> Iterator[None]:
    """Serialize checkpoint upgrades/appends across threads and processes."""
    lock_path = path.with_name(path.name + ".lock")
    deadline = time.monotonic() + CHECKPOINT_LOCK_TIMEOUT_SECONDS
    acquired = False
    while not acquired:
        try:
            # A directory is the lock token.  Unlike an O_EXCL file, releasing
            # it with rmdir cannot race with a contender that creates the next
            # token: there is no open file descriptor to unlink on Windows.
            lock_path.mkdir()
            acquired = True
        except FileExistsError:
            if time.monotonic() >= deadline:
                raise TimeoutError("checkpoint lock acquisition timed out")
            time.sleep(0.01)
    try:
        yield
    finally:
        try:
            lock_path.rmdir()
        except FileNotFoundError:
            pass


def classify_error(exc: BaseException) -> str:
    if isinstance(exc, ScorerError):
        return ERROR_SCORER
    if isinstance(exc, BudgetExceeded):
        if exc.kind == "wall-clock":
            return ERROR_TIMEOUT
        if exc.kind == "output":
            # Kept as OOM for continuity with existing artifacts: an output-byte
            # blowout is at least named after a resource rather than blamed on
            # the model.
            return ERROR_OOM
        if exc.kind in ("tokens", "cost"):
            # Cumulative, run-wide pools. By the time instance 7 fails on these,
            # they were drained by instances 1-6, so the failure says nothing
            # about instance 7's agent. See ERROR_BUDGET.
            return ERROR_BUDGET
        if exc.kind not in ("processes",):
            # Unknown resource kinds are Harness-side budget failures.  Keep
            # them out of the model denominator until the taxonomy is updated
            # deliberately; silently calling them agent errors is fail-open.
            return ERROR_BUDGET
        # Everything else, notably "processes": a per-instance, instantaneous cap
        # that the agent's own behaviour hit. Attributing that to the agent is
        # correct — it really did try to spawn past the limit.
        return ERROR_AGENT
    if isinstance(exc, subprocess.TimeoutExpired) or isinstance(exc, TimeoutError):
        return ERROR_TIMEOUT
    if isinstance(exc, MemoryError):
        return ERROR_OOM
    if "connection" in str(exc).lower() or "network" in str(exc).lower():
        return ERROR_INFRA
    return ERROR_AGENT


def _get_tracer():
    """Return an OTel tracer, or ``None`` when telemetry is unavailable.

    Mirrors ``orchestrator/runtime/conversation.py``'s defensive pattern: a
    benchmark run must never fail because the OTel SDK is missing (§9.3 — the
    business path degrades normally when the exporter is off).
    """
    try:
        from opentelemetry import trace as trace_api

        return trace_api.get_tracer(__name__)
    except Exception:  # noqa: BLE001 - telemetry is never load-bearing
        return None


@contextlib.contextmanager
def _span(tracer: Any, name: str, attributes: dict[str, Any]) -> Iterator[Any]:
    """Start *name* as the current span, or yield ``None`` if that is impossible.

    Exceptions raised inside the body propagate: the SDK's own context manager
    sets the span status to ERROR and ends it on the way out.  §20.6.4 item 2
    requires error/timeout/cancel/skipped to *still end the span and record
    status*, and letting the exception travel through the span is what makes
    that automatic rather than something each call site must remember.
    """
    if tracer is None:
        yield None
        return
    try:
        manager = tracer.start_as_current_span(
            name,
            attributes=attributes,
            record_exception=False,
        )
    except Exception:  # noqa: BLE001 - telemetry is never load-bearing
        yield None
        return
    with manager as span:
        yield span


def _set_attribute(span: Any, key: str, value: Any) -> None:
    """Best-effort ``span.set_attribute`` that tolerates a ``None`` span."""
    if span is None:
        return
    try:
        span.set_attribute(key, value)
    except Exception:  # noqa: BLE001 - telemetry is never load-bearing
        pass


@dataclass
class HarnessRun:
    run_id: str
    artifacts: RunArtifacts
    budget: Budget = field(default_factory=Budget)
    checkpoint_path: Path | None = None
    network_allowed: bool = False
    adapter: AgentAdapter | None = None
    scorer: ScorerCallback | None = None
    setup_workspace: WorkspaceSetup | None = None  # clone repo, checkout, etc.
    # Hosts that stay reachable while the rest of the network fails closed.
    # The agent needs its model endpoint; nothing else is opened, and whatever
    # is listed here is recorded in the run manifest.
    network_allowlist: tuple[str, ...] = ()
    config: dict[str, Any] = field(default_factory=dict)

    def __post_init__(self) -> None:
        self._completed: set[str] = set()
        # Importing the optional OTel SDK can take roughly 0.2s on a cold
        # interpreter.  Do that constructor-side, before the run timer starts,
        # so a short instance deadline measures agent work rather than a
        # one-time dependency import.
        if self.config.get("trace_capture", True):
            try:
                from opentelemetry import trace as trace_api
                from opentelemetry.sdk.trace import TracerProvider  # noqa: F401

                trace_api.get_tracer_provider()
            except Exception:  # noqa: BLE001 - telemetry is optional
                pass
        self._global_usage = BudgetUsage()
        self._budget_contract = budget_contract(self.budget)
        self._budget_contract_sha256 = budget_contract_sha256(self.budget)
        self._instance_count = 0
        # O2: set by run(); holds the spans this run produced.  ``None`` means
        # capture was disabled or unavailable, which _finalize records honestly
        # rather than treating as "no spans were expected".
        self._capture: TraceCapture | None = None
        # Per-instance usage; rebound at the top of each instance so the
        # process-lifecycle hooks below always target the live budget.
        self._current_usage: BudgetUsage = BudgetUsage()
        self._checkpoint_contract = self._checkpoint_contract_payload()
        self._checkpoint_contract_sha256 = _stable_json_sha256(
            self._checkpoint_contract
        )
        if self.checkpoint_path is not None:
            self._load_or_initialize_checkpoint()

    # -- H4: process-lifecycle hooks -------------------------------------
    # max_processes is only a real constraint if something reports child
    # processes into the live BudgetUsage.  Adapters that spawn subprocesses
    # call these; the post-solve check_budget() then classifies an overrun as
    # BudgetExceeded("processes") -> ERROR_AGENT instead of silently passing.

    def report_active_processes(self, count: int) -> None:
        """Set the concurrent child-process count for the running instance."""
        self._current_usage.active_processes = max(0, int(count))

    def process_started(self) -> None:
        """Report that the adapter spawned one child process."""
        self._current_usage.record_process_start()

    def process_ended(self) -> None:
        """Report that one adapter child process exited."""
        self._current_usage.record_process_end()

    def _mark_completed(self, instance_id: str) -> None:
        self._completed.add(instance_id)
        if self.checkpoint_path is not None:
            self._append_checkpoint(
                {"kind": CHECKPOINT_COMPLETED_KIND, "instance_id": instance_id}
            )

    def _checkpoint_contract_payload(self) -> dict[str, Any]:
        budget = self.budget
        return {
            "version": CHECKPOINT_VERSION,
            "run_id": self.run_id,
            "budget": {
                "contract": self._budget_contract,
                "contract_sha256": self._budget_contract_sha256,
                "wall_clock_seconds": budget.wall_clock_seconds,
                "instance_wall_clock_seconds": getattr(
                    budget, "instance_wall_clock_seconds", None
                ),
                "scorer_reserve_seconds": getattr(
                    budget, "scorer_reserve_seconds", 0.0
                ),
                "max_tokens": budget.max_tokens,
                "max_cost": budget.max_cost,
                "max_output_bytes": budget.max_output_bytes,
                "max_processes": budget.max_processes,
            },
            "pins": {
                key: self.config.get(key, "")
                for key in (
                    "git_sha",
                    "dirty_hash",
                    "provider",
                    "model",
                    "model_revision",
                    "prompt_hash",
                    "benchmark",
                    "dataset_pin",
                    "evaluation_subset",
                )
            },
        }

    def _load_or_initialize_checkpoint(self) -> None:
        assert self.checkpoint_path is not None
        self.checkpoint_path.parent.mkdir(parents=True, exist_ok=True)
        with _exclusive_checkpoint_lock(self.checkpoint_path):
            if not self.checkpoint_path.exists() or not self.checkpoint_path.read_bytes():
                self._write_checkpoint_contract()
                return

            try:
                rows = self._read_checkpoint_rows()
            except ValueError:
                rows = []
            if rows and rows[0].get("kind") != CHECKPOINT_CONTRACT_KIND:
                raise ValueError("checkpoint contract is missing or malformed")
            if not rows:
                # Pre-contract checkpoints contained one completed instance ID
                # per line. Upgrade them once while holding the same lock used
                # for all future writes.
                legacy_ids = [
                    line.strip()
                    for line in self.checkpoint_path.read_text(encoding="utf-8").splitlines()
                    if line.strip()
                ]
                if not all("{" not in value and "}" not in value for value in legacy_ids):
                    raise ValueError("checkpoint contract is missing or malformed")
                self._completed.update(legacy_ids)
                self.checkpoint_path.unlink()
                self._write_checkpoint_contract()
                for instance_id in legacy_ids:
                    self._append_checkpoint_unlocked(
                        {"kind": CHECKPOINT_COMPLETED_KIND, "instance_id": instance_id}
                    )
                return
            self._validate_checkpoint_rows(rows)

    def _validate_checkpoint_rows(self, rows: list[dict[str, Any]]) -> None:
        header = rows[0]
        if (
            header.get("version") != CHECKPOINT_VERSION
            or header.get("sha256") != self._checkpoint_contract_sha256
            or header.get("contract") != self._checkpoint_contract
        ):
            raise ValueError("checkpoint contract does not match this evaluation run")
        for row in rows[1:]:
            if set(row) != {"kind", "instance_id"} or row["kind"] != CHECKPOINT_COMPLETED_KIND:
                raise ValueError("checkpoint contains an invalid completion record")
            instance_id = row["instance_id"]
            if not isinstance(instance_id, str) or not instance_id:
                raise ValueError("checkpoint contains an invalid instance ID")
            self._completed.add(instance_id)

    def _read_checkpoint_rows(self) -> list[dict[str, Any]]:
        assert self.checkpoint_path is not None
        rows: list[dict[str, Any]] = []
        for line in self.checkpoint_path.read_text(encoding="utf-8").splitlines():
            if not line.strip():
                continue
            try:
                row = json.loads(line)
            except json.JSONDecodeError as exc:
                raise ValueError("checkpoint contract is missing or malformed") from exc
            if not isinstance(row, dict):
                raise ValueError("checkpoint contains a non-object record")
            rows.append(row)
        return rows

    def _write_checkpoint_contract(self) -> None:
        self._append_checkpoint_unlocked(
            {
                "kind": CHECKPOINT_CONTRACT_KIND,
                "version": CHECKPOINT_VERSION,
                "sha256": self._checkpoint_contract_sha256,
                "contract": self._checkpoint_contract,
            },
            mode="x",
        )

    def _append_checkpoint(self, row: dict[str, Any], *, mode: str = "a") -> None:
        assert self.checkpoint_path is not None
        with _exclusive_checkpoint_lock(self.checkpoint_path):
            self._append_checkpoint_unlocked(row, mode=mode)

    def _append_checkpoint_unlocked(self, row: dict[str, Any], *, mode: str = "a") -> None:
        assert self.checkpoint_path is not None
        try:
            with self.checkpoint_path.open(mode, encoding="utf-8") as handle:
                handle.write(json.dumps(row, sort_keys=True, separators=(",", ":")) + "\n")
                handle.flush()
                os.fsync(handle.fileno())
        except FileExistsError as exc:
            # This method is only called while the exclusive lock is held.
            # Re-entering checkpoint initialization here would wait on our own
            # lock forever, so fail closed and let the caller retry explicitly.
            raise ValueError("checkpoint contract was created concurrently") from exc

    def _remaining_wall_clock_seconds(self) -> float:
        """Return the unspent run deadline or fail before starting more work."""
        remaining = self.budget.wall_clock_seconds - self._global_usage.wall_clock()
        if remaining <= 0:
            raise BudgetExceeded(
                "wall-clock",
                f"{self._global_usage.wall_clock():.1f}s > "
                f"{self.budget.wall_clock_seconds}s",
            )
        return remaining

    def _remaining_instance_wall_clock_seconds(
        self,
        usage: BudgetUsage,
    ) -> float:
        """Return the time left in both the run and current instance."""
        run_remaining = self._remaining_wall_clock_seconds()
        instance_limit = self.budget.instance_wall_clock_seconds
        if instance_limit is None:
            return run_remaining
        instance_remaining = instance_limit - usage.wall_clock()
        remaining = min(run_remaining, instance_remaining)
        if remaining <= 0:
            raise BudgetExceeded(
                "wall-clock",
                f"instance used {usage.wall_clock():.1f}s of "
                f"{instance_limit}s",
            )
        return remaining

    def _agent_phase_seconds(self, usage: BudgetUsage) -> float:
        remaining = self._remaining_instance_wall_clock_seconds(usage)
        reserve = self.budget.scorer_reserve_seconds if self.scorer else 0.0
        agent_seconds = remaining - reserve
        if agent_seconds <= 0:
            raise BudgetExceeded(
                "wall-clock",
                f"{remaining:.1f}s remains but {reserve:.1f}s is reserved "
                "for the official scorer",
            )
        return agent_seconds

    def _scorer_phase_seconds(self, usage: BudgetUsage) -> float:
        remaining = self._remaining_instance_wall_clock_seconds(usage)
        reserve = self.budget.scorer_reserve_seconds
        return min(remaining, reserve) if reserve > 0 else remaining

    def _call_with_phase_deadline(
        self,
        call: Callable[[threading.Event, float], Any],
        usage: BudgetUsage,
        *,
        reserve_scorer_time: bool,
    ) -> Any:
        timeout_s = (
            self._agent_phase_seconds(usage)
            if reserve_scorer_time
            else self._scorer_phase_seconds(usage)
        )
        cancel_event = threading.Event()
        done_event = threading.Event()
        result: list[Any] = []
        error: list[BaseException] = []

        def run() -> None:
            try:
                result.append(call(cancel_event, timeout_s))
            except BaseException as exc:  # noqa: BLE001 - reported by owner
                error.append(exc)
            finally:
                done_event.set()

        worker = threading.Thread(target=run, daemon=True)
        worker.start()
        if not done_event.wait(timeout_s):
            cancel_event.set()
            # Give cooperative adapters a short cancellation window to return
            # their partial result. A non-cooperative call is still detached
            # from the harness after this bounded grace period.
            if not done_event.wait(min(0.05, max(timeout_s * 0.25, 0.001))):
                raise TimeoutError(f"phase exceeded {timeout_s:.3f}s deadline")
        if error:
            raise error[0]
        return result[0]

    def run(self, instances: list[EvalInstance]) -> dict[str, Any]:
        """Run all *instances* through the adapter, recording artifacts.

        Parameters
        ----------
        instances:
            List of :class:`EvalInstance` objects to evaluate.

        Returns
        -------
        dict
            ``{"summary": {...}, "summary_path": "..."}``.
        """
        self._assert_fixed_budget_contract()
        self._instance_count = len(instances)
        summary: dict[str, Any] = {
            "run_id": self.run_id,
            "total": len(instances),
            "completed": 0,
            "resumed_skipped": 0,
            "by_category": {
                ERROR_TIMEOUT: 0,
                ERROR_OOM: 0,
                ERROR_INFRA: 0,
                ERROR_AGENT: 0,
                ERROR_SCORER: 0,
                ERROR_BUDGET: 0,
            },
            "budget": {
                "contract": self._budget_contract,
                "contract_sha256": self._budget_contract_sha256,
                "instance_count": self._instance_count,
                "usage": self._global_usage.snapshot(),
            },
        }

        # O2: install the in-process span capture and open the run root span.
        # The capture must be installed *before* the first span is created, and
        # the run span must be closed before _finalize() reads the spans, or the
        # root would be missing from its own artifact.
        capture: TraceCapture | None = None
        tracer = None
        if self.config.get("trace_capture", True):
            capture = TraceCapture()
            otlp_endpoint = ""
            if self.config.get("trace_profile", "default") == "o3":
                otlp_endpoint = str(self.config.get("phoenix_otlp_endpoint", "")).strip()
            if capture.install(otlp_endpoint=otlp_endpoint):
                tracer = _get_tracer()
        self._capture = capture

        with _span(tracer, SPAN_EVAL_RUN, self._run_span_attributes()):
            self._run_instances(instances, summary, tracer)

        summary["ok"] = summary["completed"]
        summary["failed"] = sum(summary["by_category"].values())
        summary["skipped"] = summary["resumed_skipped"]
        summary["budget"]["usage"] = self._global_usage.snapshot()

        path = _finalize(self, summary)
        return {"summary": summary, "summary_path": path}

    def _assert_fixed_budget_contract(self) -> None:
        """Reject caller-provided budget claims that disagree with the run.

        A benchmark may carry a precomputed contract in its manifest config,
        but it must describe the immutable :class:`Budget` owned by this
        HarnessRun. The check happens before traces, workspaces, or adapters
        are started so a mismatched evaluation cannot leave a misleading
        receipt behind.
        """
        declared = self.config.get("budget_contract")
        if declared is not None and declared != self._budget_contract:
            raise ValueError("budget contract does not match HarnessRun budget")
        declared_sha = self.config.get("budget_contract_sha256")
        if declared_sha is not None and str(declared_sha) != self._budget_contract_sha256:
            raise ValueError("budget contract sha256 does not match HarnessRun budget")

    def _run_span_attributes(self) -> dict[str, Any]:
        """Join attributes for the ``eval.run`` root span (§9.2).

        Sourced from ``self.config`` — the same dict ``_build_manifest`` reads —
        so the trace and the manifest cannot disagree about which commit and
        model the run used.  ``eval.instance_id`` is present but empty on the
        root: the contract requires the join *key* on every span, and the root
        legitimately spans all instances rather than one.
        """
        config = self.config
        return {
            "eval.run_id": self.run_id,
            "eval.instance_id": "",
            "git.commit": str(config.get("git_sha", "")),
            "eval.benchmark": str(config.get("benchmark", "")),
            "eval.model": str(config.get("model", "")),
            "eval.mode": str(config.get("mode", "official")),
        }

    def _instance_span_attributes(self, instance_id: str) -> dict[str, Any]:
        return {
            "eval.run_id": self.run_id,
            "eval.instance_id": instance_id,
            "git.commit": str(self.config.get("git_sha", "")),
        }

    def _run_instances(
        self,
        instances: list[EvalInstance],
        summary: dict[str, Any],
        tracer: Any,
    ) -> None:
        for instance in instances:
            instance_id = instance.instance_id
            # Fail closed: missing/empty instance_id is never silently skipped
            if not instance_id:
                summary["by_category"][ERROR_AGENT] += 1
                self.artifacts.record_failure(
                    "", ERROR_AGENT,
                    f"instance_id is empty or missing; instance={instance!r}",
                )
                self.artifacts.record_event("", "failed-missing-instance-id")
                # O2: even a rejected instance gets a span.  §20.1 rule 7 keeps
                # failures in the denominator, and a trace that silently omits
                # them would misstate how many instances the run attempted.
                with _span(
                    tracer, SPAN_EVAL_INSTANCE, self._instance_span_attributes("")
                ) as span:
                    _set_attribute(span, "eval.instance_status", "failed-missing-instance-id")
                    _set_attribute(span, "eval.error_category", ERROR_AGENT)
                continue

            # Pre-start budget check — fail before creating workspace
            try:
                check_budget(self.budget, self._global_usage)
            except BudgetExceeded as exc:
                category = classify_error(exc)
                summary["by_category"][category] += 1
                self.artifacts.record_failure(instance_id, category, str(exc)[:500])
                self.artifacts.record_event(instance_id, f"failed-{category}")
                with _span(
                    tracer,
                    SPAN_EVAL_INSTANCE,
                    self._instance_span_attributes(instance_id),
                ) as span:
                    _set_attribute(span, "eval.instance_status", f"failed-{category}")
                    _set_attribute(span, "eval.error_category", category)
                continue

            # Resume skip
            if instance_id in self._completed:
                summary["resumed_skipped"] += 1
                self.artifacts.record_event(instance_id, "skipped-resume")
                # §20.6.4 item 2 names "skipped" explicitly: a resumed run must
                # still be able to show which instances it deliberately did not
                # re-execute.
                with _span(
                    tracer,
                    SPAN_EVAL_INSTANCE,
                    self._instance_span_attributes(instance_id),
                ) as span:
                    _set_attribute(span, "eval.instance_status", "skipped-resume")
                continue

            workspace = Path(tempfile.mkdtemp(prefix=f"eval-{self.run_id}-"))
            usage = BudgetUsage()
            # H4: expose the per-instance usage so adapters can report their
            # real concurrent child-process count.  Without this hook the
            # max_processes branch of check_budget() is unreachable and the
            # budget is dead configuration.
            self._current_usage = usage
            try:
                # O2: the instance span is opened *inside* the try so that any
                # exception below travels out through the span's __exit__, which
                # records ERROR status and ends it, before the except clauses
                # classify it.  Opening it outside would leave a failed instance
                # with an UNSET span.
                with _span(
                    tracer,
                    SPAN_EVAL_INSTANCE,
                    self._instance_span_attributes(instance_id),
                ) as instance_span:
                    self._execute_instance(
                        instance=instance,
                        instance_id=instance_id,
                        workspace=workspace,
                        usage=usage,
                        summary=summary,
                        tracer=tracer,
                        instance_span=instance_span,
                    )

            except ScorerError as exc:
                category = ERROR_SCORER
                summary["by_category"][category] += 1
                self.artifacts.record_failure(instance_id, category, str(exc)[:500])
                self.artifacts.record_event(instance_id, f"failed-{category}")
                # Preserve workspace on failure for post-mortem
                _mark_workspace_preserved(workspace, category, str(exc)[:500])
            except BaseException as exc:  # noqa: BLE001 - classify and record
                category = classify_error(exc)
                summary["by_category"][category] += 1
                self.artifacts.record_failure(
                    instance_id, category,
                    f"{str(exc)[:500]} | workspace: {workspace}",
                )
                self.artifacts.record_event(instance_id, f"failed-{category}")
                # Preserve workspace on failure for post-mortem
                _mark_workspace_preserved(workspace, category, str(exc)[:500])

    def _execute_instance(
        self,
        instance: EvalInstance,
        instance_id: str,
        workspace: Path,
        usage: BudgetUsage,
        summary: dict[str, Any],
        tracer: Any,
        instance_span: Any,
    ) -> None:
        """Solve and score one instance; raises on failure for the caller to
        classify.  Extracted so the instance span can wrap it as a unit."""
        # Populate workspace (e.g. clone repo) before the agent runs. Setup is
        # part of the same per-instance agent phase and cannot consume scorer
        # reserve or run past the deadline.
        if self.setup_workspace is not None:
            self._call_with_phase_deadline(
                lambda _cancel_event, _timeout_s: self.setup_workspace(
                    instance, str(workspace)
                ),
                usage,
                reserve_scorer_time=True,
            )

        if not self.network_allowed:
            _block_network(workspace, self.network_allowlist)

        # Call adapter.  The join context is attached around the call so spans
        # created *inside* the adapter — the driver's tool spans, and the
        # orchestrator's ``chat`` — inherit ``eval.run_id``/``eval.instance_id``
        # without the orchestrator having to import anything from eval.  See
        # eval/harness/trace_join.py for what this does and does not prove.
        if self.adapter is not None:
            with eval_join_context(self.run_id, instance_id):
                result = self._call_with_phase_deadline(
                    lambda cancel_event, timeout_s: self.adapter.solve_instance(
                        instance,
                        str(workspace),
                        cancel_event=cancel_event,
                        timeout_s=timeout_s,
                    ),
                    usage,
                    reserve_scorer_time=True,
                )
            # H4: enforce the process cap on whatever the adapter
            # reported while it was running (fail closed, never
            # silently over-subscribe the machine).
            check_budget(self.budget, usage)
        else:
            result = EvalResult(
                instance_id=instance_id,
                error="no adapter configured",
            )

        # Feed budget from result. Output is measured in UTF-8 bytes so the
        # limit is stable across platforms and catches both patch and answer
        # payloads before they reach artifacts or the official scorer.
        usage.record_tokens(result.tokens_in + result.tokens_out)
        usage.record_cost(result.cost)
        usage.record_output(
            len(result.model_patch.encode("utf-8"))
            + len(result.answer.encode("utf-8"))
        )
        # Wall clock is auto-captured by BudgetUsage.started_at

        # Account for the attempted result before checking limits. A receipt
        # must show the resource that caused a run to stop, rather than hiding
        # the over-budget attempt from its own usage snapshot.
        self._global_usage.record_tokens(usage.tokens)
        self._global_usage.record_cost(usage.cost)
        self._global_usage.record_output(usage.output_bytes)

        # Post-solve budget check
        check_budget(self.budget, usage)
        check_budget(self.budget, self._global_usage)
        self._remaining_instance_wall_clock_seconds(usage)

        # An adapter error is a failed attempt even if it also returned a
        # patch/answer. Never let the scorer or predictions turn an explicit
        # failure into a completed denominator row.
        if result.error:
            raise AdapterResultError(result.error)

        # Record instance
        self.artifacts.record_instance({
            "instance_id": instance_id,
            "task_description": instance.task_description,
            "metadata": instance.metadata,
        })

        # Build prediction dict
        prediction: dict[str, Any] = {
            "instance_id": instance_id,
            "model_patch": result.model_patch,
            "answer": result.answer,
            "cost": result.cost,
            "tokens_in": result.tokens_in,
            "tokens_out": result.tokens_out,
            "trace_id": result.trace_id,
            "error": result.error,
            "wall_time_s": result.wall_time_s,
        }
        if self.config.get("trace_profile", "default") == "o3":
            prediction["evidence"] = _validate_o3_evidence(result.evidence)

        # Scorer
        if self.scorer is not None:
            # O2 (§20.6.4 item 2): the official scorer gets its own span.  The
            # harness owns the scorer callback, so this is the one call site
            # every benchmark's official scoring passes through — instrumenting
            # it here covers SWE-bench, Terminal-Bench and tau2-bench without
            # each adapter re-implementing it.
            with _span(
                tracer,
                SPAN_SCORER_OFFICIAL,
                self._instance_span_attributes(instance_id),
            ) as scorer_span:
                try:
                    check_budget(self.budget, self._global_usage)
                    scorer_result = self._call_with_phase_deadline(
                        lambda _cancel_event, timeout_s: self.scorer(
                            result,
                            instance,
                            workspace,
                            timeout_s=timeout_s,
                        ),
                        usage,
                        reserve_scorer_time=False,
                    )
                    if not isinstance(scorer_result, dict):
                        raise TypeError("official scorer must return a mapping")
                    check_budget(self.budget, self._global_usage)
                    self._remaining_instance_wall_clock_seconds(usage)
                except (BudgetExceeded, TimeoutError, subprocess.TimeoutExpired):
                    raise
                except Exception as scorer_exc:
                    # scorer exception → ERROR_SCORER (now reachable!).  Raised
                    # inside the span's with-block so the span is ended with
                    # ERROR status rather than left UNSET.
                    raise ScorerError(str(scorer_exc)) from scorer_exc
                # A benchmark may hand back the official harness's raw
                # output under this reserved key.  It is persisted under
                # scorer/ (so checksums.sha256 pins it) instead of being
                # inlined into the prediction row, which is one JSON
                # line per instance and must stay readable.  Both the raw
                # content and the structured fields merged into the
                # prediction are output for budget purposes.  Account for
                # the complete attempt before checking limits or writing
                # any scorer artifact so an over-budget run remains an
                # accurate, side-effect-free receipt.
                raw_outputs = scorer_result.pop(SCORER_RAW_OUTPUT_KEY, None)
                raw_payloads: list[tuple[str, str, int]] = []
                if isinstance(raw_outputs, dict):
                    for name, content in raw_outputs.items():
                        text = str(content)
                        raw_payloads.append((str(name), text, len(text.encode("utf-8"))))

                structured_output_bytes = 0
                if scorer_result:
                    structured_output_bytes = len(
                        json.dumps(
                            scorer_result,
                            ensure_ascii=False,
                            separators=(",", ":"),
                        ).encode("utf-8")
                    )
                scorer_output_bytes = structured_output_bytes + sum(
                    size for _, _, size in raw_payloads
                )
                usage.record_output(scorer_output_bytes)
                self._global_usage.record_output(scorer_output_bytes)
                check_budget(self.budget, usage)
                check_budget(self.budget, self._global_usage)

                # Only write scorer evidence after both budget checks pass;
                # an over-budget attempt must not leave partial raw files.
                for name, text, _ in raw_payloads:
                    self.artifacts.record_scorer_output(name, text)
                _set_attribute(
                    scorer_span, "eval.scorer_keys", ",".join(sorted(scorer_result))
                )
                prediction.update(scorer_result)

        self.artifacts.record_prediction(prediction)
        self.artifacts.record_event(instance_id, "completed")
        self._mark_completed(instance_id)
        summary["completed"] += 1
        _set_attribute(instance_span, "eval.instance_status", "completed")

        # Success — clean up workspace
        try:
            shutil.rmtree(workspace, ignore_errors=True)
        except Exception:
            pass


def _finalize(harness: HarnessRun, summary: dict[str, Any]) -> str:
    """Write summary, environment, manifest, and checksums; return summary path.

    Order matters for H3 finalization: summary and environment first,
    then the manifest built from harness config + summary (fail-closed on
    missing pins via :func:`_build_manifest`), and finally
    ``checksums.sha256`` pinning every artifact written so far.  A
    ``ValueError`` from the manifest aborts finalization before any
    manifest/checksum is emitted — a run without mandatory pins is never
    reported as reproducible.
    """
    path = harness.artifacts.write_summary(summary)
    harness.artifacts.write_environment()
    manifest = _build_manifest(harness, summary)
    harness.artifacts.write_manifest(manifest)
    _write_trace_artifacts(harness)
    harness.artifacts.write_checksums()
    return str(path)


def _write_trace_artifacts(harness: HarnessRun) -> None:
    """Write ``traces/trace-summary.json`` and ``traces/span-assertion.json``.

    Must run before ``write_checksums()`` so the recursive pin covers both
    files (§20.7).  Both files are written unconditionally, including when no
    spans were captured: an absent trace artifact is indistinguishable from an
    un-run step, whereas a present artifact reporting ``INCOMPLETE`` with zero
    spans states the situation and fails the gate honestly.
    """
    capture = harness._capture
    try:
        profile = harness.config.get("trace_profile", "default")
        expected_instance_ids = tuple(harness._completed)
        if profile == "o3":
            phoenix_url = str(harness.config.get("phoenix_url", "")).strip()
            phoenix_project = str(harness.config.get("phoenix_project", "")).strip()
            start_time = str(harness.config.get("trace_start_time", "")).strip()
            if not phoenix_url or not phoenix_project or not start_time:
                raise ValueError(
                    "O3 trace profile requires phoenix_url, phoenix_project, and trace_start_time"
                )
            # O3 verifies the collector rather than the in-process capture.
            # Its root/instance/scorer spans can still be queued in the SDK's
            # BatchSpanProcessor when the run context exits, so drain the
            # provider before polling Phoenix or readback can observe only an
            # orphaned earlier subset of the real trace.
            try:
                from opentelemetry import trace as trace_api

                trace_api.get_tracer_provider().force_flush()
            except Exception:  # noqa: BLE001 - telemetry is never load-bearing
                pass
            from eval.harness.phoenix import read_run_spans

            # Phoenix ingestion is asynchronous even after the SDK provider
            # reports a successful flush. Poll a bounded window for the O3
            # topology rather than accepting an early partial trace.
            spans = []
            deadline = time.monotonic() + 10.0
            while True:
                spans = read_run_spans(
                    phoenix_url,
                    phoenix_project,
                    start_time,
                    harness.run_id,
                    expected_instance_ids,
                )
                names = {span.name for span in spans}
                required_names = {"eval.run", "eval.instance", "scorer.official"}
                if required_names.issubset(names) or time.monotonic() >= deadline:
                    break
                time.sleep(0.5)
            trace_summary = {
                "capture_mode": "phoenix-api-readback",
                "collector": "phoenix",
                "phoenix_verified": True,
                "attached": False,
                "span_count": len(spans),
                "trace_ids": sorted({span.trace_id for span in spans}),
                "spans": [span.to_dict() for span in spans],
            }
        elif capture is None:
            trace_summary: dict[str, Any] = {
                "capture_mode": "disabled",
                "collector": "none",
                "phoenix_verified": False,
                "attached": False,
                "span_count": 0,
                "trace_ids": [],
                "spans": [],
                "reason": "trace capture disabled or OTel SDK unavailable",
            }
            spans: list[Any] = []
        else:
            trace_summary = capture.summary()
            spans = capture.spans()

        # A benchmark that declares nothing gets ``None``, which keeps every
        # span kind required: silence can only make the contract stricter.
        capabilities = harness.config.get("trace_capabilities")
        assertion = evaluate_trace_contract(
            spans,
            run_id=harness.run_id,
            capabilities=capabilities,
            profile=profile,
            expected_instance_ids=expected_instance_ids,
        )
        harness.artifacts.record_trace(TRACE_SUMMARY_FILENAME, trace_summary)
        harness.artifacts.record_trace(SPAN_ASSERTION_FILENAME, assertion)
    except Exception as exc:  # noqa: BLE001 - telemetry is never load-bearing
        # Record the failure instead of silently omitting the subtree, so the
        # artifact never implies a trace was assessed when it was not.
        harness.artifacts.record_trace(
            SPAN_ASSERTION_FILENAME,
            {
                "contract_version": "unknown",
                "run_id": harness.run_id,
                "profile": harness.config.get("trace_profile", "default"),
                "verdict": "FAIL",
                "problems": [f"trace artifact generation failed: {exc}"],
                "span_count": 0,
            },
        )
    finally:
        # Stop recording so a long-lived process running several HarnessRuns
        # does not accumulate live processors, each capturing every subsequent
        # run's spans.
        if capture is not None:
            capture.stop()


class ScorerError(Exception):
    """Raised when the scorer callback fails; classified as ERROR_SCORER."""


def _validate_o3_evidence(evidence: Any) -> dict[str, list[dict[str, Any]]]:
    """Validate the only evidence shape that an O3 artifact may persist.

    It deliberately does not tolerate arbitrary nested metadata: an O3 receipt
    may attest to the real retrieved IDs and ranks, but not raw query/prompt,
    tool output, document text, scorer labels, or credentials.
    """
    if not isinstance(evidence, dict) or set(evidence) != {"retrieval_hits"}:
        raise ValueError("O3 evidence must contain only retrieval_hits")
    hits = evidence["retrieval_hits"]
    if not isinstance(hits, list) or not hits:
        raise ValueError("O3 evidence requires at least one retrieval hit")
    safe_hits: list[dict[str, Any]] = []
    allowed = {"rank", "document_id", "chunk_id", "score"}
    for hit in hits:
        if not isinstance(hit, dict) or set(hit) != allowed:
            raise ValueError("O3 retrieval evidence has an unsafe hit shape")
        rank = hit["rank"]
        document_id = hit["document_id"]
        chunk_id = hit["chunk_id"]
        score = hit["score"]
        if not isinstance(rank, int) or rank <= 0:
            raise ValueError("O3 retrieval evidence rank must be a positive integer")
        if not isinstance(document_id, str) or not document_id.strip():
            raise ValueError("O3 retrieval evidence document_id is required")
        if not isinstance(chunk_id, (int, str)) or isinstance(chunk_id, bool):
            raise ValueError("O3 retrieval evidence chunk_id is invalid")
        if not isinstance(score, (int, float)) or isinstance(score, bool):
            raise ValueError("O3 retrieval evidence score is invalid")
        safe_hits.append(
            {
                "rank": rank,
                "document_id": document_id,
                "chunk_id": chunk_id,
                "score": float(score),
            }
        )
    return {"retrieval_hits": safe_hits}




def _build_manifest(harness: HarnessRun, summary: dict[str, Any]) -> dict[str, Any]:
    """Build run-manifest.json from harness config and run summary.

    Fail closed via :mod:`eval.harness.pin_contract`.  This
    previously required only ``git_sha`` and ``model``, so ``prompt_hash``,
    ``qrels_hash`` and ``physical_index`` could all be empty strings and the
    manifest was written regardless — an unpinned run was indistinguishable
    from a pinned one (portfolio defect 9).

    The contract does *not* simply require everything: RAG pins are waived for
    benchmarks that perform no retrieval (demanding them would make a correct
    SWE-bench manifest impossible), and an absent ``model_revision`` is
    recorded as ``MODEL_IDENTITY_UNVERIFIED`` rather than aborting, because
    §20.6.3 E2 mandates exactly that status when the provider reports no
    immutable revision.  Both the waivers and the statuses are written into the
    manifest so a reader can audit them instead of trusting this docstring.
    """
    config = harness.config
    budget_obj = harness.budget
    contract = getattr(harness, "_budget_contract", budget_contract(budget_obj))
    contract_sha = getattr(
        harness, "_budget_contract_sha256", budget_contract_sha256(budget_obj)
    )
    instance_count = getattr(harness, "_instance_count", summary.get("total", 0))
    budget_summary = summary.get("budget")
    usage = budget_summary.get("usage") if isinstance(budget_summary, dict) else None
    if usage is None:
        global_usage = getattr(harness, "_global_usage", None)
        usage = (
            global_usage.snapshot()
            if global_usage is not None
            else {
                "wall_clock_seconds": 0.0,
                "tokens": 0,
                "cost": 0.0,
                "output_bytes": 0,
                "active_processes": 0,
            }
        )
    capabilities = config.get("trace_capabilities")
    pin_values = {
        "git_sha": config.get("git_sha", ""),
        "model": config.get("model", ""),
        # Computed here when the caller supplies nothing, rather than demanded
        # from every caller.  The prompt scaffold is a property of the checked-out
        # code, so the harness can always answer "which prompt was this measured
        # under?" itself.  Requiring callers to remember would recreate the defect
        # class this contract exists to close: a pin that is missing because
        # somebody forgot, recorded as though nothing were wrong.
        "prompt_hash": config.get("prompt_hash") or system_prompt_pin(),
        "model_revision": config.get("model_revision", ""),
        "corpus_generation": config.get("corpus_generation", ""),
        "qrels_hash": config.get("qrels_hash", ""),
        "physical_index": config.get("index_name", ""),
    }
    pin_report = evaluate_pins(pin_values, capabilities=capabilities)
    if pin_report["missing"]:
        raise ValueError(
            "refusing to write run-manifest.json: missing mandatory pins: "
            f"{', '.join(pin_report['missing'])} "
            f"(declared capabilities: {pin_report['declared_capabilities']})"
        )
    if pin_report["contradictions"]:
        raise ValueError(
            "refusing to write run-manifest.json: pin/capability contradiction: "
            + "; ".join(pin_report["contradictions"])
        )
    return {
        "pin_contract": {
            "declared_capabilities": pin_report["declared_capabilities"],
            "waived_pins": pin_report["waived"],
            "statuses": pin_report["statuses"],
        },
        "run_id": harness.run_id,
        "mode": config.get("mode", "official"),
        "synthetic": config.get("synthetic", False),
        "git_sha": pin_values["git_sha"],
        "dirty_hash": config.get("dirty_hash", ""),
        "provider": config.get("provider", ""),
        "model": pin_values["model"],
        "model_revision": pin_values["model_revision"],
        "prompt_hash": pin_values["prompt_hash"],
        "corpus_generation": pin_values["corpus_generation"],
        "qrels_hash": pin_values["qrels_hash"],
        "physical_index": pin_values["physical_index"],
        "index_mapping_hash": config.get("index_mapping_hash", ""),
        "dataset_pin": config.get("dataset_pin", {}),
        "evaluation_subset": config.get("evaluation_subset", {}),
        "budget_contract": contract,
        "budget_contract_sha256": contract_sha,
        "budget_instance_count": instance_count,
        "budget_usage": usage,
        "budgets": {
            "contract": contract,
            "contract_sha256": contract_sha,
            "instance_count": instance_count,
            "wall_clock_seconds": harness.budget.wall_clock_seconds,
            "instance_wall_clock_seconds": (
                getattr(harness.budget, "instance_wall_clock_seconds", None)
            ),
            "scorer_reserve_seconds": getattr(
                harness.budget, "scorer_reserve_seconds", 0.0
            ),
            "max_tokens": harness.budget.max_tokens,
            "max_cost": harness.budget.max_cost,
            "max_output_bytes": harness.budget.max_output_bytes,
            "max_processes": harness.budget.max_processes,
            "usage": usage,
        },
        "seed": config.get("seed", 42),
        # Which arm of a paired experiment produced this artifact. Without it the
        # two arms' manifests are byte-identical apart from the run_id, so an
        # artifact could not answer the one question the experiment is about.
        # Recorded from the environment at manifest time, which is the same
        # source the solve path reads, so the two cannot disagree.
        "harness_uplift": _uplift_manifest_block(),
        "start_time": config.get("start_time", ""),
        # "disabled" must never be claimed while a remote host was reachable:
        # that would misstate the conditions the measurement was taken under.
        "network_policy": (
            "allowed"
            if harness.network_allowed
            else ("allowlist" if harness.network_allowlist else "disabled")
        ),
        "network_allowlist": list(harness.network_allowlist),
        "summary": {
            "total": summary["total"],
            "completed": summary["completed"],
            "failed": summary["failed"],
            "skipped": summary.get("skipped", summary.get("resumed_skipped", 0)),
        },
    }


def _uplift_manifest_block() -> dict[str, Any]:
    """Describe the active optimization arm for the manifest.

    Degrades to ``{"enabled": False, "unavailable": ...}`` rather than raising:
    an unreadable optimization config must not stop a run from recording what it
    could determine, and a manifest that says "I could not tell" is more useful
    than a manifest that says nothing.
    """
    try:
        from eval.harness.uplift import uplift_config

        return uplift_config().as_manifest_block()
    except Exception as exc:  # pragma: no cover - defensive by intent
        return {"enabled": False, "unavailable": f"{type(exc).__name__}: {exc}"}


def _mark_workspace_preserved(workspace: Path, category: str, message: str) -> None:
    """Write a WORKSPACE_PRESERVED marker so post-mortem analysis knows why
    the workspace was kept (failure category + error message)."""
    try:
        (workspace / WORKSPACE_PRESERVED_MARKER).write_text(
            f"category: {category}\nerror: {redact_credential_text(message)}\n",
            encoding="utf-8",
        )
    except OSError:
        # Workspace may already be gone (cleanup race); marker is best-effort
        pass


def _block_network(workspace: Path, allow_hosts: Sequence[str] = ()) -> None:
    """Best-effort network block for the instance workspace on Windows.

    Two layers, both advisory:

    1. ``NETWORK_DISABLED`` marker file records the granted allowlist, so infra
       failures on network calls stay distinguishable from agent bugs.
    2. HTTP(S)_PROXY / NO_PROXY env vars are pinned to a dead proxy so child
       processes that honor proxy env vars fail loudly instead of silently
       leaking traffic.  NO_PROXY carries loopback plus *allow_hosts*, so local
       services stay reachable and named remote endpoints can be reached.

    *allow_hosts* exists because the agent has to call a model endpoint.  With
    loopback-only NO_PROXY this function silently killed every run against a
    remote API (the LLM call died with ConnectionRefused, the agent emitted
    zero tokens and an empty patch, and the run still exited 0).  Hosts must be
    named explicitly and are recorded in the run manifest: an unlisted host —
    github.com, pypi, the upstream fix — still fails closed.

    Known limitation: on Windows this is best-effort and cannot stop a child
    that ignores proxy env vars (raw sockets, DNS, ICMP, non-HTTP protocols)
    or an in-process call that builds its own transport.  The real isolation
    boundary is the benchmark's Docker/container network policy; this block
    only makes the default fail-closed posture real for naive clients.
    """
    dead_proxy = "http://127.0.0.1:9"  # discard port — connections fail fast
    for name in ("HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"):
        os.environ[name] = dead_proxy
    # Loopback plus the explicit allowlist. Must NOT be "*", or the block is
    # bypassed entirely.
    no_proxy_entries = ["127.0.0.1", "localhost", "::1", *allow_hosts]
    for name in ("NO_PROXY", "no_proxy"):
        os.environ[name] = ",".join(no_proxy_entries)
    granted = ", ".join(allow_hosts) if allow_hosts else "(none)"
    (workspace / NETWORK_DISABLED_MARKER).write_text(
        "HTTP(S)_PROXY pinned to dead proxy; every host outside the allowlist "
        "fails closed\n"
        f"allowlist: {granted}\n",
        encoding="utf-8",
    )
