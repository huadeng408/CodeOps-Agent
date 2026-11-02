"""Run explicit engineering checks and retain an immutable Verification Receipt.

Windows Job containment is required. Targeted plans name checks explicitly.
A full plan contains only schema_version
and scope_kind; it expands to the project's Go/Python recipe, with frontend
checks when frontend source changed. Release and product acceptance are separate.
"""
from __future__ import annotations

import argparse
from collections import Counter
import errno
import hashlib
import json
import math
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import threading
import time
from datetime import datetime, timezone
from typing import Any

from eval.harness.artifacts import RunArtifacts
from eval.harness.process_tree import OwnedProcessTree
from eval.harness.redaction import redact_credential_text
from eval.harness.source_pin import source_pin
from eval.harness.verification_reuse import capture_readonly_inputs, load_original_pass, reused_check

_ID = re.compile(r"[A-Za-z0-9][A-Za-z0-9._-]{0,127}\Z")
_OUTPUT_LIMIT = 16 * 1024 * 1024
_SUPERVISOR = """import os, signal, subprocess, sys, time
parent = int(sys.argv[1])
if os.getppid() != parent: sys.exit(125)
if sys.stdin.buffer.read(1) != b'1': sys.exit(125)
if os.getppid() != parent: sys.exit(125)
flags = subprocess.CREATE_NO_WINDOW if os.name == 'nt' else 0
child = subprocess.Popen(sys.argv[3:], stdin=subprocess.DEVNULL, creationflags=flags)
deadline = time.monotonic() + float(sys.argv[2])
while child.poll() is None:
    if os.name != 'nt' and os.getppid() != parent: os.killpg(os.getpgrp(), signal.SIGKILL)
    if time.monotonic() >= deadline:
        if os.name != 'nt': os.killpg(os.getpgrp(), signal.SIGKILL)
        child.kill(); child.wait(); sys.exit(124)
    time.sleep(.02)
code = child.returncode
if os.name != 'nt' and os.getppid() != parent: os.killpg(os.getpgrp(), signal.SIGKILL)
if os.name != 'nt' and code < 0:
    if -code not in (signal.SIGKILL, signal.SIGSTOP): signal.signal(-code, signal.SIG_DFL)
    os.kill(os.getpid(), -code)
sys.exit(code)
"""


def _hash(value: Any) -> str:
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(",", ":")).encode()).hexdigest()


def _utc() -> str:
    return datetime.now(timezone.utc).isoformat()


def _git(root: Path, *args: str) -> str:
    return subprocess.check_output(["git", *args], cwd=root, encoding="utf-8", errors="replace").strip()


class _ExecutionSlot:
    def __init__(self, root: Path, timeout: float) -> None:
        directory = Path(_git(root, "rev-parse", "--git-common-dir"))
        if not directory.is_absolute():
            directory = root / directory
        self.path, self.timeout = directory.resolve() / "codeops-verification.lock", timeout
        self.file: Any = None

    def __enter__(self):
        self.file = self.path.open("a+b")
        if self.file.seek(0, os.SEEK_END) == 0:
            self.file.write(b"\0")
            self.file.flush()
        deadline = time.monotonic() + self.timeout
        while True:
            try:
                self.file.seek(0)
                if os.name == "nt":
                    import msvcrt
                    msvcrt.locking(self.file.fileno(), msvcrt.LK_NBLCK, 1)
                else:
                    import fcntl
                    fcntl.flock(self.file.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
                return self
            except OSError as error:
                if error.errno not in (errno.EACCES, errno.EAGAIN):
                    self.file.close()
                    raise
                if time.monotonic() >= deadline:
                    self.file.close()
                    raise TimeoutError("engineering execution slot wait expired") from error
                time.sleep(min(0.05, max(0, deadline - time.monotonic())))

    def __exit__(self, *_):
        self.file.close()


def _load_plan(path: Path, root: Path, run_root: Path) -> tuple[dict[str, Any], list[dict[str, Any]], str]:
    raw = path.read_bytes()
    if len(raw) > 1024 * 1024:
        raise ValueError("plan exceeds 1 MiB")
    plan = json.loads(raw)
    if not isinstance(plan, dict) or type(plan.get("schema_version")) is not int or plan["schema_version"] != 1:
        raise ValueError("plan schema_version must be 1")
    kind = plan.get("scope_kind")
    checks: list[dict[str, Any]]
    if kind == "full":
        if set(plan) != {"schema_version", "scope_kind"}:
            raise ValueError("full checks use the fixed project recipe")
        plan["scope_name"] = "Full Go/Python engineering checks; changed frontend included; release and product acceptance separate"
        checks = [
            {"id": "go-test", "argv": ["go", "test", "-vet=off", "-p", "1", "-json", "./...", "-count=1", "-timeout=30m"], "timeout_seconds": 5400},
            {"id": "go-vet", "argv": ["go", "vet", "-p", "1", "./..."], "timeout_seconds": 1800},
            {"id": "pytest", "argv": [sys.executable, "-m", "pytest", "-q", "--durations=20", "--durations-min=1"], "timeout_seconds": 5400},
        ]
        changed = _git(root, "diff", "HEAD", "--name-only", "--", "frontend") or _git(root, "ls-files", "--others", "--exclude-standard", "--", "frontend")
        if changed:
            npm = "npm.cmd" if os.name == "nt" else "npm"
            checks.extend([
                {"id": "frontend-test", "argv": [npm, "test"], "cwd": "frontend", "timeout_seconds": 1800},
                {"id": "frontend-build", "argv": [npm, "run", "build"], "cwd": "frontend", "timeout_seconds": 1800},
            ])
        binary = run_root / "bin" / ("agent.exe" if os.name == "nt" else "agent")
        checks.extend([
            {"id": "agent-build", "argv": ["go", "build", "-p", "1", "-o", str(binary), "./cmd/agent"], "timeout_seconds": 1800},
            {"id": "python-compile", "argv": [sys.executable, "-m", "compileall", "-q", "-f", "-i", str(run_root / "python-sources.txt")], "timeout_seconds": 300},
            {"id": "python-import", "argv": [sys.executable, "-c", "import orchestrator.runtime.conversation; import orchestrator.server; import eval.experiments"], "timeout_seconds": 300},
            {"id": "diff-check", "argv": ["git", "diff", "HEAD", "--check"], "timeout_seconds": 60},
        ])
    elif kind == "targeted":
        if set(plan) != {"schema_version", "scope_kind", "scope_name", "checks"}:
            raise ValueError("targeted plan requires an explicit scope_name and checks")
        checks = plan["checks"]
    else:
        raise ValueError("scope_kind must be targeted or full")
    if not isinstance(plan.get("scope_name"), str) or not 1 <= len(plan["scope_name"]) <= 200:
        raise ValueError("scope_name must contain 1-200 characters")
    if not isinstance(checks, list) or not 1 <= len(checks) <= 64:
        raise ValueError("plan requires 1-64 checks")
    names = set()
    for check in checks:
        if not isinstance(check, dict) or set(check) - {"id", "argv", "cwd", "timeout_seconds", "reuse_policy"}:
            raise ValueError("invalid check fields")
        if check.get("reuse_policy") not in (None, "readonly_gofmt_files_v1"):
            raise ValueError("unsupported reuse policy")
        name, argv, timeout = check.get("id"), check.get("argv"), check.get("timeout_seconds", 900)
        if not isinstance(name, str) or not _ID.fullmatch(name) or name in names:
            raise ValueError("check id must be safe and unique")
        if not isinstance(argv, list) or not 1 <= len(argv) <= 128 or any(not isinstance(arg, str) or not arg or len(arg) > 65536 or "\0" in arg for arg in argv):
            raise ValueError("argv must contain bounded nonempty strings")
        if type(timeout) not in (int, float) or not math.isfinite(timeout) or not 0 < timeout <= 21600:
            raise ValueError("check timeout must be between zero and six hours")
        cwd = (root / check.get("cwd", ".")).resolve(strict=True)
        if not cwd.is_dir() or not cwd.is_relative_to(root):
            raise ValueError("check cwd must stay inside the repository")
        check.update(cwd=cwd.relative_to(root).as_posix(), timeout_seconds=float(timeout))
        names.add(name)
    return plan, checks, hashlib.sha256(raw).hexdigest()


def _tool_identity(argv: list[str], environment: dict[str, str]) -> dict[str, Any]:
    resolved = shutil.which(argv[0], path=environment.get("PATH"))
    if resolved is None:
        return {"requested": argv[0], "state": "unavailable", "sha256": None}
    try:
        path = Path(resolved).resolve(strict=True)
        return {"requested": argv[0], "resolved": str(path), "state": "observed_executable_only", "sha256": hashlib.sha256(path.read_bytes()).hexdigest()}
    except OSError as error:
        return {"requested": argv[0], "state": "unconfirmed", "sha256": None, "error": type(error).__name__}


def _prepare_full_go(root: Path, environment: dict[str, str]) -> dict[str, Any]:
    """Select an already installed SDK before disabling automatic downloads."""
    def version(value: str) -> tuple[int, ...]:
        match = re.fullmatch(r"(?:go)?(\d+)\.(\d+)(?:\.(\d+))?", value)
        if not match:
            raise ValueError("unconfirmed Go toolchain version")
        return tuple(int(part or "0") for part in match.groups())

    declared = re.findall(r"^(?:go |toolchain go)(\d+\.\d+(?:\.\d+)?)\s*(?://.*)?$", (root / "go.mod").read_text(encoding="utf-8"), re.MULTILINE)
    if not declared:
        raise ValueError("full verification requires a declared Go version")
    required = max(declared, key=version)
    base = shutil.which("go", path=environment.get("PATH"))
    if base is None:
        raise ValueError("Go toolchain is unavailable")
    probe = {**environment, "GOTOOLCHAIN": "local", "GOPROXY": "off", "GOSUMDB": "off"}
    keys = ("GOMODCACHE", "GOCACHE", "GOPATH", "GOROOT", "GOVERSION", "GOHOSTOS", "GOHOSTARCH")
    observed = json.loads(subprocess.check_output([base, "env", "-json", *keys], cwd=root, env=probe, timeout=15))
    if not isinstance(observed, dict) or any(not isinstance(observed.get(key), str) or not observed[key] for key in keys):
        raise ValueError("Go environment identity is unconfirmed")
    sdk = Path(observed["GOROOT"])
    if version(observed["GOVERSION"]) < version(required):
        sdk = Path(observed["GOMODCACHE"]) / f"golang.org/toolchain@v0.0.1-go{required}.{observed['GOHOSTOS']}-{observed['GOHOSTARCH']}"
    executable = sdk / "bin/go.exe"
    if not executable.is_file():
        raise ValueError("required Go toolchain is not installed or cached; no downloads attempted")
    executable = executable.resolve(strict=True)
    environment.update({key: observed[key] for key in ("GOMODCACHE", "GOCACHE", "GOPATH")})
    environment.update(GOROOT=str(sdk.resolve()), PATH=str(executable.parent) + os.pathsep + environment.get("PATH", ""))
    actual = subprocess.check_output([str(executable), "env", "GOVERSION"], cwd=root, env={**environment, "GOENV": "off", "GOTOOLCHAIN": "local", "GOPROXY": "off", "GOSUMDB": "off"}, text=True, timeout=15).strip()
    if version(actual) < version(required):
        raise ValueError("required Go toolchain is not installed or cached; no downloads attempted")
    return {"required_version": "go" + required, "actual_version": actual, "executable": str(executable), "sha256": hashlib.sha256(executable.read_bytes()).hexdigest(), "automatic_downloads": False}


def _test_denominator(check: dict[str, Any], result: dict[str, Any], artifact: RunArtifacts) -> dict[str, Any]:
    argv = check["argv"]
    is_go_test = Path(argv[0]).stem == "go" and len(argv) > 1 and argv[1] == "test"
    text = "\n".join((artifact.root / result[channel + "_log"]).read_text(encoding="utf-8") for channel in ("stdout", "stderr"))
    if is_go_test:
        counts = {label: 0 for label in ("pass", "fail", "skip")}
        packages = counts.copy()
        started: Counter[tuple[str, str]] = Counter()
        ended: Counter[tuple[str, str]] = Counter()
        for line in text.splitlines():
            try:
                event = json.loads(line)
            except ValueError:
                continue
            if not isinstance(event, dict):
                continue
            action, package, test = event.get("Action"), event.get("Package"), event.get("Test", "")
            if not isinstance(package, str) or not isinstance(test, str):
                continue
            key = (package, test)
            if action == "start" and not test or action == "run" and test:
                started[key] += 1
            elif action in counts:
                (counts if test else packages)[action] += 1
                ended[key] += 1
        # Failed Go runs can omit unstarted tests through flags or persisted Go config.
        complete = "-json" in argv and result["exit_code"] == 0 and not result["timed_out"] and result["process_tree_stop_confirmed"] is True and bool(packages) and sum(packages.values()) > 0 and started == ended
        return {"kind": "go-test-json", "unit": "terminal test events including subtests", "complete": complete,
                "test_events": counts, "package_events": packages, "total": sum(counts.values()) if complete else None,
                "reason": None if complete else "complete Go JSON denominator not confirmed"}
    if "pytest" not in argv[:3] and not Path(argv[0]).stem.startswith("pytest"):
        return {"kind": "not_applicable", "complete": True, "total": None, "reason": "only the declared engineering check is counted; no nested test-case claim"}
    pytest_counts: dict[str, int] = {}
    footer = None
    for line in reversed(text.splitlines()):
        found = re.findall(r"(\d+) (passed|failed|skipped|warnings?|errors?|deselected|xfailed|xpassed|rerun)\b", line)
        if re.search(r"\bin [0-9.]+s\b", line) and found:
            pytest_counts = {"warnings" if name.startswith("warning") else "errors" if name.startswith("error") else name: int(value) for value, name in found}
            footer = line[-1000:]
            break
    labels = ("passed", "failed", "skipped", "errors", "xfailed", "xpassed")
    complete = result["exit_code"] in (0, 1) and not result["timed_out"] and bool(pytest_counts) and any(label in pytest_counts for label in labels)
    if result["exit_code"] != 0 and any(arg in {"-x", "--exitfirst"} or arg.startswith("--maxfail") for arg in argv):
        complete = False
    if re.search(r"stopping after \d+ failures?\b", text):
        complete = False
    return {"kind": "pytest", "unit": "reported test outcomes; errors can include setup/teardown", "complete": complete,
            **{label: pytest_counts.get(label, 0) if complete else pytest_counts.get(label) for label in labels},
            "deselected": pytest_counts.get("deselected"), "warnings": pytest_counts.get("warnings"),
            "total": sum(pytest_counts.get(label, 0) for label in labels) if complete else None,
            "footer": footer, "reason": None if complete else "complete test denominator not confirmed"}


def _run_check(root: Path, check: dict[str, Any], environment: dict[str, str], artifact: RunArtifacts, slot: _ExecutionSlot) -> dict[str, Any]:
    started = time.monotonic()
    result = check
    result.update(state="infra_error", executed_this_run=False, reused=False, exit_code=None, started_utc=_utc(), timed_out=False, process_tree_stop_confirmed=None)
    chunks: dict[str, list[str]] = {"stdout": [], "stderr": []}
    output_lock, overflow = threading.Lock(), threading.Event()
    size = 0

    def read(stream: Any, channel: str) -> None:
        nonlocal size
        while True:
            data = stream.read(4096)
            if not data:
                return
            with output_lock:
                remaining = max(0, _OUTPUT_LIMIT - size)
                if remaining:
                    chunks[channel].append(data[:remaining])
                size += len(data)
                if size > _OUTPUT_LIMIT:
                    overflow.set()

    process = None
    tree = None
    readers = []
    try:
        command = [sys.executable, "-I", "-S", "-c", _SUPERVISOR, str(os.getpid()), str(check["timeout_seconds"] + 15), *check["argv"]]
        options: dict[str, Any] = {"creationflags": subprocess.CREATE_NEW_PROCESS_GROUP | subprocess.CREATE_NO_WINDOW} if os.name == "nt" else {"start_new_session": True, "pass_fds": (slot.file.fileno(),)}
        process = subprocess.Popen(command, cwd=root / check["cwd"], env=environment, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, encoding="utf-8", errors="replace", **options)
        result["owned_root_pid"] = process.pid
        tree = OwnedProcessTree(process)
        for channel, stream in (("stdout", process.stdout), ("stderr", process.stderr)):
            thread = threading.Thread(target=read, args=(stream, channel), daemon=True)
            thread.start()
            readers.append(thread)
        assert process.stdin is not None
        deadline = time.monotonic() + check["timeout_seconds"]
        process.stdin.write("1")
        process.stdin.close()
        result["executed_this_run"] = True
        while True:
            if overflow.is_set():
                result["error_code"] = "output_bound_exceeded"
                break
            if time.monotonic() >= deadline:
                result["timed_out"] = True
                result["error_code"] = "command_timeout"
                break
            if process.poll() is not None:
                break
            time.sleep(0.02)
        if process.poll() is None:
            tree.stop()
        result["exit_code"] = process.poll()
        result["state"] = "timed_out" if result["timed_out"] else "infra_error" if result.get("error_code") else "passed" if process.returncode == 0 else "failed"
    except KeyboardInterrupt:
        result.update(state="interrupted", error_code="interrupted")
    except (OSError, ValueError, TimeoutError, subprocess.SubprocessError) as error:
        result["error_code"] = type(error).__name__
    finally:
        try:
            if tree is not None:
                tree.close()
                result["process_tree_stop_confirmed"] = process is not None and process.poll() is not None
            elif process is not None and process.poll() is None:
                process.kill()
                process.wait(timeout=10)
                result["process_tree_stop_confirmed"] = True
            elif process is None:
                result["process_tree_stop_confirmed"] = True
        except (OSError, TimeoutError, subprocess.SubprocessError):
            result["process_tree_stop_confirmed"] = False
        for thread in readers:
            thread.join(timeout=5)
            if thread.is_alive():
                result["process_tree_stop_confirmed"] = False
        if process is not None:
            result["exit_code"] = process.poll()
            if process.stdin is not None:
                process.stdin.close()
            if not any(thread.is_alive() for thread in readers):
                for stream in (process.stdout, process.stderr):
                    if stream is not None:
                        stream.close()
        for channel in chunks:
            text = "".join(chunks[channel])
            if overflow.is_set():
                text = text.rsplit("\n", 1)[0] + "\n[output bound exceeded; check not accepted]\n"
            try:
                path = artifact.record_scorer_output(f"{check['id']}-{channel}.log", text)
                result[channel + "_log"] = path.relative_to(artifact.root).as_posix()
                result[channel + "_sha256"] = hashlib.sha256(path.read_bytes()).hexdigest()
            except (OSError, ValueError) as error:
                result.update(state="infra_error", error_code="log_persistence_unavailable", log_error=type(error).__name__)
                result[channel + "_log"] = result[channel + "_sha256"] = None
        if overflow.is_set():
            result["state"], result["error_code"] = "infra_error", "output_bound_exceeded"
        result["finished_utc"], result["elapsed_seconds"] = _utc(), time.monotonic() - started
    return result


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--plan", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--run-id", required=True)
    parser.add_argument("--reuse-from", type=Path)
    parser.add_argument("--queue-timeout-seconds", type=float, default=60)
    args = parser.parse_args(argv)
    try:
        if os.name != "nt":
            raise ValueError("Windows Job process containment is required; no checks were dispatched")
        if not _ID.fullmatch(args.run_id) or not math.isfinite(args.queue_timeout_seconds) or not 0 <= args.queue_timeout_seconds <= 3600:
            raise ValueError("invalid run id or queue timeout")
        root = Path.cwd().resolve()
        run_root = args.output.resolve() / args.run_id
        if run_root.is_relative_to(root):
            ignored = subprocess.run(["git", "check-ignore", "--quiet", "--no-index", "--", run_root.relative_to(root).as_posix() + "/"], cwd=root, capture_output=True)
            if ignored.returncode != 0:
                raise ValueError("output inside the repository must be under an existing Git-ignored root")
        plan, checks, plan_sha = _load_plan(args.plan, root, run_root)
        if plan["scope_kind"] == "full" and args.reuse_from is not None:
            raise ValueError("full verification always executes fresh; reuse is not accepted")
        environment = dict(os.environ)
        adjustments = {"GOMAXPROCS": "2"}
        environment["GOMAXPROCS"] = "2"
        go_toolchain = None
        if plan["scope_kind"] == "full":
            if any(name.startswith(("CODE_AGENT_RUN_", "CODEOPS_RUN_")) and value == "1" for name, value in environment.items()):
                raise ValueError("full source verification refuses explicit real integration flags")
            for name in ("GOFLAGS", "PYTEST_ADDOPTS"):
                environment.pop(name, None)
                adjustments[name] = "cleared"
            go_toolchain = _prepare_full_go(root, environment)
            adjustments.update(PATH="selected Go SDK bin prepended", GOROOT="selected Go SDK", GOMODCACHE="preserved configured cache", GOCACHE="preserved configured cache", GOPATH="preserved configured value")
            for name, value in {"GOENV": "off", "GOTOOLCHAIN": "local", "GOPROXY": "off", "GOSUMDB": "off"}.items():
                environment[name] = adjustments[name] = value
        run_root.mkdir(parents=True, exist_ok=False)
        artifact = RunArtifacts(args.run_id, args.output)
        (artifact.root / "bin").mkdir()
        inputs: dict[str, Any] = {"plan_sha256": plan_sha, "effective_environment_sha256": _hash(environment), "environment_adjustments": adjustments, "go_toolchain": go_toolchain, "tools": [_tool_identity(check["argv"], environment) for check in checks], "input_closure": {"complete": False, "reason": "arbitrary command inputs and host dependency closure are not confirmed"}}
        records: list[dict[str, Any]] = [{**check, "state": "not_run", "executed_this_run": False, "reused": False, "exit_code": None, "process_tree_stop_confirmed": None} for check in checks]
        receipt: dict[str, Any] = {"schema_version": 1, "receipt_kind": "engineering_verification", "run_id": args.run_id, "scope_kind": plan["scope_kind"], "scope_name": plan["scope_name"], "started_utc": _utc(), "inputs": inputs, "checks": records, "status": "BLOCKED", "errors": [], "planned_checks": len(checks), "non_claims": "Engineering commands only; no product Session, model usage, scorer result, or release acceptance."}
        artifact.write("plan.json", {**plan, "checks": checks})
        try:
            with _ExecutionSlot(root, args.queue_timeout_seconds) as slot:
                inputs["source_pin_before"] = source_pin(root)
                if plan["scope_kind"] == "full":
                    listed = subprocess.check_output(["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z", "--", "orchestrator", "codeagent", "eval", "scripts"], cwd=root).decode("utf-8").split("\0")
                    sources = sorted({path for path in listed if path.endswith(".py") and (root / path).is_file()})
                    if not sources or any("\n" in path or "\r" in path for path in sources):
                        raise ValueError("Python source list must be nonempty and contain no line breaks")
                    source_list = artifact.root / "python-sources.txt"
                    source_list.write_text("\n".join(sources) + "\n", encoding="utf-8")
                    inputs["python_compile_sources"] = {"path": source_list.name, "count": len(sources), "sha256": hashlib.sha256(source_list.read_bytes()).hexdigest()}
                artifact.write_manifest(receipt)
                validation_started = time.monotonic()
                original = load_original_pass(args.reuse_from, output_root=args.output) if args.reuse_from else None
                validation_seconds = time.monotonic() - validation_started if args.reuse_from else 0.0
                receipt["reuse_source_validation_seconds"] = validation_seconds if args.reuse_from else None
                for index, check in enumerate(checks):
                    lookup_started = time.monotonic()
                    closure = capture_readonly_inputs(root, check, plan_sha256=plan_sha, effective_environment=environment, source_sentinel=inputs["source_pin_before"])
                    reused = reused_check(original, check, closure, new_run_root=artifact.root)
                    lookup_seconds = validation_seconds + time.monotonic() - lookup_started
                    miss_reason = closure.get("reason", "no matching valid original execution")
                    threshold = None
                    if reused is not None:
                        threshold = reused["reuse"]["source_check"]["elapsed_seconds"] * 0.8
                        if lookup_seconds >= threshold:
                            reused = None
                            miss_reason = "verification_cost_not_lower_than_execution"
                    if reused is not None:
                        reused.update(reuse_lookup_seconds=lookup_seconds, reuse_cost_threshold_seconds=threshold)
                        records[index] = reused
                        continue
                    record = records[index]
                    record["reuse_inputs_before"] = closure
                    if args.reuse_from:
                        record.update(reuse_miss_reason=miss_reason, reuse_lookup_seconds=lookup_seconds, reuse_cost_threshold_seconds=threshold)
                    execution_environment = environment
                    if closure["complete"]:
                        record["requested_argv"] = check["argv"]
                        record["argv"] = closure["execution_argv"]
                        execution_environment = closure["execution_environment"]
                    records[index] = _run_check(root, record, execution_environment, artifact, slot)
                    if records[index].get("error_code") == "interrupted":
                        records[index]["test_denominator"] = {"kind": "unknown", "complete": False, "total": None, "reason": "check interrupted"}
                        receipt["errors"].append("interrupted")
                        break
                    if records[index].get("error_code") == "log_persistence_unavailable":
                        records[index]["test_denominator"] = {"kind": "unknown", "complete": False, "total": None, "reason": "log persistence unavailable"}
                        receipt["errors"].append("log_persistence_unavailable")
                        break
                    if check.get("reuse_policy"):
                        after = capture_readonly_inputs(root, check, plan_sha256=plan_sha, effective_environment=environment, source_sentinel=inputs["source_pin_before"])
                        records[index]["reuse_inputs_after"] = after
                        if closure["complete"] and (not after["complete"] or closure["fingerprint"] != after["fingerprint"]):
                            records[index].update(state="infra_error", error_code="closed_inputs_changed_during_check")
                    if check.get("reuse_policy") and closure["stdout_must_be_empty"] and (artifact.root / records[index]["stdout_log"]).read_bytes() and records[index]["state"] == "passed":
                        records[index].update(state="failed", error_code="formatter_output_not_empty")
                    denominator = {"kind": "static_files", "complete": True, "total": None, "check_count": 1, "input_file_count": closure["input_file_count"]} if closure["complete"] else _test_denominator(check, records[index], artifact)
                    if plan["scope_kind"] == "full" and denominator.get("deselected", 0):
                        denominator.update(complete=False, total=None, reason="full verification deselected tests")
                    records[index]["test_denominator"] = denominator
                    if not denominator["complete"]:
                        records[index]["source_state"] = records[index]["state"]
                        records[index].update(state="infra_error", error_code="test_denominator_unconfirmed")
                    if records[index]["process_tree_stop_confirmed"] is not True:
                        receipt["errors"].append("process_tree_stop_unconfirmed")
                        break
                inputs["source_pin_after"] = source_pin(root)
                if inputs["source_pin_before"] != inputs["source_pin_after"]:
                    receipt["errors"].append("source_changed_during_checks")
        except KeyboardInterrupt:
            receipt["errors"].append("interrupted")
            for record in records:
                if record["executed_this_run"] and "finished_utc" not in record:
                    record.update(state="interrupted", error_code="interrupted")
        except (OSError, ValueError, TimeoutError, subprocess.SubprocessError) as error:
            receipt["errors"].append(type(error).__name__)
        receipt["attempted_checks"] = sum(check["executed_this_run"] for check in records)
        receipt["passed_checks"] = sum(check["state"] in {"passed", "reused"} for check in records)
        receipt["reused_checks"] = sum(check["state"] == "reused" for check in records)
        receipt["failed_checks"] = sum(check["state"] == "failed" for check in records)
        receipt["not_run_checks"] = sum(check["state"] == "not_run" for check in records)
        receipt["finished_utc"] = _utc()
        if not receipt["errors"] and all(check["state"] in {"passed", "failed", "reused"} for check in records):
            receipt["status"] = "PASS" if all(check["state"] in {"passed", "reused"} for check in records) else "FAIL"
        artifact.write_summary(receipt)
        artifact.write_checksums()
        if artifact.verify_checksums():
            raise ValueError("verification artifact checksums failed")
        summary = {key: receipt[key] for key in ("run_id", "scope_kind", "status", "planned_checks", "attempted_checks", "passed_checks", "failed_checks", "not_run_checks")}
        summary["receipt"] = str(artifact.root / "summary.json")
        print(json.dumps(summary))
        return 130 if "interrupted" in receipt["errors"] else {"PASS": 0, "FAIL": 1, "BLOCKED": 3}[receipt["status"]]
    except KeyboardInterrupt:
        print(json.dumps({"status": "BLOCKED", "error": "interrupted"}))
        return 130
    except (OSError, ValueError, TypeError, KeyError, subprocess.SubprocessError) as error:
        print(json.dumps({"status": "BLOCKED", "error": redact_credential_text(str(error))[:300]}))
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
