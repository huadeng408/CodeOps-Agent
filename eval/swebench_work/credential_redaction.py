"""Stream child-process output without exposing provider credentials."""

from __future__ import annotations

import os
import subprocess
from collections.abc import Iterable, Mapping, Sequence
from pathlib import Path

from eval.harness.redaction import redact_credential_text


def run_redacted_command(
    command: Sequence[str | os.PathLike[str]],
    *,
    cwd: str | os.PathLike[str] | Path,
    env: Mapping[str, str],
    secrets: Iterable[str],
) -> int:
    """Run a command, merge both output streams, and return its exit code."""
    secret_values = tuple(secrets)
    process = subprocess.Popen(
        [os.fspath(argument) for argument in command],
        cwd=os.fspath(cwd),
        env=dict(env),
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        encoding="utf-8",
        errors="replace",
        bufsize=1,
        shell=False,
    )
    assert process.stdout is not None
    with process.stdout:
        for line in process.stdout:
            print(redact_credential_text(line, secret_values), end="", flush=True)
    return process.wait()
