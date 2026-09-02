"""Which manifest pins a run owes, and what an empty one is allowed to mean.

Defect 9 in the portfolio: ``_build_manifest`` demanded only ``git_sha`` and
``model``.  Everything else — ``model_revision``, ``prompt_hash``,
``qrels_hash``, ``physical_index`` — could be the empty string and the manifest
was written anyway, so a run with no recorded model identity and no index pin
looked indistinguishable from a fully pinned one.

The naive fix is to require them all.  That would be wrong twice over:

* Not every pin applies to every benchmark.  SWE-bench reads and patches a
  checked-out repository; it has no corpus, no qrels and no physical index.
  Demanding those pins would make a correct SWE-bench manifest impossible —
  the same unreachable-gate defect that the trace contract's capability gating
  exists to prevent.
* ``model_revision`` legitimately has no value for some providers. The E2
  identity contract says: *若 provider 不提供不可变 revision，状态必须为*
  ``MODEL_IDENTITY_UNVERIFIED``.  The required outcome is a recorded status,
  not an aborted run. Hard-failing would discard valid but limited evidence.

So pins fall into three classes:

``REQUIRED``
    Must be non-empty or the run refuses to write a manifest.
``CAPABILITY``
    Required only when the run exercises the named capability; waived
    otherwise, with the waiver recorded in the manifest.
``DEGRADABLE``
    May be empty, but emptiness must be *reported* as a named status so it can
    never be read as "verified".

The anti-cheating properties mirror ``trace_contract``: silence is strict (an
undeclared capability set means *all* capabilities are assumed on, so nothing
is waived by omission), the declaration is written into the artifact and is
therefore auditable, and a waived-but-populated pin is reported rather than
quietly accepted.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Iterable

CAPABILITY_RAG = "rag"

#: ``rerank`` carries no pin of its own, but it shares the ``TRACE_CAPABILITIES``
#: vocabulary with :mod:`eval.harness.trace_contract`.  Accepting it here keeps
#: one declaration serving both contracts instead of forcing benchmarks to
#: maintain two lists that could drift apart.
CAPABILITY_RERANK = "rerank"

ALL_CAPABILITIES: frozenset[str] = frozenset({CAPABILITY_RAG, CAPABILITY_RERANK})

#: Status recorded when nobody declared a capability set at all.
#:
#: ``trace_contract`` treats an omitted declaration as *every* capability on,
#: because there the only cost of over-strictness is a FAIL verdict written into
#: an artifact — loud, recoverable, and exactly what you want someone to see.
#: Here the cost is a *refused run*, and a harness assembled without any
#: benchmark module (every unit test, for one) legitimately has nothing to say
#: about RAG.  Aborting those would be the unreachable-gate defect again, so an
#: undeclared set waives the capability pins and records this status instead.
#:
#: The gate is not thereby weakened for real benchmarks: they reach this code
#: through ``eval/run.py``, which reads ``TRACE_CAPABILITIES`` off the benchmark
#: module, and ``tests/eval/test_pin_contract.py`` asserts every benchmark
#: module declares one.  So a real benchmark cannot arrive here undeclared, and
#: if one ever does, the status makes the omission visible in the artifact
#: rather than silently accepting empty pins.
STATUS_CAPABILITIES_UNDECLARED = "CAPABILITIES_UNDECLARED"

CLASS_REQUIRED = "required"
CLASS_CAPABILITY = "capability"
CLASS_DEGRADABLE = "degradable"

#: Status recorded when the provider gave no immutable revision (§20.6.3 E2).
STATUS_MODEL_IDENTITY_UNVERIFIED = "MODEL_IDENTITY_UNVERIFIED"


@dataclass(frozen=True)
class PinRule:
    """One manifest key and the conditions under which it may be empty."""

    key: str
    pin_class: str
    capability: str = ""
    empty_status: str = ""
    why: str = ""


_PIN_RULES: tuple[PinRule, ...] = (
    PinRule(
        key="git_sha",
        pin_class=CLASS_REQUIRED,
        why="a run that cannot name its own commit is not reproducible",
    ),
    PinRule(
        key="model",
        pin_class=CLASS_REQUIRED,
        why="the requested model is the minimum identity of what was measured",
    ),
    PinRule(
        key="prompt_hash",
        pin_class=CLASS_REQUIRED,
        why="two runs with different prompts are not comparable; the hash is "
        "computed locally so there is no provider that can fail to supply it",
    ),
    PinRule(
        key="model_revision",
        pin_class=CLASS_DEGRADABLE,
        empty_status=STATUS_MODEL_IDENTITY_UNVERIFIED,
        why="§20.6.3 E2 requires MODEL_IDENTITY_UNVERIFIED when the provider "
        "reports no immutable revision; hard-failing would contradict it",
    ),
    PinRule(
        key="corpus_generation",
        pin_class=CLASS_CAPABILITY,
        capability=CAPABILITY_RAG,
        why="identifies which corpus build was retrieved over",
    ),
    PinRule(
        key="qrels_hash",
        pin_class=CLASS_CAPABILITY,
        capability=CAPABILITY_RAG,
        why="retrieval metrics are meaningless without pinning the gold set",
    ),
    PinRule(
        key="physical_index",
        pin_class=CLASS_CAPABILITY,
        capability=CAPABILITY_RAG,
        why="an alias can be repointed; only the physical index identifies "
        "what was actually searched",
    ),
)


def pin_rules() -> tuple[PinRule, ...]:
    """The pin rules, in declaration order."""
    return _PIN_RULES


def system_prompt_pin() -> str:
    """SHA-256 of the agent's base system-prompt template.

    Lives here, next to the rule that requires it, so there is exactly one
    definition of "what the prompt pin is".  Two copies would eventually
    disagree, and a manifest whose pin depends on which caller wrote it pins
    nothing.

    Returns ``""`` when the template cannot be read, which makes
    :func:`evaluate_pins` report ``prompt_hash`` missing and the caller refuse
    the manifest.  Failing loudly is correct: a run that cannot say which
    prompt scaffold produced it must not emit an artifact that looks pinned.
    """
    try:
        import hashlib

        from orchestrator.prompts.system import load_base_template

        return hashlib.sha256(load_base_template().encode("utf-8")).hexdigest()
    except Exception:  # noqa: BLE001 - surfaces as a missing pin, not a crash
        return ""


def required_pin_keys(capabilities: Iterable[str] | None = None) -> tuple[str, ...]:
    """Keys that must be non-empty given the run's *capabilities*.

    ``None`` means "nothing was declared": capability pins are waived and
    :data:`STATUS_CAPABILITIES_UNDECLARED` is recorded by :func:`evaluate_pins`.
    See that constant for why this differs from ``trace_contract``.
    """
    enabled = frozenset() if capabilities is None else frozenset(capabilities)
    keys: list[str] = []
    for rule in _PIN_RULES:
        if rule.pin_class == CLASS_REQUIRED:
            keys.append(rule.key)
        elif rule.pin_class == CLASS_CAPABILITY and rule.capability in enabled:
            keys.append(rule.key)
    return tuple(keys)


def evaluate_pins(
    values: dict[str, Any],
    capabilities: Iterable[str] | None = None,
) -> dict[str, Any]:
    """Check *values* against the pin contract.

    Returns a report with ``missing`` (required-and-empty), ``waived`` (gated
    off by capability), ``statuses`` (named degradations such as
    ``MODEL_IDENTITY_UNVERIFIED``), ``contradictions`` (a pin populated while
    its capability was declared off) and ``declared_capabilities``.

    The caller decides whether ``missing`` aborts the run; this function only
    reports.  Keeping the judgement separate is what lets the same contract
    drive both the fail-closed manifest write and a read-only audit.
    """
    undeclared = capabilities is None
    declared = frozenset() if undeclared else frozenset(capabilities)
    unknown = sorted(declared - ALL_CAPABILITIES)

    missing: list[str] = []
    waived: list[str] = []
    statuses: list[str] = []
    contradictions: list[str] = []

    if undeclared:
        statuses.append(STATUS_CAPABILITIES_UNDECLARED)

    for rule in _PIN_RULES:
        value = values.get(rule.key, "")
        present = bool(str(value).strip()) if value is not None else False

        if rule.pin_class == CLASS_REQUIRED:
            if not present:
                missing.append(rule.key)
            continue

        if rule.pin_class == CLASS_CAPABILITY:
            if rule.capability in declared:
                if not present:
                    missing.append(rule.key)
            else:
                waived.append(rule.key)
                # Only an *explicit* "off" can be contradicted.  When nothing
                # was declared there is no claim to disagree with, so a
                # populated pin is just extra information, not a lie.
                if present and not undeclared:
                    contradictions.append(
                        f"{rule.key} was waived because capability "
                        f"{rule.capability!r} was declared off, but the run "
                        "populated it anyway"
                    )
            continue

        # DEGRADABLE
        if not present and rule.empty_status:
            statuses.append(rule.empty_status)

    return {
        "declared_capabilities": sorted(declared),
        "unknown_capabilities": unknown,
        "missing": missing,
        "waived": waived,
        "statuses": statuses,
        "contradictions": contradictions,
    }
