"""O1: the unified, versioned trace acceptance contract (design map §20.6.4).

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
from typing import Any, Iterable, Sequence

#: Bumped whenever the required-kind set or a rule changes, so an artifact's
#: verdict stays interpretable after the schema moves on.  An artifact that
#: records only "PASS" without a version cannot be re-checked later.
CONTRACT_VERSION = "v1"

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

#: §9.2's unified join attribute set, in the order the design map lists them.
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

    def to_dict(self) -> dict[str, Any]:
        return {
            "name": self.name,
            "trace_id": self.trace_id,
            "span_id": self.span_id,
            "parent_span_id": self.parent_span_id,
            "status": self.status,
            "attributes": dict(self.attributes),
            "links": list(self.links),
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


def find_secret_leak(value: Any) -> str | None:
    """Return the name of the matching secret shape, or ``None`` if clean."""
    text = str(value)
    for label, pattern in _SECRET_PATTERNS:
        if pattern.search(text):
            return label
    return None


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


def evaluate_trace_contract(
    spans: Iterable[CapturedSpan],
    run_id: str,
    capabilities: Iterable[str] | None = None,
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
