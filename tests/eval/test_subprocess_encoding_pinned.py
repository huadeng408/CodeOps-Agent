"""Every text-mode subprocess call must pin its encoding.

``text=True`` decodes the child's output with the *locale* codec.  On this
project's zh-CN Windows that is gbk, so any byte the gbk table rejects raises
``UnicodeDecodeError`` inside ``subprocess``'s reader thread.  The thread dies,
``communicate()`` returns whatever had been buffered, and the caller sees
truncated or empty output with no exception of its own.

Observed in the H5 run ``eval_results/h5-smoke-20260810-123749``::

    Exception in thread Thread-25 (_readerthread):
    UnicodeDecodeError: 'gbk' codec can't decode byte 0x93 in position 3912

That call was the official scorer invocation, whose stdout becomes the
``scorer_status`` field -- release evidence.  The recorded status was truncated
to ``official: resolved=False (WSL2 official scoring completed: : 0 ...``: the
"Instances resolved" label had been eaten.  An encoding fault had disguised
itself as a scoring result.

This is a defect *class*, not one call site, so it is pinned by AST rather than
by testing individual functions: any new text-mode call is caught here.
"""

from __future__ import annotations

import ast
import sys
from pathlib import Path

import pytest

PROJECT_ROOT = Path(__file__).resolve().parents[2]

# Production modules only.  ``eval/swebench_work/`` holds one-off experiment
# scripts whose output is never release evidence.
SCANNED_FILES = sorted(
    {
        *(PROJECT_ROOT / "eval").glob("*.py"),
        *(PROJECT_ROOT / "eval" / "benchmarks").glob("*.py"),
        *(PROJECT_ROOT / "eval" / "harness").glob("*.py"),
        *(PROJECT_ROOT / "eval" / "contamination").glob("*.py"),
        *(PROJECT_ROOT / "orchestrator" / "eval").glob("*.py"),
    }
)

TEXT_MODE_KWARGS = {"text", "universal_newlines"}
SUBPROCESS_FUNCS = {"run", "check_output", "Popen", "check_call", "call"}


def _is_subprocess_call(node: ast.Call) -> bool:
    func = node.func
    if isinstance(func, ast.Attribute) and func.attr in SUBPROCESS_FUNCS:
        value = func.value
        if isinstance(value, ast.Name) and value.id == "subprocess":
            return True
    return False


def _text_mode_calls_missing_encoding() -> list[str]:
    offenders: list[str] = []
    for path in SCANNED_FILES:
        try:
            tree = ast.parse(path.read_text(encoding="utf-8"))
        except (SyntaxError, UnicodeDecodeError):  # pragma: no cover
            continue
        for node in ast.walk(tree):
            if not isinstance(node, ast.Call) or not _is_subprocess_call(node):
                continue
            kwargs = {kw.arg for kw in node.keywords if kw.arg}
            if not (kwargs & TEXT_MODE_KWARGS):
                continue  # bytes mode cannot raise UnicodeDecodeError
            if "encoding" not in kwargs:
                rel = path.relative_to(PROJECT_ROOT).as_posix()
                offenders.append(f"{rel}:{node.lineno}")
    return offenders


def test_scanner_actually_finds_calls():
    """Guard the guard: an empty scan would make this suite vacuously green."""
    total = 0
    for path in SCANNED_FILES:
        try:
            tree = ast.parse(path.read_text(encoding="utf-8"))
        except (SyntaxError, UnicodeDecodeError):  # pragma: no cover
            continue
        total += sum(
            1 for n in ast.walk(tree) if isinstance(n, ast.Call) and _is_subprocess_call(n)
        )
    assert total >= 5, f"expected to scan real subprocess calls, found {total}"


def test_no_text_mode_subprocess_call_omits_encoding():
    offenders = _text_mode_calls_missing_encoding()
    assert not offenders, (
        "these text-mode subprocess calls decode with the locale codec (gbk on "
        "this host) and will lose output on any non-gbk byte; pass "
        'encoding="utf-8", errors="replace":\n  ' + "\n  ".join(offenders)
    )


def test_scorer_invocation_pins_utf8():
    """The specific call whose output became corrupted release evidence."""
    src = (PROJECT_ROOT / "eval" / "benchmarks" / "swebench.py").read_text(
        encoding="utf-8"
    )
    tree = ast.parse(src)
    found = False
    for node in ast.walk(tree):
        if not isinstance(node, ast.FunctionDef):
            continue
        if node.name != "_run_official_scoring_wsl":
            continue
        for call in ast.walk(node):
            if isinstance(call, ast.Call) and _is_subprocess_call(call):
                kwargs = {kw.arg for kw in call.keywords if kw.arg}
                assert "encoding" in kwargs, (
                    "the official scorer call must pin its encoding: its stdout "
                    "is recorded as scorer_status evidence"
                )
                assert "errors" in kwargs, (
                    "pin errors= too, so an undecodable byte degrades one "
                    "character instead of killing the reader thread"
                )
                found = True
    assert found, "expected a subprocess call inside _run_official_scoring_wsl"


def test_gbk_undecodable_byte_survives_utf8_pinning():
    """Behavioural proof, not just an AST shape assertion.

    0x93 is the byte from the real traceback.  Decoded as gbk in a lone-byte
    context it raises; with the pinned settings the call returns and the rest of
    the output is intact.
    """
    import subprocess

    payload = b"resolved: 1 \x93 done"
    with pytest.raises(UnicodeDecodeError):
        payload.decode("gbk")

    result = subprocess.run(
        [sys.executable, "-c", "import sys; sys.stdout.buffer.write(%r)" % payload],
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        timeout=60,
    )
    assert "resolved: 1" in result.stdout
    assert "done" in result.stdout
