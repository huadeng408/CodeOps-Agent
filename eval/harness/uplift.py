"""The optimized arm's configuration, in one place.

Why this module exists
----------------------
The paired experiment needs exactly one switch between arms, and it needs the
switch to be readable. Scattering ``if os.environ.get(...)`` through the
benchmark and the driver would make "what did arm B actually change?" a question
answered by grepping, which is the kind of question an interviewer asks and a
reviewer cannot verify.

What the baseline measurement showed
-----------------------------------
Arm A on the astropy-20 subset produced almost no patches, and the traces say
why. Across 11 scored instances the agent issued 81 search calls (Grep 42,
Read 30, Glob 9) and **2** edits; 9 of 11 instances made no edit at all. Nearly
every instance reached ``chats=9`` against a ``max_tool_rounds=8`` ceiling — the
turn budget was spent finding the bug, and the model, out of turns, printed the
fix as chat text. For ``astropy__astropy-13236`` that text was a *correct* fix
for the reported issue; ``git diff`` was still empty, so it scored as a failure.

So the baseline was not mainly losing on reasoning. It was losing on where its
turns went, and on the difference between describing a fix and applying one.
Each knob below targets one of those two losses:

``localization``
    Spend cheap local compute (BM25 over the repo's own identifiers) to hand the
    agent ranked candidate files and functions, so its scarce turns start at the
    edit instead of at the search. This is the Agentless-style
    localize-then-repair split (https://github.com/openautocoder/agentless).

``tool_rounds``
    8 rounds is below the floor for "search, read, edit, verify" on a repository
    the size of astropy. Raised, but deliberately not to infinity: an unbounded
    budget converts a stuck agent into a stuck *expensive* agent, and the point
    of the paired design is to attribute the gain, not to buy it.

``edit_mandate``
    State the deliverable in the terms the scorer uses. The scorer reads
    ``git diff``; a model that answers in a code block has produced nothing
    measurable. This is prompt text, not machinery, and it is listed here so the
    write-up cannot quietly credit the machinery for its effect.

Every knob is off unless :envvar:`SWEBENCH_HARNESS_UPLIFT` is set, so arm A runs
byte-identical code to the version that produced the baseline.
"""

from __future__ import annotations

import os
from dataclasses import dataclass, field
from typing import Any

#: The single environment switch between the two arms.
UPLIFT_ENV = "SWEBENCH_HARNESS_UPLIFT"

#: Turn budget for each arm. The baseline's value is recorded here rather than
#: left implicit in the driver, so the diff between arms is visible in one file.
BASELINE_TOOL_ROUNDS = 8
UPLIFT_TOOL_ROUNDS = 24

#: How many candidates localization passes to the agent. Small on purpose: a
#: long list is a way of saying "I don't know", and it costs prompt tokens that
#: the agent needs for reading the code it was pointed at.
UPLIFT_TOP_FILES = 6
UPLIFT_TOP_FUNCTIONS = 10

EDIT_MANDATE = """\
## How this task is graded

Your work is graded by running the repository's tests against the changes you
leave **on disk**. The grader reads `git diff` in the working directory; it does
not read your replies. Concretely:

- Apply the fix with the file-editing tools. A fix shown in your reply but not
  written to a file scores zero, no matter how correct it is.
- Edit the source file that contains the defect. Do not create a new file
  alongside it, and do not modify the tests.
- Make the smallest change that fixes the reported behaviour.
- When you believe you are done, confirm with `git diff` that your change is
  actually present, and if it is empty, apply it before finishing.
"""


@dataclass(frozen=True)
class UpliftConfig:
    """Which optimizations are active for this run."""

    enabled: bool = False
    localization: bool = False
    tool_rounds: int = BASELINE_TOOL_ROUNDS
    edit_mandate: bool = False
    validation: bool = False
    selection: bool = False
    #: Names of the active components, for the manifest. An artifact that records
    #: which arm produced it is the difference between a comparison and a claim.
    components: tuple[str, ...] = field(default=())

    def as_manifest_block(self) -> dict[str, Any]:
        return {
            "enabled": self.enabled,
            "components": list(self.components),
            "tool_rounds": self.tool_rounds,
            "env_var": UPLIFT_ENV,
        }


def _flag(name: str, default: bool) -> bool:
    """Read a per-component override, defaulting to the arm's setting.

    Per-component overrides exist so a follow-up ablation can turn one knob off
    without editing code, which is how the write-up can attribute the gain to
    parts rather than to the bundle.
    """
    raw = os.environ.get(name)
    if raw is None:
        return default
    return raw.strip().lower() not in ("", "0", "false", "no", "off")


def uplift_config() -> UpliftConfig:
    """Return the active configuration, read fresh from the environment.

    Read fresh rather than cached at import: the tests set the variable per case,
    and a cached value would make them pass or fail depending on import order.
    """
    enabled = _flag(UPLIFT_ENV, False)
    if not enabled:
        return UpliftConfig()

    localization = _flag("SWEBENCH_UPLIFT_LOCALIZATION", True)
    edit_mandate = _flag("SWEBENCH_UPLIFT_EDIT_MANDATE", True)
    validation = _flag("SWEBENCH_UPLIFT_VALIDATION", False)
    selection = _flag("SWEBENCH_UPLIFT_SELECTION", False)
    try:
        rounds = int(os.environ.get("SWEBENCH_UPLIFT_TOOL_ROUNDS", UPLIFT_TOOL_ROUNDS))
    except ValueError:
        rounds = UPLIFT_TOOL_ROUNDS
    rounds = max(BASELINE_TOOL_ROUNDS, min(rounds, 64))

    components = tuple(
        name
        for name, on in (
            ("localization", localization),
            ("tool_rounds", rounds != BASELINE_TOOL_ROUNDS),
            ("edit_mandate", edit_mandate),
            ("validation", validation),
            ("selection", selection),
        )
        if on
    )
    return UpliftConfig(
        enabled=True,
        localization=localization,
        tool_rounds=rounds,
        edit_mandate=edit_mandate,
        validation=validation,
        selection=selection,
        components=components,
    )


def augment_task_description(
    task_description: str,
    repo_root: str,
    *,
    config: UpliftConfig | None = None,
) -> str:
    """Prepend localization candidates and the grading contract to the task.

    Returns *task_description* unchanged when the uplift is off, so arm A's
    prompt is not merely equivalent but identical.

    Localization failure is never fatal: if the repository cannot be analysed the
    agent gets the same task it would have got anyway. An accelerator that can
    fail the run is a liability, and a localizer is only an accelerator.
    """
    cfg = config or uplift_config()
    if not cfg.enabled:
        return task_description

    blocks: list[str] = []
    if cfg.localization:
        try:
            from eval.harness.localize import localize, render_localization_block

            result = localize(
                task_description,
                repo_root,
                top_files=UPLIFT_TOP_FILES,
                top_functions=UPLIFT_TOP_FUNCTIONS,
            )
            block = render_localization_block(result)
            if block:
                blocks.append(block)
        except Exception as exc:  # pragma: no cover - defensive by intent
            print(f"[uplift] localization skipped: {type(exc).__name__}: {exc}")
    if cfg.edit_mandate:
        blocks.append(EDIT_MANDATE)
    if not blocks:
        return task_description
    return "\n\n".join([task_description, *blocks])
