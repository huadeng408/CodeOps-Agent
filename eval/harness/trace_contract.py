"""O1: the unified, versioned trace acceptance contract.

Why this module exists
----------------------
Before it, three different definitions of "a valid run trace" coexisted:

* ``scripts/eval/trace_assert.py`` required 5 span kinds,
* ``tests/integration/trace_e2e.py`` required a root, one ``Read`` tool span and
  two ``chat`` spans,
* the Phoenix trace on disk was a 5-span synthetic chain containing no RAG spans
  at all (§20.3.1).

With three contracts, "the trace is fine" was unfalsifiable — whichever
definition a run happened to satisfy could be cited.  §20.6.4 item 1 therefore
asks for **one versioned schema** covering root, instance, agent/chat, tool,
retrieve, embedding, rerank (only when enabled) and the official scorer, with
every span in the same trace (or joined by a legal span link) and carrying
``eval.run_id`` / ``eval.instance_id`` plus non-sensitive Git/model/dataset/index
pins.  This module is that schema plus its validator.

Verdict semantics
-----------------
``PASS`` / ``INCOMPLETE`` / ``FAIL`` are deliberately three values, not two,
mirroring the contamination scanner's ``CLEAN`` / ``INCOMPLETE`` / ``BLOCKING``
(§29-§30) for the same anti-cheating reason:

* ``FAIL``       — a span exists but violates the contract (wrong run, split
                   trace, missing pins, secret leak).  A real defect.
* ``INCOMPLETE`` — the spans present are all valid, but required kinds were
                   never produced.  This is the honest verdict when the Go agent
                   or the RAG stack was not running: the recording is truthful,
                   the *chain* is not yet closed.
* ``PASS``       — every required kind present and every rule satisfied.

An empty capture is ``INCOMPLETE``, never ``PASS``.  That single rule is what
stops the H5 gate's "trace" item from being satisfiable by doing nothing, and it
is why :func:`evaluate_trace_contract` refuses to treat "no problems found" as
success without also checking that something was actually observed.

This module is pure: no OTel import, no I/O.  Capture lives in
``eval/harness/trace_capture.py`` so the contract can be evaluated against spans
from any source (in-process capture today, a Phoenix query under O3).
"""

from __future__ import annotations

import re
from dataclasses import dataclass, field
from typing import Any, Iterable, Mapping, Sequence

#: Bumped whenever the required-kind set or a rule changes, so an artifact's
#: verdict stays interpretable after the schema moves on.  An artifact that
#: records only "PASS" without a version cannot be re-checked later.
CONTRACT_VERSION = "v2"

TRACE_PROFILE_DEFAULT = "default"
TRACE_PROFILE_O3 = "o3"
_KNOWN_PROFILES = frozenset({TRACE_PROFILE_DEFAULT, TRACE_PROFILE_O3})

# -- span names -------------------------------------------------------------
# Harness-owned names are defined here; the agent/orchestrator names match what
# those processes already emit (Go ``invoke_agent``/``execute_tool``, Python
# ``chat``/``rag.retrieve``), so the contract describes reality rather than
# renaming it.
SPAN_EVAL_RUN = "eval.run"
SPAN_EVAL_INSTANCE = "eval.instance"
SPAN_INVOKE_AGENT = "invoke_agent"
SPAN_CHAT = "chat"
SPAN_EXECUTE_TOOL = "execute_tool"
SPAN_RAG_RETRIEVE = "rag.retrieve"
SPAN_EMBEDDING = "embedding"
SPAN_RERANK = "rerank"
SPAN_SCORER_OFFICIAL = "scorer.official"

#: Unified join attribute set in stable serialization order.
JOIN_ATTRIBUTES = (
    "eval.run_id",
    "eval.instance_id",
    "git.commit",
    "rag.query_hash",
    "rag.corpus_generation",
    "rag.index_name",
    "rag.retrieval_mode",
)

#: Attributes a retrieval span must carry for the RAG evidence to be traceable
#: back to a specific corpus generation and physical index (§4.2).
RAG_REQUIRED_ATTRIBUTES = (
    "rag.query_hash",
    "rag.corpus_generation",
    "rag.index_name",
    "rag.retrieval_mode",
)

VERDICT_PASS = "PASS"
VERDICT_INCOMPLETE = "INCOMPLETE"
VERDICT_FAIL = "FAIL"

# Producers, used to attribute a missing kind to the process that owes it.
# "INCOMPLETE" without this is an unactionable verdict: the next window cannot
# tell whether to start the Go server, the embedding service, or neither.
PRODUCER_HARNESS = "harness"
PRODUCER_GO_AGENT = "go-agent"
PRODUCER_ORCHESTRATOR = "orchestrator"

#: The process that actually ran the agent loop and its tools.  ``invoke_agent``
#: and ``execute_tool`` were previously attributed to ``go-agent``, which is only
#: true for the gRPC path: the headless eval driver executes Read/Write/Edit/
#: Bash/Glob/Grep in-process via ``eval.driver_headless.LocalToolExecutor``.
#: Naming the Go agent there sends the next window to start a server that this
#: path never contacts, which is the same "unactionable verdict" defect the
#: producer field exists to prevent.  Waiving the two kinds instead would be
#: worse: they are the only evidence that tool work happened at all.
PRODUCER_AGENT_RUNTIME = "agent-runtime"


# Capabilities a run may or may not exercise.  A SWE-bench instance is fixed by
# reading and patching a checked-out repository: it performs no retrieval at
# all, so demanding ``rag.retrieve``/``embedding`` from it makes ``PASS``
# unreachable.  An unreachable ``PASS`` is the mirror image of a preflight that
# cannot fail — both produce a verdict that carries no information.  Gate those
# kinds on the capability instead, exactly as ``rerank`` was already gated.
CAPABILITY_RAG = "rag"
CAPABILITY_RERANK = "rerank"

#: Capabilities assumed when a caller declares nothing.  Deliberately *all* of
#: them: an omitted declaration must not silently weaken the contract.
ALL_CAPABILITIES: frozenset[str] = frozenset({CAPABILITY_RAG, CAPABILITY_RERANK})


@dataclass(frozen=True)
class SpanKind:
    """One span kind in the acceptance contract.

    ``required=False`` marks a kind whose absence is legitimate — currently only
    ``rerank``, which both §9.2 and §20.6.4 qualify with "启用时" (when enabled).
    A run with the reranker off must not be marked incomplete for obeying its
    own configuration.

    ``capability`` names the run capability this kind depends on.  When the run
    does not exercise that capability the kind is waived — but the waiver is
    recorded in the artifact, and emitting the span anyway while declaring the
    capability off is reported as a contradiction.  Otherwise "we don't do RAG"
    would be a free pass that any caller could assert.
    """

    name: str
    producer: str
    required: bool = True
    description: str = ""
    required_attributes: tuple[str, ...] = ()
    capability: str = ""


_SPAN_KINDS: tuple[SpanKind, ...] = (
    SpanKind(
        name=SPAN_EVAL_RUN,
        producer=PRODUCER_HARNESS,
        description="run root; pins git/model/benchmark",
        required_attributes=("git.commit",),
    ),
    SpanKind(
        name=SPAN_EVAL_INSTANCE,
        producer=PRODUCER_HARNESS,
        description="one benchmark instance",
    ),
    SpanKind(
        name=SPAN_INVOKE_AGENT,
        producer=PRODUCER_AGENT_RUNTIME,
        description="agent loop root (Go agent, or the headless driver in-process)",
    ),
    SpanKind(
        name=SPAN_EXECUTE_TOOL,
        producer=PRODUCER_AGENT_RUNTIME,
        description="one tool invocation, wherever the tool actually ran",
    ),
    SpanKind(
        name=SPAN_CHAT,
        producer=PRODUCER_ORCHESTRATOR,
        description="LLM call",
    ),
    SpanKind(
        name=SPAN_RAG_RETRIEVE,
        producer=PRODUCER_ORCHESTRATOR,
        description="retrieval over a pinned physical index; only when RAG runs",
        required_attributes=RAG_REQUIRED_ATTRIBUTES,
        capability=CAPABILITY_RAG,
    ),
    SpanKind(
        name=SPAN_EMBEDDING,
        producer=PRODUCER_ORCHESTRATOR,
        description="query/document embedding; only when RAG runs",
        capability=CAPABILITY_RAG,
    ),
    SpanKind(
        name=SPAN_RERANK,
        producer=PRODUCER_ORCHESTRATOR,
        required=False,
        description="reranker; only when enabled",
    ),
    SpanKind(
        name=SPAN_SCORER_OFFICIAL,
        producer=PRODUCER_HARNESS,
        description="official scorer invocation",
    ),
)

#: Required kinds in declaration order, assuming every capability is on.
#: ``rerank`` is absent by construction.  Use :func:`required_span_kinds` when
#: the run's capabilities are known.
REQUIRED_SPAN_KINDS: tuple[str, ...] = tuple(
    kind.name for kind in _SPAN_KINDS if kind.required
)


def span_kinds() -> tuple[SpanKind, ...]:
    """The full versioned schema, including conditional kinds."""
    return _SPAN_KINDS


def required_span_kinds(
    capabilities: Iterable[str] | None = None,
) -> tuple[str, ...]:
    """Kinds a run must emit, given the capabilities it actually exercises.

    ``capabilities=None`` means "not declared" and keeps every kind required, so
    forgetting to declare can only ever make the contract stricter.
    """
    enabled = ALL_CAPABILITIES if capabilities is None else frozenset(capabilities)
    return tuple(
        kind.name
        for kind in _SPAN_KINDS
        if kind.required and (not kind.capability or kind.capability in enabled)
    )


@dataclass
class CapturedSpan:
    """A finished span, normalised away from any particular SDK.

    ``links`` holds the trace ids this span links to, which is how §20.6.4's
    "same trace **or** a legal span link" escape hatch is represented: a span in
    its own trace is acceptable precisely when it links back to the primary one.
    """

    name: str
    trace_id: str
    span_id: str
    parent_span_id: str = ""
    status: str = "UNSET"
    attributes: dict[str, Any] = field(default_factory=dict)
    links: tuple[str, ...] = ()
    ended: bool = True

    def to_dict(self) -> dict[str, Any]:
        return {
            "name": self.name,
            "trace_id": self.trace_id,
            "span_id": self.span_id,
            "parent_span_id": self.parent_span_id,
            "status": self.status,
            "attributes": dict(self.attributes),
            "links": list(self.links),
            "ended": self.ended,
        }


# -- secret detection -------------------------------------------------------
# §9.2 forbids recording raw API keys, internal tokens, DSNs, full sensitive
# queries and full prompts.  These patterns target credential *shapes* rather
# than any specific value, so no real secret appears in this file.  They are
# deliberately narrow: a false positive would push honest runs to FAIL and
# create pressure to switch the check off, which is worse than a narrow check.
_SECRET_PATTERNS: tuple[tuple[str, re.Pattern[str]], ...] = (
    ("api-key", re.compile(r"\bsk-[A-Za-z0-9_\-]{16,}")),
    ("bearer-token", re.compile(r"\bBearer\s+[A-Za-z0-9._\-]{16,}")),
    ("dsn-with-password", re.compile(r"\b[a-z][a-z0-9+.\-]*://[^/\s:@]+:[^/\s@]+@")),
    ("aws-access-key", re.compile(r"\b(?:AKIA|ASIA)[0-9A-Z]{16}\b")),
)

_O3_PROHIBITED_ATTRIBUTE_KEYS = frozenset(
    {
        "gen_ai.tool.call.arguments",
        "gen_ai.tool.call.result",
        "rag.query",
        "gen_ai.prompt",
        "gen_ai.input.messages",
        "gen_ai.output.messages",
        "document.content",
        "textContent",
        "text_content",
    }
)


def find_secret_leak(value: Any) -> str | None:
    """Return the name of the matching secret shape, or ``None`` if clean."""
    text = str(value)
    for label, pattern in _SECRET_PATTERNS:
        if pattern.search(text):
            return label
    return None


def find_trace_privacy_problem(value: Any) -> str | None:
    """Inspect the backend object before any attribute allowlist hides fields."""
    if find_secret_leak(value):
        return "credential-shape"

    def contains_raw_content(item: Any, path: str = "") -> bool:
        if isinstance(item, Mapping):
            for key, child in item.items():
                name = path + "." + str(key)
                if any(name.endswith("." + prohibited) for prohibited in _O3_PROHIBITED_ATTRIBUTE_KEYS):
                    return True
                if contains_raw_content(child, name):
                    return True
        elif isinstance(item, list):
            return any(contains_raw_content(child, path) for child in item)
        return False

    return "raw-content" if contains_raw_content(value) else None


def redact_if_secret(value: Any) -> Any:
    """Replace *value* with ``<redacted>`` when it looks like a credential."""
    return "<redacted>" if find_secret_leak(value) is not None else value


def _primary_trace_id(spans: Sequence[CapturedSpan]) -> str:
    """The trace id of the run root, else the most common trace id.

    Falling back to "most common" matters for a partial capture: if the root
    span was lost, the bulk of the spans still identify the trace under
    examination, so the report can name it instead of reporting nothing.
    """
    for span in spans:
        if span.name == SPAN_EVAL_RUN:
            return span.trace_id
    counts: dict[str, int] = {}
    for span in spans:
        counts[span.trace_id] = counts.get(span.trace_id, 0) + 1
    if not counts:
        return ""
    return max(counts.items(), key=lambda item: (item[1], item[0]))[0]


def _matches_kind(span_name: str, kind_name: str) -> bool:
    """Kind match by prefix.

    OTel names are frequently suffixed with their subject — the Go side emits
    ``invoke_agent code-agent`` and ``execute_tool Read``.  Prefix matching lets
    the contract accept those without forcing either language to rename spans.
    """
    return span_name == kind_name or span_name.startswith(kind_name + " ")


def _product_tool_outcomes(expected, requested, problems, missing):
    facts_by_run = expected.get("tool_facts_by_run", {})
    run_ids = expected.get("run_ids", [expected["run_id"]])
    if set(facts_by_run) != set(run_ids):
        problems.append("complete native tool facts are required")
    dispatched, outcomes = {}, []
    for run_id, facts in facts_by_run.items():
        if any((run_id, call_id) not in requested for call_id in facts):
            problems.append("native tool fact has no matching request")
    for (run_id, call_id), name in requested.items():
        facts = facts_by_run.get(run_id, {}).get(call_id, [])
        calls = [fact for fact in facts if fact["type"] == "tool/call"]
        dispatch = [fact for fact in facts if fact["type"] == "tool/dispatched"]
        denied = [fact for fact in facts if fact["type"] == "approval/denied"]
        results = [fact for fact in facts if fact["type"] == "tool/result"]
        state = "unknown"
        argument_pins = [fact.get("arguments_sha256") for fact in facts
                         if fact["type"] in {"tool/call", "tool/dispatched", "approval/denied"}]
        if (len(calls) != 1 or len(dispatch) > 1 or len(denied) > 1 or len(results) > 1
                or len(set(argument_pins)) != 1
                or any(not isinstance(pin, str) or not re.fullmatch(r"[0-9a-f]{64}", pin) for pin in argument_pins)
                or any(fact.get("tool_name") != name for fact in facts)
                or any(fact["seq"] <= calls[0]["seq"] for fact in facts if fact["type"] != "tool/call")):
            problems.append("native tool facts do not match the request")
        elif len(results) != 1:
            missing.append("tool.result:" + run_id + "/" + call_id)
        else:
            result = results[0]
            known = (not result.get("outcome_unknown") and not any(fact["type"] == "tool/unknown" for fact in facts)
                     and (not result.get("runtime_started") or result.get("stopped") is True)
                     and type(result.get("exit_code", 0)) is int)
            if (denied and not dispatch and known and denied[0].get("decision") == "denied"
                    and denied[0]["seq"] < result["seq"] and result.get("exit_code", 0) != 0
                    and result.get("has_error") and not result.get("runtime_started")):
                state = "denied"
            elif dispatch and not denied and known and dispatch[0]["seq"] < result["seq"]:
                state = "failed" if result.get("exit_code", 0) or result.get("has_error") else "succeeded"
        if dispatch:
            dispatched[(run_id, call_id)] = name
        if state == "unknown":
            problems.append("tool outcome or dispatch authorization is unknown")
        outcomes.append({"run_id": run_id, "tool_call_id": call_id, "tool_name": name,
                         "dispatched": bool(dispatch), "state": state})
    return dispatched, outcomes


def _product_attempt_roots(items, roots, expected, problems):
    run_ids = expected.get("run_ids", [expected["run_id"]])
    attempts = expected.get("attempts_by_run", {})
    if (set(attempts) != set(run_ids) or expected.get("run_attempts_valid", True) is not True
            or len(set(run_ids)) != len(run_ids)
            or any(type(count) is not int or count < 1 for count in attempts.values())
            or {root.attributes.get("session.run_id") for root in roots} != set(run_ids)
            or any(sum(root.attributes.get("session.run_id") == run_id for root in roots) != count
                   for run_id, count in attempts.items())):
        problems.append("Go execution roots do not match the native attempt denominator")
    contexts = {(span.trace_id, span.span_id): span for span in items}
    owners = {}
    for span in items:
        current, owner, seen = span, None, set()
        while True:
            key = (current.trace_id, current.span_id)
            if key in seen:
                problems.append("product span ancestry is cyclic")
                break
            seen.add(key)
            if current.name == "agent.main":
                if owner is None:
                    owner = current
                elif current.attributes.get("session.run_id") != owner.attributes.get("session.run_id"):
                    problems.append("recovered Go roots cross durable runs")
            if not current.parent_span_id:
                if current.name != "agent.main":
                    problems.append("product span does not reach the Go root")
                break
            parent = contexts.get((current.trace_id, current.parent_span_id))
            if parent is None:
                problems.append("product span parent is missing or in another trace")
                break
            current = parent
        if owner is None:
            problems.append("product span does not reach the Go root")
        else:
            owners[(span.trace_id, span.span_id)] = owner
    return owners


def evaluate_product_trace(spans: Iterable[CapturedSpan], expected: Mapping[str, Any], *, contract_version: str = "product-v1") -> dict[str, Any]:
    """Check the product's selected run trees against its durable denominator."""
    if contract_version not in {"product-v1", "product-integrity-v2"}:
        raise ValueError("unknown product Trace contract")
    integrity = contract_version == "product-integrity-v2"
    items = list(spans)
    problems, missing = [], []
    session_id, run_id = expected["session_id"], expected["run_id"]
    run_ids = expected.get("run_ids", [run_id])
    tools_by_run = expected.get("tool_calls_by_run")
    if tools_by_run is None:
        tools_by_run = {run_id: expected["tool_calls"]}
    tools = {(run, call_id): name for run, calls in tools_by_run.items() for call_id, name in calls.items()}
    requested_tools = tools
    if integrity:
        tools, tool_outcomes = _product_tool_outcomes(expected, requested_tools, problems, missing)
    models = expected["model_calls"]
    retry_of = expected.get("retry_of", {})
    logical_calls = {call_id: purpose for call_id, purpose in models.items() if not retry_of.get(call_id)}
    if not integrity and expected.get("completed") is not True:
        problems.append("the durable product run did not complete successfully")
    if not requested_tools or not models:
        problems.append("a coding product trace requires actual tool and model calls")
    if set(tools_by_run) != set(run_ids):
        problems.append("tool run identities do not match the durable runs")
    roots = [span for span in items if span.name == "agent.main"]
    roots_by_trace = {span.trace_id: span for span in roots}
    root = roots[0] if roots else None
    if not roots:
        missing.append("agent.main")
    elif not integrity and (len(roots) != len(run_ids) or len(set(run_ids)) != len(run_ids)
          or len(roots_by_trace) != len(roots)
          or {span.attributes.get("session.run_id") for span in roots} != set(run_ids)):
        problems.append("product capture must contain exactly one Go root per requested run")
    if any(span.attributes.get("session.id") != session_id for span in roots):
        problems.append("Go root does not match the durable Session/run")
    if not integrity and any(span.parent_span_id for span in roots):
        problems.append("Go task root must not have a parent")
    by_id = {span.span_id: span for span in items}
    contexts = {(span.trace_id, span.span_id): span for span in items}
    if len(contexts if integrity else by_id) != len(items):
        problems.append("duplicate span IDs")
    owners = _product_attempt_roots(items, roots, expected, problems) if integrity else {}
    for span in items:
        if (not re.fullmatch(r"[0-9a-f]{32}", span.trace_id) or span.trace_id == "0" * 32
                or not re.fullmatch(r"[0-9a-f]{16}", span.span_id) or span.span_id == "0" * 16):
            problems.append("invalid W3C span identity")
        if not span.ended:
            missing.append("unfinished span")
        if integrity and str(span.status).upper() not in {"OK", "UNSET", "ERROR", "CANCELED", "CANCELLED"}:
            problems.append("product span has an unknown status")
        elif not integrity and str(span.status).upper() not in {"OK", "UNSET"}:
            problems.append("product tree includes an error or canceled span")
        if find_secret_leak(span.attributes) or find_secret_leak(span.name):
            problems.append("product tree contains a credential shape")
        if integrity:
            continue
        owning_root = roots_by_trace.get(span.trace_id)
        if owning_root is None:
            if roots:
                problems.append("product span is in another trace")
            continue
        current, seen = span, set()
        while current.span_id != owning_root.span_id:
            if current.span_id in seen or current.parent_span_id not in by_id:
                problems.append("product span does not reach the Go root")
                break
            seen.add(current.span_id)
            current = by_id[current.parent_span_id]
            if current.trace_id != span.trace_id:
                problems.append("product parent is in another trace")
                break
    if len(run_ids) > 1 or integrity:
        foreground = [span for span in items if (span.name == "provider.call" or integrity and _matches_kind(span.name, "provider.call"))
                      and span.attributes.get("model.purpose") == "foreground"]
        foreground_runs = {owners[(span.trace_id, span.span_id)].attributes.get("session.run_id") for span in foreground
                           if (span.trace_id, span.span_id) in owners}
        if (integrity and set(run_ids) - foreground_runs) or (not integrity and any(span.trace_id not in {call.trace_id for call in foreground} for span in roots)):
            problems.append("a Go run has no foreground Python provider call")
    for name, calls, id_key, value_key in (
        ("execute_tool", tools, "gen_ai.tool.call.id", "gen_ai.tool.name"),
        ("provider.call", logical_calls, "model.call_id", "model.purpose"),
        ("provider.attempt", models, "model.call_id", "model.purpose"),
    ):
        observed = {}
        for span in items:
            if not _matches_kind(span.name, name):
                continue
            call_id = span.attributes.get(id_key)
            if name == "execute_tool":
                owning_root = owners.get((span.trace_id, span.span_id)) if integrity else roots_by_trace.get(span.trace_id)
                call_id = (owning_root.attributes.get("session.run_id") if owning_root else "", call_id)
            if call_id in observed:
                problems.append("duplicate call spans")
            observed[call_id] = span.attributes.get(value_key)
        missing.extend(name + ":" + ("/".join(call_id) if isinstance(call_id, tuple) else call_id) for call_id in calls if call_id not in observed)
        if any(call_id not in calls or calls[call_id] != value for call_id, value in observed.items()):
            problems.append("call spans do not match the durable call identities")
    for span in items:
        kind = next((name for name in ("provider.call", "provider.attempt") if _matches_kind(span.name, name)), "") if integrity else span.name
        if kind not in {"provider.call", "provider.attempt"}:
            continue
        if span.attributes.get("gen_ai.request.model") != expected["model"]:
            problems.append("provider model does not match the frozen request model")
        parent = contexts.get((span.trace_id, span.parent_span_id)) if integrity else by_id.get(span.parent_span_id)
        if kind == "provider.call" and span.attributes.get("model.purpose") == "foreground":
            if parent is None or parent.name != "chat":
                problems.append("foreground provider call is not a child of Python chat")
        if kind == "provider.attempt":
            call_id, seen = span.attributes.get("model.call_id"), set()
            if span.attributes.get("model.retry_of", "") != retry_of.get(call_id, ""):
                problems.append("provider retry lineage differs from the Ledger")
            while retry_of.get(call_id):
                if call_id in seen:
                    problems.append("cyclic provider retry lineage")
                    break
                seen.add(call_id)
                call_id = retry_of[call_id]
            if (parent is None or not (parent.name == "provider.call" or integrity and _matches_kind(parent.name, "provider.call"))
                    or parent.attributes.get("model.call_id") != call_id):
                problems.append("provider attempt is not a child of its logical Python call")
    if integrity:
        outcomes = expected.get("run_outcomes", {})
        if set(outcomes) != set(run_ids) or any(state not in {"completed", "failed", "canceled"} for state in outcomes.values()):
            problems.append("durable execution outcomes are incomplete")
        cancellations = expected.get("cancellation_states", {})
        if set(cancellations) != {run for run, state in outcomes.items() if state == "canceled"} or any(state != "stopped" for state in cancellations.values()):
            problems.append("canceled execution has unconfirmed side effects")
    verdict = VERDICT_FAIL if problems else VERDICT_INCOMPLETE if missing or not items else VERDICT_PASS
    report = {"contract_version": contract_version if integrity else "product-task-v1" if len(run_ids) > 1 else "product-v1", "profile": "product", "verdict": verdict,
            "session_id": session_id, "run_id": run_id, "primary_trace_id": root.trace_id if root else "",
            "run_ids": list(run_ids), "trace_ids": sorted(roots_by_trace),
            "span_count": len(items), "expected_tools": len(requested_tools), "expected_model_calls": len(models),
            "expected_logical_calls": len(logical_calls),
            "missing": sorted(set(missing)), "problems": sorted(set(problems))}
    if integrity:
        report.update(expected_dispatched_tools=len(tools),
                      execution={"runs": outcomes, "cancellation_states": cancellations, "tools": tool_outcomes},
                      error_spans=[{"trace_id": span.trace_id, "span_id": span.span_id, "name": span.name, "status": span.status}
                                   for span in items if str(span.status).upper() not in {"OK", "UNSET"}])
    return report


def evaluate_trace_contract(
    spans: Iterable[CapturedSpan],
    run_id: str,
    capabilities: Iterable[str] | None = None,
    profile: str = TRACE_PROFILE_DEFAULT,
    expected_instance_ids: Iterable[str] = (),
) -> dict[str, Any]:
    """Evaluate *spans* against the contract; return the span-assertion report.

    The report is the artifact written to ``traces/span-assertion.json``.  It
    always reports what was observed — including error spans and missing kinds —
    because §20.1 rule 7 keeps failures in the denominator and §20.2 rejects any
    report that hides them.

    *capabilities* declares what this run exercises (see :data:`ALL_CAPABILITIES`).
    A SWE-bench run performs no retrieval, so requiring ``rag.retrieve`` of it
    would make ``PASS`` unreachable and the verdict uninformative.  Declaring a
    capability off waives its kinds, but the waiver is recorded in the artifact
    and a waived kind that shows up anyway is reported as a contradiction — so
    the declaration is auditable rather than a free pass.
    """
    span_list = list(spans)
    problems: list[str] = []

    if profile not in _KNOWN_PROFILES:
        problems.append(f"unknown trace profile {profile!r}")

    declared = ALL_CAPABILITIES if capabilities is None else frozenset(capabilities)
    unknown = sorted(declared - ALL_CAPABILITIES)
    if unknown:
        problems.append(
            f"unknown capabilities declared: {unknown}; "
            f"known capabilities are {sorted(ALL_CAPABILITIES)}"
        )

    present_kinds: list[str] = []
    for kind in _SPAN_KINDS:
        if any(_matches_kind(span.name, kind.name) for span in span_list):
            present_kinds.append(kind.name)

    required_now = set(required_span_kinds(declared))
    waived_kinds = [
        kind.name
        for kind in _SPAN_KINDS
        if kind.required and kind.name not in required_now
    ]

    # A kind waived by declaration that nevertheless appears means the
    # declaration misdescribes the run.  Surface it instead of quietly
    # accepting both stories.
    for name in waived_kinds:
        if name in present_kinds:
            problems.append(
                f"span kind {name!r} was waived because its capability was "
                "declared off, but the run emitted it anyway; the capability "
                "declaration does not match the run"
            )

    missing_required = [
        kind.name
        for kind in _SPAN_KINDS
        if kind.name in required_now and kind.name not in present_kinds
    ]
    missing_by_producer: dict[str, list[str]] = {}
    for kind in _SPAN_KINDS:
        if kind.name in missing_required:
            missing_by_producer.setdefault(kind.producer, []).append(kind.name)

    primary_trace_id = _primary_trace_id(span_list)

    for span in span_list:
        label = f"span {span.name!r}"

        if not span.ended:
            problems.append(f"{label} was not ended")
        status = str(span.status).upper()
        if status in {"ERROR", "CANCELLED", "TIMEOUT", "SKIPPED"}:
            if profile == TRACE_PROFILE_O3:
                problems.append(f"O3 success chain includes {status} span {span.name!r}")
            else:
                problems.append(f"{label} has disallowed status {status}")

        # Same trace, or an explicit link back to it.
        if span.trace_id != primary_trace_id and primary_trace_id:
            if primary_trace_id not in span.links:
                problems.append(
                    f"{label} is in trace {span.trace_id!r}, not the primary "
                    f"trace {primary_trace_id!r}, and carries no span link to it"
                )

        # Join attributes.  The keys must be present on every eval-owned span;
        # eval.run_id must additionally agree with the run being reported, or
        # the artifact would attribute one run's evidence to another.
        if "eval.run_id" not in span.attributes:
            problems.append(f"{label} is missing join attribute eval.run_id")
        elif str(span.attributes["eval.run_id"]) != run_id:
            problems.append(
                f"{label} has eval.run_id={span.attributes['eval.run_id']!r}, "
                f"expected {run_id!r}"
            )
        if "eval.instance_id" not in span.attributes:
            problems.append(f"{label} is missing join attribute eval.instance_id")

        # Kind-specific pins.
        for kind in _SPAN_KINDS:
            if not _matches_kind(span.name, kind.name):
                continue
            for attribute in kind.required_attributes:
                if attribute not in span.attributes:
                    problems.append(
                        f"{label} ({kind.name}) is missing required "
                        f"attribute {attribute}"
                    )

        # Secret leakage.
        for key, value in span.attributes.items():
            leak = find_secret_leak(value)
            if leak is not None:
                problems.append(
                    f"{label} attribute {key!r} looks like a secret ({leak}); "
                    "§9.2 forbids recording credentials in spans"
                )

    if profile == TRACE_PROFILE_O3:
        _evaluate_o3_topology(span_list, problems, expected_instance_ids)

    if not span_list:
        problems.append(
            "no spans were captured; a run that produced no trace cannot "
            "satisfy the trace requirement"
        )

    if problems:
        verdict = VERDICT_FAIL if span_list else VERDICT_INCOMPLETE
    elif missing_required:
        verdict = VERDICT_INCOMPLETE
    else:
        verdict = VERDICT_PASS

    return {
        "contract_version": CONTRACT_VERSION,
        "profile": profile,
        "run_id": run_id,
        "verdict": verdict,
        "primary_trace_id": primary_trace_id,
        "span_count": len(span_list),
        "error_span_count": sum(1 for span in span_list if span.status == "ERROR"),
        "present_kinds": present_kinds,
        "missing_required_kinds": missing_required,
        "missing_by_producer": missing_by_producer,
        "conditional_kinds": [
            kind.name for kind in _SPAN_KINDS if not kind.required
        ],
        # What the run said it does, and which required kinds that waived.  A
        # reader can re-derive the verdict from these two fields alone.
        "declared_capabilities": sorted(declared),
        "waived_required_kinds": waived_kinds,
        "join_attributes": list(JOIN_ATTRIBUTES),
        "problems": problems,
    }


def _evaluate_o3_topology(
    spans: Sequence[CapturedSpan],
    problems: list[str],
    expected_instance_ids: Iterable[str],
) -> None:
    """Enforce the non-negotiable, real O3 evidence topology.

    Generic harness runs may legitimately omit RAG or emit partial evidence.
    O3 is the strict end-to-end receipt and cannot use those degradations.
    """
    by_id: dict[str, CapturedSpan] = {}
    by_kind: dict[str, list[CapturedSpan]] = {}
    for span in spans:
        if span.span_id in by_id:
            problems.append(f"duplicate span identity {span.trace_id!r}/{span.span_id!r}")
        else:
            by_id[span.span_id] = span
        for kind in _SPAN_KINDS:
            if _matches_kind(span.name, kind.name):
                by_kind.setdefault(kind.name, []).append(span)
                break
        for attr in ("rag.degraded", "rag.reranker_timeout"):
            if span.attributes.get(attr) is True:
                problems.append(f"O3 success chain has {attr}=true on {span.name!r}")
        for key in span.attributes:
            if key in _O3_PROHIBITED_ATTRIBUTE_KEYS:
                problems.append(
                    f"O3 span {span.name!r} has prohibited sensitive attribute {key!r}"
                )

    for child in spans:
        if child.parent_span_id and child.parent_span_id not in by_id:
            problems.append(f"span {child.name!r} has missing parent {child.parent_span_id!r}")

    roots = by_kind.get(SPAN_EVAL_RUN, [])
    if len(roots) != 1:
        problems.append(f"O3 requires exactly one eval.run root, found {len(roots)}")
        return
    root = roots[0]
    if root.parent_span_id:
        problems.append("O3 eval.run must be a root span")

    # Every O3 evidence span must reach the sole root through parent pointers.
    # This catches a self-consistent detached mini-tree that happens to have
    # plausible local parents, as well as explicit cycles.
    for span in spans:
        seen: set[str] = set()
        cursor = span
        while cursor.parent_span_id:
            if cursor.span_id in seen:
                problems.append(f"O3 parent cycle includes span {cursor.span_id!r}")
                break
            seen.add(cursor.span_id)
            parent = by_id.get(cursor.parent_span_id)
            if parent is None:
                break
            cursor = parent
        else:
            if cursor.span_id != root.span_id:
                problems.append(f"O3 span {span.name!r} is detached from eval.run")

    instances = by_kind.get(SPAN_EVAL_INSTANCE, [])
    if not instances:
        problems.append("O3 requires at least one eval.instance")
    for instance in instances:
        if instance.parent_span_id != root.span_id:
            problems.append("O3 eval.instance must be a direct child of eval.run")

    expected = set(expected_instance_ids)
    actual = {str(span.attributes.get("eval.instance_id", "")) for span in instances}
    if expected and actual != expected:
        problems.append(f"O3 eval.instance IDs {sorted(actual)!r} do not match expected {sorted(expected)!r}")

    agents = by_kind.get(SPAN_INVOKE_AGENT, [])
    tools = [span for span in by_kind.get(SPAN_EXECUTE_TOOL, []) if _matches_kind(span.name, "execute_tool SearchKnowledge")]
    retrieves = by_kind.get(SPAN_RAG_RETRIEVE, [])
    embeddings = by_kind.get(SPAN_EMBEDDING, [])
    reranks = by_kind.get(SPAN_RERANK, [])
    scorers = by_kind.get(SPAN_SCORER_OFFICIAL, [])
    if not reranks:
        problems.append("O3 requires a real rerank span")
    for agent in agents:
        if agent.parent_span_id not in {span.span_id for span in instances}:
            problems.append("O3 invoke_agent must be a child of eval.instance")
    for tool in tools:
        if tool.parent_span_id not in {span.span_id for span in agents}:
            problems.append("O3 SearchKnowledge tool must be a child of invoke_agent")
    tool_ids = {span.span_id for span in tools}
    for retrieve in retrieves:
        if retrieve.parent_span_id not in tool_ids:
            problems.append("O3 rag.retrieve must have execute_tool SearchKnowledge as its W3C parent")
        if retrieve.attributes.get("rag.reranker_applied") is not True:
            problems.append("O3 rag.retrieve must record rag.reranker_applied=true")
    retrieve_ids = {span.span_id for span in retrieves}
    for span in [*embeddings, *reranks]:
        if span.parent_span_id not in retrieve_ids:
            problems.append(f"O3 {span.name!r} must be a child of rag.retrieve")
    for scorer in scorers:
        if scorer.parent_span_id not in {span.span_id for span in instances}:
            problems.append("O3 scorer.official must be a child of eval.instance")
