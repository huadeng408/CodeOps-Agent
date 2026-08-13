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
import os
import shutil
import subprocess
import tempfile
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Callable, Iterator, Sequence

from eval.adapter import AgentAdapter, EvalInstance, EvalResult
from eval.harness.artifacts import RunArtifacts
from eval.harness.budget import Budget, BudgetExceeded, BudgetUsage, check_budget
from eval.harness.trace_capture import TraceCapture
from eval.harness.trace_contract import (
    SPAN_EVAL_INSTANCE,
    SPAN_EVAL_RUN,
    SPAN_SCORER_OFFICIAL,
    evaluate_trace_contract,
)
from eval.harness.pin_contract import evaluate_pins, system_prompt_pin
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

# O2 (design map §20.6.4): trace artifact filenames under ``traces/``.
TRACE_SUMMARY_FILENAME = "trace-summary.json"
SPAN_ASSERTION_FILENAME = "span-assertion.json"

# Scorer callback: called after each successful solve_instance, receiving
# the instance workspace as well (where the benchmark adapter writes its own
# artifacts, e.g. predictions.jsonl / score.json sidecars).  Returns a dict
# that gets merged into the prediction artifact.
ScorerCallback = Callable[[EvalResult, EvalInstance, Path], dict[str, Any]]

# Workspace setup callback: called before each solve_instance to populate
# the working directory (e.g. clone a repo, checkout a commit).  Receives
# the instance and the temp directory path the harness created.
WorkspaceSetup = Callable[[EvalInstance, str], None]


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
        manager = tracer.start_as_current_span(name, attributes=attributes)
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
        self._global_usage = BudgetUsage()
        # O2: set by run(); holds the spans this run produced.  ``None`` means
        # capture was disabled or unavailable, which _finalize records honestly
        # rather than treating as "no spans were expected".
        self._capture: TraceCapture | None = None
        # Per-instance usage; rebound at the top of each instance so the
        # process-lifecycle hooks below always target the live budget.
        self._current_usage: BudgetUsage = BudgetUsage()
        if self.checkpoint_path is not None and self.checkpoint_path.exists():
            for line in self.checkpoint_path.read_text(encoding="utf-8").splitlines():
                if line.strip():
                    self._completed.add(line.strip())

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
            self.checkpoint_path.parent.mkdir(parents=True, exist_ok=True)
            with self.checkpoint_path.open("a", encoding="utf-8") as handle:
                handle.write(instance_id + "\n")

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
        }

        # O2: install the in-process span capture and open the run root span.
        # The capture must be installed *before* the first span is created, and
        # the run span must be closed before _finalize() reads the spans, or the
        # root would be missing from its own artifact.
        capture: TraceCapture | None = None
        tracer = None
        if self.config.get("trace_capture", True):
            capture = TraceCapture()
            if capture.install():
                tracer = _get_tracer()
        self._capture = capture

        with _span(tracer, SPAN_EVAL_RUN, self._run_span_attributes()):
            self._run_instances(instances, summary, tracer)

        summary["ok"] = summary["completed"]
        summary["failed"] = sum(summary["by_category"].values())
        summary["skipped"] = summary["resumed_skipped"]

        path = _finalize(self, summary)
        return {"summary": summary, "summary_path": path}

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
        # Populate workspace (e.g. clone repo) before the agent runs
        if self.setup_workspace is not None:
            self.setup_workspace(instance, str(workspace))

        if not self.network_allowed:
            _block_network(workspace, self.network_allowlist)

        # Call adapter.  The join context is attached around the call so spans
        # created *inside* the adapter — the driver's tool spans, and the
        # orchestrator's ``chat`` — inherit ``eval.run_id``/``eval.instance_id``
        # without the orchestrator having to import anything from eval.  See
        # eval/harness/trace_join.py for what this does and does not prove.
        if self.adapter is not None:
            with eval_join_context(self.run_id, instance_id):
                result = self.adapter.solve_instance(instance, str(workspace))
            # H4: enforce the process cap on whatever the adapter
            # reported while it was running (fail closed, never
            # silently over-subscribe the machine).
            check_budget(self.budget, usage)
        else:
            result = EvalResult(
                instance_id=instance_id,
                error="no adapter configured",
            )

        # Feed budget from result
        usage.record_tokens(result.tokens_in + result.tokens_out)
        usage.record_cost(result.cost)
        # Wall clock is auto-captured by BudgetUsage.started_at

        # Post-solve budget check
        check_budget(self.budget, usage)
        # Update global usage for pre-start checks on next instances
        self._global_usage.record_tokens(usage.tokens)
        self._global_usage.record_cost(usage.cost)

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
                    scorer_result = self.scorer(result, instance, workspace)
                except Exception as scorer_exc:
                    # scorer exception → ERROR_SCORER (now reachable!).  Raised
                    # inside the span's with-block so the span is ended with
                    # ERROR status rather than left UNSET.
                    raise ScorerError(str(scorer_exc)) from scorer_exc
                # A benchmark may hand back the official harness's raw
                # output under this reserved key.  It is persisted under
                # scorer/ (so checksums.sha256 pins it) instead of being
                # inlined into the prediction row, which is one JSON
                # line per instance and must stay readable.
                raw_outputs = scorer_result.pop(SCORER_RAW_OUTPUT_KEY, None)
                if isinstance(raw_outputs, dict):
                    for name, content in raw_outputs.items():
                        self.artifacts.record_scorer_output(name, str(content))
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

    Order matters (H3, design map §20.7): summary and environment first,
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
        if capture is None:
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
            spans, run_id=harness.run_id, capabilities=capabilities
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




def _build_manifest(harness: HarnessRun, summary: dict[str, Any]) -> dict[str, Any]:
    """Build run-manifest.json from harness config and run summary.

    Fail-closed (design map §20.7) via :mod:`eval.harness.pin_contract`.  This
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
        "model": pin_values["model"],
        "model_revision": pin_values["model_revision"],
        "prompt_hash": pin_values["prompt_hash"],
        "corpus_generation": pin_values["corpus_generation"],
        "qrels_hash": pin_values["qrels_hash"],
        "physical_index": pin_values["physical_index"],
        "index_mapping_hash": config.get("index_mapping_hash", ""),
        "dataset_pin": config.get("dataset_pin", {}),
        "budgets": {
            "wall_clock_seconds": harness.budget.wall_clock_seconds,
            "max_tokens": harness.budget.max_tokens,
            "max_cost": harness.budget.max_cost,
            "max_output_bytes": harness.budget.max_output_bytes,
            "max_processes": harness.budget.max_processes,
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
            f"category: {category}\nerror: {message}\n",
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
