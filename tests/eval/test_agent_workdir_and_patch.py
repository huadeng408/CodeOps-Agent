"""The agent's working directory must BE the repo, and the answer must be text.

Two defects found in the first H5 run whose official scorer actually returned
a verdict (``eval_results/h5-smoke-20260810-122909``):

**F — empty patch by construction.**  ``SWEBenchAdapter.prepare`` cloned the
repo to ``workspace/<instance_id>``, while the harness hands the driver the
workspace *root* as ``working_dir``.  The driver then ran ``git diff HEAD``
with ``cwd=<workspace root>``, where no git repository exists, so
``model_patch`` was always empty.  The agent had genuinely located the bug
(69,413 input tokens, and it named ``_cstack`` line 246), and the official
scorer still reported "empty patches: 1".  A benchmark that cannot express a
result is worse than one that fails loudly.

**E — the recorded answer was shredded.**  ``"\\n".join(final_text_parts)``
joined *streaming token deltas* with newlines, producing ``"Now\\nI\\ncan\\nsee"``
instead of ``"Now I can see"``.  The prediction artifact is the evidence, so a
corrupted answer field corrupts the record.
"""

from __future__ import annotations

import subprocess
from pathlib import Path

from eval.adapter import EvalInstance


def _synthetic_instance() -> EvalInstance:
    return EvalInstance(
        instance_id="synthetic__django-1234",
        task_description="fix it",
        metadata={"synthetic": True, "repo": "django/django", "base_commit": "abc"},
    )


def _is_git_root(path: Path) -> bool:
    return (path / ".git").is_dir()


# ---------------------------------------------------------------------------
# F: prepare() must make the workspace itself the repo root
# ---------------------------------------------------------------------------


def test_prepare_makes_the_workspace_the_repo_root(tmp_path: Path) -> None:
    """The harness's per-instance workspace must be the git root itself.

    The harness gives every instance a fresh temp dir, so nesting a second
    directory inside it serves no purpose and breaks the diff capture.
    """
    from eval.benchmarks.swebench import SWEBenchAdapter

    adapter = SWEBenchAdapter()
    adapter.prepare(_synthetic_instance(), tmp_path)

    assert _is_git_root(tmp_path), (
        "workspace root must contain .git so that the working_dir the harness "
        "passes to the agent is the repository"
    )
    nested = tmp_path / "synthetic__django-1234"
    assert not _is_git_root(nested), (
        "the repo must not be nested one level below the working_dir"
    )


def test_git_diff_in_working_dir_sees_agent_edits(tmp_path: Path) -> None:
    """An edit made in working_dir must show up in `git diff HEAD` there."""
    from eval.benchmarks.swebench import SWEBenchAdapter, _capture_git_diff

    adapter = SWEBenchAdapter()
    adapter.prepare(_synthetic_instance(), tmp_path)

    target = next(
        (p for p in tmp_path.rglob("*.py") if ".git" not in p.parts), None
    )
    assert target is not None, "synthetic repo produced no python file to edit"
    target.write_text(
        target.read_text(encoding="utf-8") + "\n# agent edit\n", encoding="utf-8"
    )

    diff = _capture_git_diff(str(tmp_path))
    assert diff.strip(), "an edit in working_dir must produce a non-empty diff"
    assert "# agent edit" in diff


def test_legacy_nested_layout_still_supported(tmp_path: Path) -> None:
    """The legacy CLI shares one base dir across instances and must still nest."""
    from eval.benchmarks.swebench import _setup_workdir

    workdir = _setup_workdir(_synthetic_instance(), str(tmp_path), dry_run=True)
    assert Path(workdir) == tmp_path / "synthetic__django-1234"
    assert _is_git_root(Path(workdir))


def test_instance_workdir_resolves_both_layouts(tmp_path: Path) -> None:
    """_instance_workdir must return the repo for nested and flat layouts."""
    from eval.benchmarks.swebench import _instance_workdir

    instance = _synthetic_instance()

    flat = tmp_path / "flat"
    flat.mkdir()
    assert _instance_workdir(instance, flat) == flat

    nested_base = tmp_path / "nested"
    (nested_base / instance.instance_id).mkdir(parents=True)
    assert _instance_workdir(instance, nested_base) == nested_base / instance.instance_id


# ---------------------------------------------------------------------------
# E: streamed token deltas must be concatenated, not newline-joined
# ---------------------------------------------------------------------------


def test_streamed_text_is_concatenated_not_newline_joined() -> None:
    """Token deltas are fragments of a sentence, not lines."""
    import inspect

    from eval.driver_headless import HeadlessDriver

    source = inspect.getsource(HeadlessDriver._solve_with_runner)
    assert '"\\n".join(final_text_parts)' not in source, (
        'streamed token deltas must not be joined with newlines: that turns '
        '"Now I can see" into "Now\\nI\\ncan\\nsee"'
    )
    assert '"".join(final_text_parts)' in source


def test_answer_reassembles_a_readable_sentence(monkeypatch) -> None:
    """End-to-end on the join itself: fragments must rebuild the sentence."""
    parts = ["Now", " I", " can", " see", " the", " bug", "."]
    assert "".join(parts) == "Now I can see the bug."
    assert "\n".join(parts) != "Now I can see the bug."
