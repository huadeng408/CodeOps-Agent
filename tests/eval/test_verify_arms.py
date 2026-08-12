from __future__ import annotations

import sys
from pathlib import Path
from types import MappingProxyType
from typing import Any

import pytest

from eval.swebench_work import verify_arms as V
from eval.swebench_work.report_gate import FinalizedRun


SOURCE_IDS = tuple(f"repo__case-{index}" for index in range(20))
MANDATE = "## How this task is graded\n\nThe grader reads `git diff`."
RANKING = {"files": ["pkg/separable.py"], "file_scores": {"pkg/separable.py": 4.2}}


def _instance(
    instance_id: str,
    *,
    localization: Any | None = None,
    mandate: bool = False,
    metadata: dict[str, Any] | None = None,
) -> dict[str, Any]:
    meta = dict(metadata or {})
    if localization is not None:
        meta["localization"] = localization
    description = "Fix the defect"
    if mandate:
        description += f"\n\n{MANDATE}"
    return {
        "instance_id": instance_id,
        "task_description": description,
        "metadata": meta,
    }


def _run(
    arm: str,
    *,
    instances: list[dict[str, Any]] | None = None,
    enabled: bool | None = None,
    components: list[str] | None = None,
    tool_rounds: int | None = None,
) -> FinalizedRun:
    if instances is None:
        instances = [
            _instance(
                source_id,
                localization=RANKING if arm == "optimized" else None,
                mandate=arm == "optimized",
            )
            for source_id in SOURCE_IDS
        ]
    if enabled is None:
        enabled = arm == "optimized"
    if components is None:
        components = (
            ["localization", "tool_rounds", "edit_mandate", "validation"]
            if arm == "optimized"
            else []
        )
    if tool_rounds is None:
        tool_rounds = 24 if arm == "optimized" else 8
    manifest = {
        "run_id": f"{arm}-run",
        "harness_uplift": {
            "enabled": enabled,
            "components": components,
            "tool_rounds": tool_rounds,
        },
    }
    empty = MappingProxyType({})
    return FinalizedRun(
        arm=arm,
        run_id=f"{arm}-run",
        run_dir=Path(arm),
        manifest=manifest,
        summary=empty,
        manifest_sha256="m",
        checksums_sha256="c",
        cohort_path=Path("cohort.json"),
        cohort_sha256="h",
        source_ids=SOURCE_IDS,
        eligible_ids=frozenset(SOURCE_IDS),
        instances=tuple(instances),
        predictions=(),
        events=(),
        failures=(),
        trace_summary=empty,
        span_assertion=empty,
        official_verdicts=empty,
        outcomes=empty,
        unmeasured_by_reason=empty,
        failures_by_reason=empty,
        excluded_contaminated=empty,
        scorer_evidence_scope="last-invocation-only",
    )


def test_complete_frozen_identity_passes() -> None:
    audits = {arm: V.audit_run(_run(arm)) for arm in V.ARMS}
    assert audits["baseline"]["ok"]
    assert audits["optimized"]["ok"]
    assert audits["optimized"]["with_localization"] == 20
    assert audits["optimized"]["with_mandate"] == 20
    assert "VERDICT: VERIFIED" in V.render(audits)


@pytest.mark.parametrize(
    ("arm", "enabled", "components", "rounds"),
    [
        ("baseline", True, [], 8),
        ("baseline", False, ["localization"], 8),
        ("baseline", False, [], 24),
        ("optimized", False, ["localization", "tool_rounds", "edit_mandate", "validation"], 24),
        ("optimized", True, ["localization", "edit_mandate"], 24),
        ("optimized", True, ["localization", "tool_rounds", "edit_mandate", "validation"], 8),
    ],
)
def test_manifest_identity_must_match_exactly(
    arm: str,
    enabled: bool,
    components: list[str],
    rounds: int,
) -> None:
    audit = V.audit_run(
        _run(arm, enabled=enabled, components=components, tool_rounds=rounds)
    )
    assert not audit["ok"]
    assert audit["mismatches"]


def test_optimized_requires_all_twenty_source_ids() -> None:
    audit = V.audit_run(_run("optimized", instances=_run("optimized").instances[:-1]))
    assert not audit["ok"]
    assert audit["missing_instance_ids"] == [SOURCE_IDS[-1]]
    assert SOURCE_IDS[-1] in audit["missing_localization_ids"]
    assert SOURCE_IDS[-1] in audit["missing_mandate_ids"]


def test_optimized_requires_localization_on_every_retry_row() -> None:
    instances = list(_run("optimized").instances)
    instances.append(_instance(SOURCE_IDS[0], mandate=True))
    audit = V.audit_run(_run("optimized", instances=instances))
    assert not audit["ok"]
    assert audit["missing_localization_rows"] == [f"{SOURCE_IDS[0]}#2"]


def test_optimized_requires_mandate_on_every_retry_row() -> None:
    instances = list(_run("optimized").instances)
    instances.append(_instance(SOURCE_IDS[0], localization=RANKING))
    audit = V.audit_run(_run("optimized", instances=instances))
    assert not audit["ok"]
    assert audit["missing_mandate_rows"] == [f"{SOURCE_IDS[0]}#2"]


def test_baseline_missing_pre_record_row_still_proves_no_uplift() -> None:
    audit = V.audit_run(_run("baseline", instances=list(_run("baseline").instances[1:])))
    assert audit["ok"]
    assert audit["missing_instance_ids"] == [SOURCE_IDS[0]]


def test_baseline_rejects_any_uplift_evidence() -> None:
    instances = list(_run("baseline").instances)
    instances[0] = _instance(SOURCE_IDS[0], localization=RANKING, mandate=True)
    audit = V.audit_run(_run("baseline", instances=instances))
    assert not audit["ok"]
    assert audit["unexpected_localization_rows"] == [f"{SOURCE_IDS[0]}#1"]
    assert audit["unexpected_mandate_rows"] == [f"{SOURCE_IDS[0]}#1"]


def test_answer_field_key_inside_localization_is_rejected() -> None:
    instances = list(_run("optimized").instances)
    localization = dict(RANKING)
    localization["test_patch"] = "diff --git a/tests/test_x.py b/tests/test_x.py"
    instances[0] = _instance(SOURCE_IDS[0], localization=localization, mandate=True)
    audit = V.audit_run(_run("optimized", instances=instances))
    assert audit["answer_field_leaks"] == [f"{SOURCE_IDS[0]}#1: test_patch"]
    assert not audit["ok"]


def test_answer_value_copied_under_benign_key_is_rejected() -> None:
    gold = "diff --git a/pkg/x.py b/pkg/x.py\n" + "x" * 80
    localization = dict(RANKING)
    localization["notes"] = gold
    instances = list(_run("optimized").instances)
    instances[0] = _instance(
        SOURCE_IDS[0],
        localization=localization,
        mandate=True,
        metadata={"test_patch": gold},
    )
    audit = V.audit_run(_run("optimized", instances=instances))
    assert audit["answer_field_leaks"] == [f"{SOURCE_IDS[0]}#1: test_patch"]


def test_answer_fields_in_metadata_alone_are_not_a_leak() -> None:
    instances = list(_run("optimized").instances)
    instances[0] = _instance(
        SOURCE_IDS[0],
        localization={"files": ["astropy/utils/patches.py"]},
        mandate=True,
        metadata={"test_patch": "diff --git a/tests/test_sampled.py b/tests/test_sampled.py"},
    )
    audit = V.audit_run(_run("optimized", instances=instances))
    assert audit["ok"]
    assert audit["answer_field_leaks"] == []


def test_gold_patch_marker_in_model_task_description_is_rejected() -> None:
    instances = list(_run("optimized").instances)
    instances[0] = _instance(
        SOURCE_IDS[0], localization=RANKING, mandate=True
    )
    instances[0]["task_description"] += (
        "\n\nGOLD PATCH: diff --git a/pkg/x.py b/pkg/x.py\n+secret fix"
    )

    audit = V.audit_run(_run("optimized", instances=instances))

    assert audit["answer_field_leaks"] == [
        f"{SOURCE_IDS[0]}#1: task_description: gold_patch"
    ]
    assert not audit["ok"]


def test_answer_value_with_normalized_line_endings_in_task_description_is_rejected() -> None:
    gold = "diff --git a/pkg/x.py b/pkg/x.py\n@@ -1 +1 @@\n" + "x" * 80
    instances = list(_run("optimized").instances)
    instances[0] = _instance(
        SOURCE_IDS[0],
        localization=RANKING,
        mandate=True,
        metadata={"test_patch": gold},
    )
    instances[0]["task_description"] += "\n\n" + gold.replace("\n", "\r\n")

    audit = V.audit_run(_run("optimized", instances=instances))

    assert audit["answer_field_leaks"] == [
        f"{SOURCE_IDS[0]}#1: task_description: test_patch"
    ]
    assert not audit["ok"]


def test_blocked_cli_does_not_touch_receipt_or_json(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
) -> None:
    receipt = tmp_path / "verified-arms.json"
    audit_json = tmp_path / "audit.json"
    receipt.write_bytes(b"keep-receipt")
    audit_json.write_bytes(b"keep-audit")
    monkeypatch.setattr(
        V,
        "load_finalized_run",
        lambda arm, run_id: (_ for _ in ()).throw(V.ReportGateError("invalid run")),
    )
    monkeypatch.setattr(
        sys,
        "argv",
        [
            "verify_arms.py",
            "--verified-arms",
            str(receipt),
            "--json",
            str(audit_json),
        ],
    )

    assert V.main() == 2
    assert capsys.readouterr().out == "BLOCKED: invalid run\n"
    assert receipt.read_bytes() == b"keep-receipt"
    assert audit_json.read_bytes() == b"keep-audit"
