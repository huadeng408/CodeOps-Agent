"""Runner-owned execution evidence for official benchmark receipts.

Receipt collection must not trust process metadata supplied by a caller.  A
runner issues one execution session, observes the child process itself, and
binds the resulting output bytes to that invocation before a receipt can be
written.
"""

from __future__ import annotations

import hashlib
from collections.abc import Callable, Mapping, Sequence
from dataclasses import dataclass
from pathlib import Path
from types import MappingProxyType
from typing import Any


def _resolved(path: Path) -> Path:
    return path.resolve(strict=False)


def _within(path: Path, root: Path) -> bool:
    try:
        path.relative_to(root)
    except ValueError:
        return False
    return True


def _assert_safe_output(path: Path, root: Path) -> Path:
    resolved_root = _resolved(root)
    resolved_path = _resolved(path)
    if not _within(resolved_path, resolved_root):
        raise ValueError("official output path escapes the runner output root")
    if path.is_symlink():
        raise ValueError("official output path must not be a symlink")
    return resolved_path


@dataclass(frozen=True)
class OfficialExecutionEvidence:
    """Immutable evidence exposed internally to a receipt collector."""

    output_path: Path
    output_root: Path
    returncode: int
    output_sha256: str | None
    required_output_sha256: Mapping[str, str]
    optional_output_sha256: Mapping[str, str]
    output_bytes: bytes | None
    required_output_bytes: tuple[tuple[str, bytes], ...]
    optional_output_bytes: tuple[tuple[str, bytes], ...]
    missing_required_outputs: tuple[str, ...]

    def _relative_name(self, path: Path) -> str:
        try:
            return path.absolute().relative_to(self.output_root).as_posix()
        except ValueError as exc:
            raise ValueError("official output path is outside the execution root") from exc

    def bytes_for(self, path: Path) -> bytes:
        """Return bytes captured for a required or observed optional output."""
        relative_name = self._relative_name(path)
        for name, payload in (*self.required_output_bytes, *self.optional_output_bytes):
            if name == relative_name:
                return payload
        raise FileNotFoundError("official output was not captured by this execution")

    def optional_bytes_for(self, path: Path) -> bytes | None:
        """Return an optional output snapshot, or ``None`` if none was produced."""
        relative_name = self._relative_name(path)
        for name, payload in self.optional_output_bytes:
            if name == relative_name:
                return payload
        return None


class OfficialRunExecution:
    """One runner-issued, single-use child-process execution.

    The owner and token are intentionally private to the issuing runner.  A
    caller can invoke the session, but cannot turn arbitrary return-code and
    stale-file claims into receipt evidence without going through this
    observed process boundary.
    """

    def __init__(
        self,
        *,
        owner: object,
        token: object,
        output_path: Path,
        output_root: Path,
        process_runner: Callable[..., Any],
        required_output_paths: Sequence[Path] = (),
        optional_output_paths: Sequence[Path] = (),
        default_command: Sequence[str] | None = None,
        default_cwd: Path | None = None,
    ) -> None:
        self._owner = owner
        self._token = token
        # Keep the lexical path so a final-file symlink remains observable;
        # containment checks resolve it immediately before and after execution.
        self._output_path = output_path.absolute()
        self._output_root = output_root.absolute()
        self._required_output_paths = tuple(
            path.absolute() for path in (output_path, *required_output_paths)
        )
        self._optional_output_paths = tuple(path.absolute() for path in optional_output_paths)
        all_output_paths = (*self._required_output_paths, *self._optional_output_paths)
        if len(set(all_output_paths)) != len(all_output_paths):
            raise ValueError("official execution output paths must be unique")
        self._default_command = (
            tuple(default_command) if default_command is not None else None
        )
        self._default_cwd = default_cwd
        self._process_runner = process_runner
        self._returncode: int | None = None
        self._output_sha256: str | None = None
        self._required_output_sha256: dict[str, str] | None = None
        self._optional_output_sha256: dict[str, str] | None = None
        self._required_output_bytes: dict[str, bytes] | None = None
        self._optional_output_bytes: dict[str, bytes] | None = None
        self._missing_required_outputs: tuple[str, ...] | None = None
        self._completed = False
        self._collected = False

    @property
    def output_path(self) -> Path:
        return self._output_path

    def run(
        self,
        command: Sequence[str] | None = None,
        *,
        cwd: Path | None = None,
        env: Mapping[str, str] | None = None,
    ) -> Any:
        """Run the child process and bind a fresh output to its result."""
        if self._completed:
            raise RuntimeError("official execution session can only run once")

        for path in (*self._required_output_paths, *self._optional_output_paths):
            _assert_safe_output(path, self._output_root)
            if path.exists():
                raise RuntimeError("official output already existed before this run")

        if command is None:
            command = self._default_command
        elif self._default_command is not None and tuple(command) != self._default_command:
            raise ValueError("official execution command does not match runner pin")
        if not command:
            raise ValueError("official execution command is required")
        normalized_command = tuple(command)
        if any(not isinstance(argument, str) or not argument for argument in normalized_command):
            raise ValueError("official execution command must contain non-empty strings")

        effective_cwd = self._default_cwd if cwd is None else cwd
        completed = self._process_runner(
            list(normalized_command),
            cwd=effective_cwd,
            env=env,
            check=False,
        )
        returncode = getattr(completed, "returncode", None)
        if isinstance(returncode, bool) or not isinstance(returncode, int):
            raise TypeError("official process did not return an integer exit code")

        output_hashes: dict[str, str] = {}
        required_output_bytes: dict[str, bytes] = {}
        missing_required_outputs: list[str] = []
        for path in self._required_output_paths:
            _assert_safe_output(path, self._output_root)
            relative_name = path.relative_to(self._output_root).as_posix()
            if not path.is_file():
                missing_required_outputs.append(relative_name)
                continue
            payload = path.read_bytes()
            required_output_bytes[relative_name] = payload
            output_hashes[relative_name] = hashlib.sha256(payload).hexdigest()
        optional_output_hashes: dict[str, str] = {}
        optional_output_bytes: dict[str, bytes] = {}
        for path in self._optional_output_paths:
            _assert_safe_output(path, self._output_root)
            if not path.exists():
                continue
            if not path.is_file():
                raise RuntimeError("official process produced an invalid optional output")
            payload = path.read_bytes()
            relative_name = path.relative_to(self._output_root).as_posix()
            optional_output_bytes[relative_name] = payload
            optional_output_hashes[relative_name] = hashlib.sha256(payload).hexdigest()
        self._returncode = returncode
        self._required_output_sha256 = output_hashes
        self._optional_output_sha256 = optional_output_hashes
        self._required_output_bytes = required_output_bytes
        self._optional_output_bytes = optional_output_bytes
        output_name = self._output_path.relative_to(self._output_root).as_posix()
        self._output_sha256 = output_hashes.get(output_name)
        self._missing_required_outputs = tuple(missing_required_outputs)
        self._completed = True
        return completed

    def evidence(self, *, owner: object, token: object) -> OfficialExecutionEvidence:
        """Return bound evidence only to the runner that issued this session."""
        if owner is not self._owner or token is not self._token:
            raise ValueError("receipt requires a runner-issued execution session")
        if (
            not self._completed
            or self._returncode is None
            or self._required_output_sha256 is None
            or self._optional_output_sha256 is None
            or self._required_output_bytes is None
            or self._optional_output_bytes is None
            or self._missing_required_outputs is None
        ):
            raise RuntimeError("official execution has not completed")
        if self._collected:
            raise RuntimeError("official execution evidence was already collected")
        # The runner freezes bytes immediately after the child exits. Receipt
        # collection must consume this snapshot rather than rereading paths
        # that another process could replace after execution.
        required_output_bytes = tuple(self._required_output_bytes.items())
        optional_output_bytes = tuple(self._optional_output_bytes.items())
        output_name = self._output_path.relative_to(self._output_root).as_posix()
        output_bytes = dict(required_output_bytes).get(output_name)
        self._collected = True
        return OfficialExecutionEvidence(
            output_path=self._output_path,
            output_root=self._output_root,
            returncode=self._returncode,
            output_sha256=self._output_sha256,
            required_output_sha256=MappingProxyType(dict(self._required_output_sha256)),
            optional_output_sha256=MappingProxyType(dict(self._optional_output_sha256)),
            output_bytes=output_bytes,
            required_output_bytes=tuple(required_output_bytes),
            optional_output_bytes=tuple(optional_output_bytes),
            missing_required_outputs=self._missing_required_outputs,
        )
