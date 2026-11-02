"""Conservative reuse of closed, read-only gofmt checks and original receipts."""

from __future__ import annotations

import copy
import ctypes
from datetime import datetime
import hashlib
import json
import math
import os
from pathlib import Path
import platform
import re
import shutil
import stat
import sys
import time
from typing import Any, Mapping

from eval.harness.artifacts import RunArtifacts

if os.name == "nt":
    from ctypes import wintypes

_POLICY = "readonly_gofmt_files_v1"
_DIGEST = re.compile(r"[0-9a-f]{64}\Z")
_MAX_IDENTITY_BYTES = 64 * 1024 * 1024


def _hash(value: Any) -> str:
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(",", ":")).encode()).hexdigest()


def _regular_path(path: Path) -> Path:
    absolute = path.absolute()
    for component in (absolute, *absolute.parents):
        metadata = component.lstat()
        if stat.S_ISLNK(metadata.st_mode) or getattr(metadata, "st_file_attributes", 0) & 0x400:
            raise ValueError("symbolic or reparse paths have no audited closure")
    if not stat.S_ISREG(absolute.stat().st_mode):
        raise ValueError("a regular file is required")
    return absolute


def _permission_identity(path: Path, metadata: os.stat_result) -> dict[str, Any]:
    identity: dict[str, Any] = {"mode": stat.S_IMODE(metadata.st_mode), "uid": metadata.st_uid, "gid": metadata.st_gid}
    if os.name == "nt":
        security = ctypes.WinDLL("advapi32", use_last_error=True).GetFileSecurityW
        security.argtypes = [wintypes.LPCWSTR, wintypes.DWORD, ctypes.c_void_p, wintypes.DWORD, ctypes.POINTER(wintypes.DWORD)]
        security.restype = wintypes.BOOL
        length = wintypes.DWORD()
        security(str(path), 7, None, 0, ctypes.byref(length))
        if not 0 < length.value <= 1024 * 1024:
            raise ValueError("file security descriptor unavailable")
        descriptor = ctypes.create_string_buffer(length.value)
        if not security(str(path), 7, descriptor, length.value, ctypes.byref(length)):
            raise ctypes.WinError(ctypes.get_last_error())
        identity["owner_group_dacl_sha256"] = hashlib.sha256(descriptor.raw[:length.value]).hexdigest()
    elif hasattr(os, "listxattr"):
        identity["extended_permissions"] = {name: hashlib.sha256(getattr(os, "getxattr")(path, name)).hexdigest() for name in sorted(os.listxattr(path)) if name.startswith(("system.posix_acl", "security."))}
    return identity


def _file_identity(path: Path, budget: list[int]) -> dict[str, Any]:
    path = _regular_path(path)
    before = path.stat()
    if before.st_size > budget[0]:
        raise ValueError("bounded dependency identity budget exceeded")
    data = path.read_bytes()
    after = path.stat()
    attributes = ("st_dev", "st_ino", "st_mode", "st_size", "st_mtime_ns", "st_ctime_ns")
    if any(getattr(before, key) != getattr(after, key) for key in attributes):
        raise ValueError("file changed while identifying inputs")
    budget[0] -= len(data)
    return {"path_sha256": _hash(str(path)), "sha256": hashlib.sha256(data).hexdigest(), "size": len(data), "permissions": _permission_identity(path, after)}


def _gofmt_version(data: bytes) -> dict[str, str]:
    # Go 1.18+ embeds two inline, varint-prefixed strings after this header.
    offset = data.find(b"\xff Go buildinf:")
    if offset < 0 or offset % 16 or offset + 32 >= len(data) or not data[offset + 15] & 2:
        raise ValueError("supported Go build information unavailable")
    cursor = offset + 32
    strings = []
    for _ in range(2):
        length = 0
        for shift in range(0, 70, 7):
            value = data[cursor]
            cursor += 1
            length |= (value & 127) << shift
            if value < 128:
                break
        else:
            raise ValueError("invalid Go build information")
        if length > 1024 * 1024 or cursor + length > len(data):
            raise ValueError("invalid Go build string length")
        strings.append(data[cursor:cursor + length])
        cursor += length
    version = strings[0].decode("ascii")
    framed = strings[1]
    if len(framed) < 33 or framed[-17] != 10:
        raise ValueError("Go module information unavailable")
    build = framed[16:-16].decode("utf-8")
    if not version.startswith("go1.") or "path\tcmd/gofmt\n" not in build or "build\tCGO_ENABLED=0\n" not in build:
        raise ValueError("only the standalone Go gofmt tool has an audited closure")
    return {"go_version": version, "package": "cmd/gofmt", "build_information_sha256": _hash(build)}


def _runtime_identity(budget: list[int]) -> dict[str, Any]:
    files: dict[str, dict[str, Any]] = {}
    aliases: dict[str, str] = {}
    for name, module in sorted(tuple(sys.modules.items())):
        origin = getattr(getattr(module, "__spec__", None), "origin", None)
        raw = getattr(module, "__file__", None)
        if not raw or origin in {"built-in", "frozen"}:
            continue
        path = Path(raw).resolve(strict=True)
        key = _hash(str(path))
        aliases[name] = key
        if key not in files:
            if len(files) >= 512:
                raise ValueError("loaded dependency count exceeds the audited limit")
            files[key] = _file_identity(path, budget)
        cached = getattr(module, "__cached__", None)
        if cached and Path(cached).is_file():
            bytecode = Path(cached).resolve(strict=True)
            cache_key = _hash(str(bytecode))
            if cache_key not in files:
                files[cache_key] = _file_identity(bytecode, budget)
    executable = Path(sys.executable).resolve(strict=True)
    interpreter = {"executable": _file_identity(executable, budget), "version": sys.version, "flags": str(sys.flags)}
    if os.name == "nt":
        kernel = ctypes.WinDLL("kernel32", use_last_error=True)
        function = kernel.GetModuleFileNameW
        function.argtypes, function.restype = [wintypes.HMODULE, wintypes.LPWSTR, wintypes.DWORD], wintypes.DWORD
        runtime_path = ctypes.create_unicode_buffer(32768)
        count = function(sys.dllhandle, runtime_path, len(runtime_path))
        if not count or count >= len(runtime_path):
            raise ValueError("loaded Python runtime unavailable")
        interpreter["loaded_runtime"] = _file_identity(Path(runtime_path.value), budget)
    elif sys.platform.startswith("linux"):
        mapped = {line.split(maxsplit=5)[-1].strip() for line in Path("/proc/self/maps").read_text().splitlines() if len(line.split(maxsplit=5)) == 6 and "/libpython" in line.split(maxsplit=5)[-1]}
        interpreter["loaded_shared_runtimes"] = [_file_identity(Path(path), budget) for path in sorted(mapped)]
    else:
        raise ValueError("loaded Python runtime identity is not audited on this platform")
    return {"interpreter": interpreter, "loaded_files": files, "module_bindings": aliases}


def capture_readonly_inputs(
    root: Path,
    check: Mapping[str, Any],
    *,
    plan_sha256: str,
    effective_environment: Mapping[str, str],
    source_sentinel: Mapping[str, Any] | None = None,
) -> dict[str, Any]:
    """Identify the complete supported input closure, or give a cache-miss reason.

    The caller must execute ``execution_argv`` with ``execution_environment``;
    these are not permission to relabel a differently executed command as closed.
    Capture before and after fresh execution, while holding the execution slot.
    """
    started = time.monotonic()
    argv = check.get("argv")
    result: dict[str, Any] = {"complete": False, "policy": check.get("reuse_policy"), "stdout_must_be_empty": isinstance(argv, list) and len(argv) > 1 and isinstance(argv[0], str) and Path(argv[0]).stem.lower() == "gofmt" and argv[1] == "-l"}
    try:
        if check.get("reuse_policy") != _POLICY:
            raise ValueError("check is not explicitly admitted for reuse")
        if not isinstance(argv, list) or not 3 <= len(argv) <= 128 or argv[1] != "-l" or Path(argv[0]).stem.lower() != "gofmt":
            raise ValueError("only gofmt -l with an explicit file list is admitted")
        if any(not isinstance(value, str) or not value or value.startswith("-") or "\0" in value for value in argv[2:]):
            raise ValueError("stdin and additional formatter flags are not admitted")
        if not _DIGEST.fullmatch(plan_sha256) or not source_sentinel:
            raise ValueError("plan or source sentinel is unconfirmed")
        root = root.resolve(strict=True)
        cwd = root / check.get("cwd", ".")
        if not cwd.resolve(strict=True).is_relative_to(root):
            raise ValueError("working directory escapes repository")
        budget = [_MAX_IDENTITY_BYTES]
        files = {}
        for argument in argv[2:]:
            path = cwd / argument
            if ".." in path.parts or path.suffix != ".go" or not path.resolve(strict=True).is_relative_to(root):
                raise ValueError("formatter inputs must be explicit in-repository Go files")
            relative = path.resolve().relative_to(root).as_posix()
            if relative in files:
                raise ValueError("duplicate formatter input")
            files[relative] = _file_identity(path, budget)
        resolved = shutil.which(argv[0], path=effective_environment.get("PATH"))
        if resolved is None:
            raise ValueError("formatter executable unavailable")
        tool = Path(resolved).resolve(strict=True)
        tool_identity = _file_identity(tool, budget)
        tool_identity.update(_gofmt_version(tool.read_bytes()))
        environment = {"PATH": str(tool.parent), "GOMAXPROCS": "2"}
        if os.name == "nt":
            variables = {key.upper(): value for key, value in effective_environment.items()}
            system = Path(variables.get("SYSTEMROOT", "")).resolve(strict=True)
            if not variables.get("SYSTEMROOT") or not system.is_dir():
                raise ValueError("Windows system directory unavailable")
            environment.update(SystemRoot=str(system), WINDIR=str(system))
        sentinels = {}
        for name in ("AGENTS.md", "AGENT.md", "CLAUDE.md", "CONTEXT.md", "docs/GOAL.md"):
            path = root / name
            sentinels[name] = _file_identity(path, budget) if path.exists() else {"absent": True}
        material = {"policy": _POLICY, "root_sha256": _hash(str(root)), "source_sentinel": dict(source_sentinel), "instructions": sentinels,
                    "plan_sha256": plan_sha256, "check_sha256": _hash(dict(check)), "files": files, "tool": tool_identity,
                    "effective_environment_sha256": _hash(environment), "runtime": _runtime_identity(budget),
                    "os": {"platform": sys.platform, "release": platform.release(), "version": platform.version(), "machine": platform.machine()}}
        result.update(complete=True, fingerprint=_hash(material), material=material, input_file_count=len(files),
                      execution_argv=[str(tool), *argv[1:]], execution_environment=environment)
    except (OSError, ValueError, TypeError, IndexError, UnicodeError) as error:
        result["reason"] = str(error) if isinstance(error, ValueError) else type(error).__name__
    result["capture_elapsed_seconds"] = time.monotonic() - started
    return result


def load_original_pass(summary_path: Path, *, output_root: Path) -> dict[str, Any] | None:
    """Read and verify a direct original PASS in this output root; never repair it."""
    try:
        output_root = output_root.resolve(strict=True)
        summary_path = _regular_path(summary_path)
        directory = summary_path.parent
        if summary_path.name != "summary.json" or directory.parent.resolve() != output_root:
            return None
        for path in directory.rglob("*"):
            if path.is_symlink() or getattr(path.lstat(), "st_file_attributes", 0) & 0x400 or path.is_file() and path.stat().st_nlink != 1:
                return None
        summary_bytes = summary_path.read_bytes()
        checksum_bytes = (directory / "checksums.sha256").read_bytes()
        artifact = RunArtifacts(directory.name, output_root)
        if artifact.verify_checksums():
            return None
        if summary_bytes != summary_path.read_bytes() or checksum_bytes != (directory / "checksums.sha256").read_bytes():
            return None
        receipt = json.loads(summary_bytes)
        if not isinstance(receipt, dict):
            return None
        inputs, checks = receipt.get("inputs", {}), receipt.get("checks", [])
        if not isinstance(inputs, dict) or not isinstance(checks, list) or type(receipt.get("schema_version")) is not int or receipt["schema_version"] != 1 or receipt.get("receipt_kind") != "engineering_verification" or receipt.get("status") != "PASS" or receipt.get("scope_kind") != "targeted" or receipt.get("run_id") != directory.name or receipt.get("errors") or not checks:
            return None
        expected_counts = {"planned_checks": len(checks), "attempted_checks": len(checks), "passed_checks": len(checks), "failed_checks": 0, "not_run_checks": 0}
        if "reused_checks" in receipt:
            expected_counts["reused_checks"] = 0
        if any(type(receipt.get(key)) is not int or receipt[key] != value for key, value in expected_counts.items()) or not isinstance(inputs.get("plan_sha256"), str) or not _DIGEST.fullmatch(inputs["plan_sha256"]):
            return None
        if not isinstance(inputs.get("source_pin_before"), dict) or not inputs["source_pin_before"] or inputs["source_pin_before"] != inputs.get("source_pin_after"):
            return None
        for check in checks:
            if not isinstance(check, dict) or not isinstance(check.get("test_denominator"), dict):
                return None
            if check.get("state") != "passed" or check.get("executed_this_run") is not True or check.get("reused") is not False or check.get("reuse") or type(check.get("exit_code")) is not int or check["exit_code"] != 0 or check.get("timed_out") is not False or check.get("process_tree_stop_confirmed") is not True or check.get("test_denominator", {}).get("complete") is not True:
                return None
            if type(check.get("owned_root_pid")) is not int or check["owned_root_pid"] <= 0 or type(check.get("elapsed_seconds")) not in (int, float) or not math.isfinite(check["elapsed_seconds"]) or check["elapsed_seconds"] < 0 or not check.get("started_utc") or not check.get("finished_utc"):
                return None
            if not isinstance(check["started_utc"], str) or not isinstance(check["finished_utc"], str):
                return None
            started = datetime.fromisoformat(check["started_utc"])
            finished = datetime.fromisoformat(check["finished_utc"])
            if started.utcoffset() is None or finished.utcoffset() is None or finished < started:
                return None
        return {"receipt": receipt, "directory": directory, "summary_sha256": hashlib.sha256(summary_bytes).hexdigest(), "checksums_sha256": hashlib.sha256(checksum_bytes).hexdigest()}
    except (OSError, ValueError, TypeError, KeyError):
        return None


def reused_check(source: dict[str, Any] | None, check: Mapping[str, Any], closure: Mapping[str, Any], *, new_run_root: Path) -> dict[str, Any] | None:
    """Return a provenance-bound observation with no claim of new execution."""
    if source is None or closure.get("complete") is not True:
        return None
    try:
        directory = source["directory"]
        if directory.parent.resolve() != new_run_root.parent.resolve() or directory.resolve() == new_run_root.resolve():
            return None
        original = next(item for item in source["receipt"]["checks"] if item["id"] == check["id"])
        before, after = original.get("reuse_inputs_before", {}), original.get("reuse_inputs_after", {})
        if not isinstance(before, dict) or not isinstance(after, dict):
            return None
        denominator = original["test_denominator"]
        if denominator.get("kind") != "static_files" or type(denominator.get("check_count")) is not int or denominator["check_count"] != 1 or type(denominator.get("input_file_count")) is not int or denominator["input_file_count"] != closure.get("input_file_count") or denominator.get("total") is not None:
            return None
        fingerprint = closure.get("fingerprint")
        if not isinstance(fingerprint, str) or not _DIGEST.fullmatch(fingerprint) or before.get("complete") is not True or after.get("complete") is not True or before.get("fingerprint") != fingerprint or after.get("fingerprint") != fingerprint:
            return None
        prefix = "../" + directory.name + "/"
        record = {**dict(check), "state": "reused", "executed_this_run": False, "reused": True, "exit_code": 0,
                  "started_utc": None, "finished_utc": None, "elapsed_seconds": None, "timed_out": None,
                  "process_tree_stop_confirmed": None, "test_denominator": copy.deepcopy(original["test_denominator"]),
                  "reuse_inputs_before": copy.deepcopy(dict(closure)), "reuse_inputs_after": copy.deepcopy(dict(closure)),
                  "reuse": {"source_summary": prefix + "summary.json", "source_summary_sha256": source["summary_sha256"], "source_checksums_sha256": source["checksums_sha256"], "source_check": copy.deepcopy(original), "denominator_source": "original_execution", "log_source": "original_execution"}}
        for channel in ("stdout", "stderr"):
            relative = Path(original[channel + "_log"])
            if relative.is_absolute() or ".." in relative.parts:
                return None
            path = _regular_path(directory / relative)
            if not path.resolve().is_relative_to(directory.resolve()) or hashlib.sha256(path.read_bytes()).hexdigest() != original[channel + "_sha256"]:
                return None
            if channel == "stdout" and path.read_bytes():
                return None
            record[channel + "_log"] = prefix + relative.as_posix()
            record[channel + "_sha256"] = original[channel + "_sha256"]
        return record
    except (OSError, ValueError, TypeError, KeyError, StopIteration):
        return None


__all__ = ["capture_readonly_inputs", "load_original_pass", "reused_check"]
