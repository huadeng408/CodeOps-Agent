"""End-to-end: the localization record must reach ``instances.jsonl``.

The stash works by a by-reference argument that is easy to break by accident:
``_augment_for_uplift`` mutates the *original* instance's ``metadata`` dict, and
``dataclasses.replace`` shares that same dict with the augmented copy, so the
runner's own reference observes the mutation. Reorder those two statements, or
deep-copy the metadata anywhere along the way, and the record silently vanishes
from the artifacts while every unit test still passes.

That failure mode is why these tests assert against the written artifact rather
than against a fabricated fixture: a fixture would only confirm the assumption
being tested.
"""

from __future__ import annotations

import json
from dataclasses import replace
from pathlib import Path

import pytest

from eval.benchmarks.swebench import _augment_for_uplift
from eval.adapter import EvalInstance
from eval.harness.uplift import UPLIFT_ENV


TASK = "Separability matrix is wrong for nested CompoundModels"


@pytest.fixture()
def repo(tmp_path: Path) -> Path:
    (tmp_path / "separable.py").write_text(
        '"""Separability matrix computation for compound models."""\n'
        "\n"
        "def separability_matrix(transform):\n"
        '    """Compute the separability matrix."""\n'
        "    return None\n",
        encoding="utf-8",
    )
    return tmp_path


def _instance() -> EvalInstance:
    return EvalInstance(
        instance_id="astropy__astropy-12907",
        task_description=TASK,
        metadata={"repo": "astropy/astropy", "base_commit": "deadbeef"},
    )


# ------------------------------------------------------------ by-reference stash


def test_stash_is_visible_on_the_callers_instance(repo: Path, monkeypatch):
    """The runner records its own reference, not the adapter's augmented copy."""
    monkeypatch.setenv(UPLIFT_ENV, "1")
    original = _instance()
    returned = _augment_for_uplift(original, str(repo))
    assert returned is not original, "expected an augmented copy"
    # The caller's reference must see the record even though the adapter
    # returned a different object.
    assert "localization" in original.metadata


def test_augmented_copy_shares_the_same_metadata_dict(repo: Path, monkeypatch):
    monkeypatch.setenv(UPLIFT_ENV, "1")
    original = _instance()
    returned = _augment_for_uplift(original, str(repo))
    assert returned.metadata is original.metadata


def test_no_stash_when_uplift_is_off(repo: Path, monkeypatch):
    monkeypatch.delenv(UPLIFT_ENV, raising=False)
    original = _instance()
    returned = _augment_for_uplift(original, str(repo))
    assert returned is original
    assert "localization" not in original.metadata


def test_stash_does_not_disturb_existing_metadata(repo: Path, monkeypatch):
    monkeypatch.setenv(UPLIFT_ENV, "1")
    original = _instance()
    _augment_for_uplift(original, str(repo))
    assert original.metadata["repo"] == "astropy/astropy"
    assert original.metadata["base_commit"] == "deadbeef"


# --------------------------------------------------------------- the artifact


def test_record_survives_the_artifact_writer(repo: Path, tmp_path: Path, monkeypatch):
    """Write it the way the runner does and read it back off disk."""
    monkeypatch.setenv(UPLIFT_ENV, "1")
    instance = _instance()
    _augment_for_uplift(instance, str(repo))

    from eval.harness.artifacts import RunArtifacts

    writer = RunArtifacts("run", tmp_path)
    out = writer.root
    writer.record_instance(
        {
            "instance_id": instance.instance_id,
            "task_description": instance.task_description,
            "metadata": instance.metadata,
        }
    )

    lines = (out / "instances.jsonl").read_text(encoding="utf-8").splitlines()
    row = json.loads(lines[0])
    assert row["metadata"]["localization"]["files"], "ranking lost in serialisation"


def test_mechanism_report_can_read_what_the_runner_wrote(
    repo: Path, tmp_path: Path, monkeypatch
):
    """Close the loop: the reader and the writer must agree on the shape."""
    monkeypatch.setenv(UPLIFT_ENV, "1")
    instance = _instance()
    _augment_for_uplift(instance, str(repo))

    from eval.swebench_work import mechanism_report as M

    run = tmp_path / "arm-b-optimized" / "run"
    run.mkdir(parents=True)
    (run / "instances.jsonl").write_text(
        json.dumps(
            {
                "instance_id": instance.instance_id,
                "task_description": instance.task_description,
                "metadata": instance.metadata,
            }
        )
        + "\n",
        encoding="utf-8",
    )
    (run / "predictions.jsonl").write_text(
        json.dumps(
            {
                "instance_id": instance.instance_id,
                "model_patch": (
                    "diff --git a/separable.py b/separable.py\n"
                    "--- a/separable.py\n+++ b/separable.py\n"
                    "@@ -1 +1,2 @@\n def separability_matrix(t):\n+    pass\n"
                ),
            }
        )
        + "\n",
        encoding="utf-8",
    )
    monkeypatch.setattr(M, "EXPERIMENT_DIR", tmp_path)

    arm = M.load_arm("optimized")
    assert arm.localization_available == {instance.instance_id}
    # The patch touches the top-ranked file, so this must read as a hit at 1.
    assert arm.localization_hit_rank == {instance.instance_id: 1}


# --------------------------------------------------------- answer-field hygiene


def test_record_carries_no_answer_fields(repo: Path, monkeypatch):
    """instances.jsonl also holds dataset answer fields; the record must not."""
    monkeypatch.setenv(UPLIFT_ENV, "1")
    instance = _instance()
    instance.metadata["test_patch"] = "diff --git a/tests/test_x.py ..."
    instance.metadata["hints_text"] = "the bug is in _coord_matrix"
    _augment_for_uplift(instance, str(repo))

    blob = json.dumps(instance.metadata["localization"])
    for forbidden in ("test_patch", "hints_text", "_coord_matrix", "tests/test_x.py"):
        assert forbidden not in blob


def test_answer_fields_never_reach_the_prompt(repo: Path, monkeypatch):
    monkeypatch.setenv(UPLIFT_ENV, "1")
    instance = _instance()
    instance.metadata["test_patch"] = "SENTINEL_TEST_PATCH"
    instance.metadata["hints_text"] = "SENTINEL_HINT"
    returned = _augment_for_uplift(instance, str(repo))
    assert "SENTINEL_TEST_PATCH" not in returned.task_description
    assert "SENTINEL_HINT" not in returned.task_description


# ------------------------------------------------------------------ degrading


def test_unreadable_repo_still_records_a_reason(tmp_path: Path, monkeypatch):
    monkeypatch.setenv(UPLIFT_ENV, "1")
    instance = _instance()
    _augment_for_uplift(instance, str(tmp_path / "missing"))
    record = instance.metadata.get("localization")
    assert record and record.get("degraded_reason")


def test_localization_failure_never_fails_the_instance(tmp_path: Path, monkeypatch):
    monkeypatch.setenv(UPLIFT_ENV, "1")
    instance = _instance()
    returned = _augment_for_uplift(instance, str(tmp_path / "missing"))
    assert TASK in returned.task_description


# ---- defect 22: runner passes str(workspace); Path() needed before / operator


def test_prepare_called_with_str_workspace_still_records_localization(
    repo: Path, monkeypatch
):
    """The runner passes str(workspace) to setup_workspace, not a Path.

    _instance_workdir uses the / operator, which requires a Path on the left
    side.  Without the Path(workspace) fix in prepare, _instance_workdir raised
    TypeError, which propagated to the runner's BaseException handler — but
    arm B completed with 8/20 resolved because _setup_workdir had already
    cloned the repo before the crash.  The crash meant localization never ran,
    so instances.jsonl showed only dataset metadata: zero localization records
    in the arm that was supposed to generate them.

    After the fix the record survives end-to-end.
    """
    monkeypatch.setenv(UPLIFT_ENV, "1")
    from eval.benchmarks.swebench import _instance_workdir

    instance = _instance()
    # Simulate the runner: str(workspace), not Path.
    workdir = _instance_workdir(instance, Path(str(repo)))
    _augment_for_uplift(instance, str(workdir))

    assert "localization" in instance.metadata, (
        "localization record missing — Path(workspace) fix may have been reverted"
    )
    assert instance.metadata["localization"].get("files"), (
        "localization ran but found nothing in the test repo"
    )
