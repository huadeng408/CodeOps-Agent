from __future__ import annotations

import hashlib
import json
import os
import signal
import shutil
import subprocess
import sys
import time
from pathlib import Path

import pytest


@pytest.fixture(scope="module")
def source_snapshot(tmp_path_factory) -> Path:
    repository = Path(__file__).resolve().parents[2]
    snapshot = tmp_path_factory.mktemp("experiment-cli-source")
    tracked = subprocess.run(
        ["git", "ls-files", "-z", "--", "eval", "orchestrator", "codeagent"],
        cwd=repository, check=True, capture_output=True,
    ).stdout.decode("utf-8").split("\0")
    sources = {path for path in tracked if path.endswith(".py")}
    sources.update(("eval/experiments.py", "eval/experiment_report.py", "eval/harness/process_tree.py"))
    for relative in sorted(sources):
        destination = snapshot / relative
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(repository / relative, destination)
    (snapshot / ".gitignore").write_text("__pycache__/\n", encoding="utf-8")
    for arguments in (
        ["git", "init", "--quiet"],
        ["git", "add", "."],
        ["git", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--quiet", "-m", "source snapshot"],
    ):
        subprocess.run(arguments, cwd=snapshot, check=True, capture_output=True)
    return snapshot


@pytest.fixture(autouse=True)
def isolated_cli_source(source_snapshot: Path, monkeypatch) -> None:
    monkeypatch.chdir(source_snapshot)


def experiment_config(tmp_path: Path, *, repeats: int = 2) -> Path:
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    (workspace / "calculator.py").write_text("def add(a, b):\n    return a - b\n", encoding="utf-8")
    for command in (
        ["git", "init", "--quiet"],
        ["git", "add", "calculator.py"],
        ["git", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--quiet", "-m", "baseline"],
    ):
        subprocess.run(command, cwd=workspace, check=True, capture_output=True)
    checks = tmp_path / "checks.json"
    checks.write_text(json.dumps({"schema_version": 1, "files": [{"path": "calculator.py", "contains": "return a + b"}]}), encoding="utf-8")
    config = tmp_path / "config.json"
    config.write_text(json.dumps({
        "schema_version": 1,
        "run_id": "public-pilot",
        "experiment": "product_trial",
        "model": "fixture-model-20261031",
        "repeats": repeats,
        "budget": {"max_tokens": 4000, "wall_clock_seconds": 60},
        "tasks": [{
            "task_id": "addition",
            "workspace": str(workspace),
            "message": "Make add(a, b) return the sum of its arguments.",
            "checks_file": str(checks),
        }],
    }), encoding="utf-8")
    return config


def command(*arguments: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [sys.executable, "-m", "eval.experiments", *arguments],
        capture_output=True, text=True, encoding="utf-8", errors="replace", timeout=60,
    )


@pytest.fixture(scope="module")
def refusal_driver_template(tmp_path_factory, source_snapshot: Path) -> Path:
    tmp_path = tmp_path_factory.mktemp("experiment-protocol-driver")
    source = tmp_path / "driver.go"
    source.write_text(r'''package main
import ("encoding/json"; "os"; "os/exec"; "path/filepath"; "runtime"; "strings"; "time")
func main() {
    if len(os.Args) > 1 && os.Args[1] == "--child" { time.Sleep(120*time.Second); return }
    if real := os.Getenv("CODEOPS_REAL_GIT"); real != "" && len(os.Args) > 1 && os.Args[1] != "--eval-trial" {
        if os.Args[1] == "clone" { os.Stderr.WriteString("fixture clone refused\n"); os.Exit(73) }
        command := exec.Command(real, os.Args[1:]...)
        command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
        if err := command.Run(); err != nil { if exit, ok := err.(*exec.ExitError); ok { os.Exit(exit.ExitCode()) }; os.Exit(2) }
        return
    }
    var request map[string]any
    if json.NewDecoder(os.Stdin).Decode(&request) != nil { os.Exit(2) }
    if path := os.Getenv("CODEOPS_FIXTURE_PROTOCOL_LOG"); path != "" {
        file, _ := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
        if file != nil { json.NewEncoder(file).Encode(request); file.Close() }
    }
    if path := os.Getenv("CODEOPS_FIXTURE_DISPATCH_LOG"); path != "" {
        file, _ := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
        if file != nil { file.WriteString(request["task_id"].(string)+"\n"); file.Close() }
    }
    if request["task_id"] == "mutate-next" {
        file, _ := os.OpenFile(os.Getenv("CODEOPS_FIXTURE_MUTATE_PATH"), os.O_APPEND|os.O_WRONLY, 0600)
        if file != nil { file.WriteString("\n"); file.Close() }
        request["type"] = "receipt"
        request["task_state"] = "failed"
        request["session_id"] = "fixture-failed"
        request["execution_path"] = "go-session-runner-grpc"
        request["error_code"] = "fixture_agent_failure"
        request["usage"] = map[string]any{"state":"known", "input_tokens":10, "output_tokens":2, "confirmed_tokens":12, "reserved_tokens":0, "unknown_calls":0, "call_count":1}
        delete(request, "message")
        json.NewEncoder(os.Stdout).Encode(request)
        os.Exit(1)
    }
    if request["task_id"] == "infrastructure-refusal" {
        request["type"] = "receipt"
        request["task_state"] = "failed"
        request["session_id"] = "fixture-failed"
        request["execution_path"] = "go-session-runner-grpc"
        request["error_code"] = os.Getenv("CODEOPS_FIXTURE_INFRASTRUCTURE_CODE")
        request["usage"] = map[string]any{"state":"known", "input_tokens":10, "output_tokens":2, "confirmed_tokens":12, "reserved_tokens":0, "unknown_calls":0, "call_count":1}
        delete(request, "message")
        json.NewEncoder(os.Stdout).Encode(request)
        os.Exit(1)
    }
    if request["task_id"] == "timeout" {
        child := exec.Command(os.Args[0], "--child")
        if child.Start() != nil { os.Exit(3) }
        json.NewEncoder(os.Stdout).Encode(map[string]any{"type":"started", "pid":os.Getpid(), "child_pid":child.Process.Pid})
        time.Sleep(120*time.Second)
        return
    }
    if request["task_id"] == "exit-child" {
        child := exec.Command(os.Args[0], "--child")
        if child.Start() != nil { os.Exit(3) }
        request["child_pid"] = child.Process.Pid
    }
    if request["task_id"] == "production" {
        root := os.Getenv("CODEOPS_FIXTURE_APPROVED_ROOT")
        mode := os.Getenv("CODEOPS_FIXTURE_PRODUCTION")
        workspaceID, sessionID := "fixture-task", "fixture-production-session"
        if strings.HasPrefix(mode, "graded_fault_missing_") { workspaceID = "fixture-"+request["run_id"].(string); sessionID = "session-"+request["run_id"].(string) }
        actual := filepath.Join(root, workspaceID)
        if mode == "workspace_escape" { actual = filepath.Join(filepath.Dir(root), "escaped-fixture") }
        os.MkdirAll(actual, 0700)
        if request["operation"] == "run" {
            os.WriteFile(filepath.Join(actual, "calculator.py"), []byte("def add(a, b):\n    return a + b\n"), 0600)
            if mode == "source_mutation" { os.WriteFile(filepath.Join(request["workspace"].(string), "calculator.py"), []byte("changed source"), 0600) }
        }
        task := map[string]any{"approved_root":root, "working_dir":actual, "workspace_id":workspaceID, "lease_id":workspaceID, "baseline_checksum":strings.Repeat("b",64), "lease_expires_at":"2099-01-01T00:00:00Z", "verified":true, "admission_ready":true, "prepared_event_id":strings.Repeat("c",32), "prepared_event_checksum":strings.Repeat("c",64)}
        if mode == "fake_workspace_id" { task["workspace_id"] = "other-task"; task["lease_id"] = "other-task" }
        runtimeID := strings.Repeat("d",64) + "/11111111-1111-4111-8111-111111111111/" + strings.Repeat("e",64)
        tool := map[string]any{"session_id":sessionID, "run_id":"fixture-product-run", "tool_call_id":"fixture-tool", "tool_name":"Bash", "dispatch_count":1, "result_count":1, "unknown_count":0, "commit_done_count":1, "runtime_id":runtimeID, "stopped":true, "outcome_unknown":false, "result_event_id":strings.Repeat("f",32), "result_event_checksum":strings.Repeat("f",64)}
        if mode == "bad_event_ref" { tool["result_event_id"] = "unbound-event" }
        if mode == "unknown_stop" { tool["stopped"] = false; tool["outcome_unknown"] = true }
        if mode == "missing_runtime" { tool["runtime_id"] = "" }
        receipt := map[string]any{"type":"receipt", "schema_version":1, "run_id":request["run_id"], "task_id":request["task_id"], "session_id":sessionID, "product_run_id":"fixture-product-run", "trace_id":"0123456789abcdef0123456789abcdef", "pid":os.Getpid(), "workspace":actual, "source_workspace":request["workspace"], "runtime_contract":"production_opensandbox", "product_path":"opensandbox-production", "execution_path":"go-session-runner-grpc", "model":request["model"], "policy":request["policy"], "budget":request["budget"], "task_state":"completed", "task_workspace":task, "opensandbox":map[string]any{"manifest_sha256":strings.Repeat("1",64), "source_sha":strings.Repeat("2",40), "policy_sha256":strings.Repeat("3",64), "server_container_id":strings.Repeat("4",64), "server_image":"sha256:"+strings.Repeat("5",64), "execd_image":"sha256:"+strings.Repeat("6",64), "egress_image":"sha256:"+strings.Repeat("7",64), "task_image":"sha256:"+strings.Repeat("8",64)}, "usage":map[string]any{"state":"known", "input_tokens":10, "output_tokens":2, "confirmed_tokens":12, "reserved_tokens":0, "unknown_calls":0, "call_count":1}, "ledger":map[string]any{"verified":true, "session_id":sessionID, "head_checksum":strings.Repeat("a",64), "event_count":8}, "execution_evidence":map[string]any{"verified":true, "tools":[]any{tool}}}
        if strings.HasPrefix(mode, "graded_identity_") {
            profile := map[string]any{"protocol":"openai", "model":request["model"], "expected_reported_model":request["model"], "reasoning_effort":"xhigh"}
            reported := []any{request["model"]}
            profiles := []any{profile}
            if mode == "graded_identity_reported_model" { reported = append(reported, "different-reflection-model") }
            if mode == "graded_identity_request_profile" { profiles = append(profiles, map[string]any{"protocol":"openai", "model":request["model"], "expected_reported_model":request["model"], "reasoning_effort":"high"}) }
            receipt["product_runtime"] = map[string]any{"model_profile":profile}
            receipt["model_identity"] = map[string]any{"requested_model":request["model"], "expected_reported_model":request["model"], "reported_models":reported, "effective_request_profiles":profiles, "system_fingerprints":[]string{""}, "revision_status":"unknown"}
        }
        if mode == "wrong_contract" { receipt["runtime_contract"] = "legacy"; receipt["product_path"] = "cli-file-tools" }
        if request["lifecycle"] != nil { receipt["lifecycle"] = request["lifecycle"] }
        if request["turn_id"] != nil { receipt["turn_id"] = request["turn_id"] }
        if strings.HasPrefix(mode, "missing_receipt_") {
            receipt["task_state"], receipt["run_state"] = "running", "running"
            receipt["usage"] = map[string]any{"state":"unknown", "input_tokens":10, "output_tokens":2, "confirmed_tokens":12, "reserved_tokens":4000, "unknown_calls":1, "call_count":2, "cost_status":"unknown"}
            marker := filepath.Join(filepath.Dir(root), "fixture-missing-finalized")
            if request["operation"] == "run" {
                child := exec.Command(os.Args[0], "--child")
                if child.Start() != nil { os.Exit(3) }
                receipt["type"], receipt["child_pid"] = "started", child.Process.Pid
                if mode == "missing_receipt_bad_pid" { receipt["pid"] = os.Getpid()+1 }
                if mode == "missing_receipt_bad_runtime" { receipt["runtime_contract"] = "legacy" }
                json.NewEncoder(os.Stdout).Encode(receipt)
                if mode == "missing_receipt_malformed_stdout" { os.Stdout.WriteString("{invalid-fixture-json\n") }
                if mode == "missing_receipt_invalid_final_identity" {
                    receipt["type"], receipt["run_id"] = "receipt", "unbound-final-request"
                    json.NewEncoder(os.Stdout).Encode(receipt)
                }
                if mode == "missing_receipt_timeout" { time.Sleep(120*time.Second) }
                os.Exit(2)
            }
            if request["operation"] == "inspect" && mode == "missing_receipt_inspect_wrong_run" { receipt["product_run_id"] = "different-product-run" }
            if request["operation"] == "finalize" {
                if request["finalize_mode"] != "abandon" || request["session_id"] != sessionID || request["expected_session_head_checksum"] != strings.Repeat("a",64) { os.Exit(4) }
                if mode == "missing_receipt_abandon_refused" {
                    receipt["task_state"], receipt["error_code"], receipt["admission_finalized"] = "infra_error", "tool_outcome_unknown", false
                    json.NewEncoder(os.Stdout).Encode(receipt)
                    os.Exit(3)
                }
                os.WriteFile(marker, []byte("yes"), 0600)
                receipt["task_state"], receipt["run_state"], receipt["admission_finalized"] = "finalized", "canceling", true
            }
            if _, err := os.Stat(marker); err == nil {
                receipt["ledger"] = map[string]any{"verified":true, "session_id":sessionID, "head_checksum":strings.Repeat("6",64), "event_count":11}
                if request["operation"] == "inspect" {
                    receipt["task_state"], receipt["run_state"], receipt["admission_finalized"] = "finalized", "canceling", true
                    if mode == "missing_receipt_stale_final_inspect" { receipt["ledger"].(map[string]any)["head_checksum"] = strings.Repeat("7",64) }
                }
            }
            json.NewEncoder(os.Stdout).Encode(receipt)
            return
        }
        if strings.HasPrefix(mode, "post_prepare_") {
            state := os.Getenv("CODEOPS_FIXTURE_PREPARED_STATE")
            task["admission_ready"] = false
            if mode == "post_prepare_bad_proof" { task["workspace_id"] = "unbound-workspace" }
            receipt["task_state"], receipt["product_run_id"] = state, ""
            receipt["error_code"] = "production_workspace_integrity_unconfirmed"
            receipt["diagnostic"] = map[string]any{"stage":"workspace_admission"}
            receipt["usage"] = map[string]any{"state":"unknown", "input_tokens":0, "output_tokens":0, "confirmed_tokens":0, "reserved_tokens":0, "unknown_calls":0, "call_count":0, "cost_status":"unknown"}
            delete(receipt, "trace_id")
            delete(receipt, "execution_evidence")
            json.NewEncoder(os.Stdout).Encode(receipt)
            os.Exit(3)
        }
        policy := request["policy"].(map[string]any)
        failedFault := mode == "graded_fault_missing_failed" && policy["fault_window"] != nil
        providerUnknown := mode == "provider_unknown"
        modelFailed := mode == "model_failed" || mode == "provider_failed_inspect" || failedFault || providerUnknown
        if modelFailed { receipt["task_state"] = "failed"; receipt["error_code"] = "fixture_agent_failure" }
        if mode == "provider_failed_inspect" || failedFault {
            receipt["error_code"] = "provider_model_identity_unconfirmed"
            receipt["diagnostic"] = map[string]any{"stage":"orchestrator_terminal", "terminal_code":"provider_model_identity_unconfirmed"}
            receipt["usage"] = map[string]any{"state":"known", "input_tokens":9184, "output_tokens":287, "cached_input_tokens":128, "confirmed_tokens":9471, "reserved_tokens":0, "unknown_calls":0, "call_count":1, "cost_status":"unknown"}
            if request["operation"] == "finalize" && mode == "provider_failed_inspect" {
                data, _ := os.ReadFile(os.Getenv("CODEOPS_FIXTURE_EARLY_ROW"))
                var row map[string]any
                json.Unmarshal([]byte(strings.Split(strings.TrimSpace(string(data)), "\n")[0]), &row)
                if row["session_id"] != sessionID || row["error_code"] != "provider_model_identity_unconfirmed" || row["tokens_in"] != float64(9184) || row["receipt_path"] == nil { os.Stderr.WriteString("model failure was not persisted before cleanup\n"); os.Exit(4) }
            }
        }
        if mode == "graded_completed_error" && request["operation"] == "run" { receipt["error_code"] = os.Getenv("CODEOPS_FIXTURE_COMPLETED_ERROR") }
        if providerUnknown {
            receipt["error_code"] = os.Getenv("CODEOPS_FIXTURE_PROVIDER_ERROR")
            receipt["diagnostic"] = map[string]any{"stage":"orchestrator_terminal", "terminal_code":receipt["error_code"]}
            receipt["usage"] = map[string]any{"state":"unknown", "input_tokens":10094, "output_tokens":484, "cached_input_tokens":3840, "confirmed_tokens":10578, "reserved_tokens":500000, "unknown_calls":1, "call_count":2, "cost_status":"unknown"}
        }
        if request["operation"] == "finalize" { receipt["task_state"] = "finalized"; receipt["run_state"] = "completed"; receipt["admission_finalized"] = true }
        if request["operation"] == "finalize" && modelFailed { receipt["run_state"] = "failed"; receipt["error_code"] = "" }
        gradeState := filepath.Join(filepath.Dir(root), "fixture-grade-state.json")
        finalizedState := filepath.Join(filepath.Dir(root), "fixture-finalized")
        if strings.HasPrefix(mode, "graded_fault_missing_") { gradeState = filepath.Join(filepath.Dir(root), sessionID+"-grade.json"); finalizedState = filepath.Join(filepath.Dir(root), sessionID+"-finalized") }
        if mode == "graded_no_model_osb" { receipt["execution_evidence"] = map[string]any{"verified":true, "tools":[]any{}} }
        if request["operation"] == "grade" && mode == "candidate_freeze_unavailable" { receipt["task_state"] = "blocked"; receipt["error_code"] = "candidate_freeze_unavailable" }
        if request["operation"] == "grade" && strings.HasPrefix(mode, "graded_") {
            passed, count := mode != "graded_fail", 1
            if !passed { count = 0 }
            grade := map[string]any{"state":"completed", "passed":passed, "checks_total":1, "checks_passed":count, "candidate_sha256":strings.Repeat("9",64), "candidate_archive_sha256":strings.Repeat("a",64), "input_sha256":strings.Repeat("b",64), "checker_sha256":strings.Repeat("c",64), "checks_sha256":request["grade_checks_sha256"], "output_sha256":strings.Repeat("e",64), "operation_id":strings.Repeat("8",64), "runtime_id":strings.Repeat("8",64)+"/22222222-2222-4222-8222-222222222222/"+strings.Repeat("9",64), "stopped":true, "exit_code":0, "intent_event_id":strings.Repeat("1",32), "intent_event_checksum":strings.Repeat("2",64), "result_event_id":strings.Repeat("3",32), "result_event_checksum":strings.Repeat("4",64), "already_recorded":false}
            if mode == "graded_bad_checks_hash" { grade["checks_sha256"] = strings.Repeat("0",64) }
            if mode == "graded_unknown_stop" { grade["stopped"] = false }
            if mode == "graded_unknown" || mode == "graded_unknown_cleanup_blocked" { grade["state"] = "unknown"; grade["passed"] = nil; grade["checks_passed"] = nil }
            if mode == "graded_unknown_cleanup_blocked" { grade["stopped"] = false }
            receipt["task_state"] = "graded"
            receipt["grade"] = grade
            if mode == "graded_unknown" || mode == "graded_unknown_cleanup_blocked" { receipt["task_state"] = "scorer_error"; receipt["error_code"] = "grader_outcome_unknown" }
            data, _ := json.Marshal(grade)
            os.WriteFile(gradeState, data, 0600)
        }
        if _, err := os.Stat(gradeState); err == nil && (request["operation"] == "grade" || request["operation"] == "inspect") && mode != "graded_stale_head" {
            receipt["ledger"] = map[string]any{"verified":true, "session_id":sessionID, "head_event_id":strings.Repeat("3",32), "head_checksum":strings.Repeat("4",64), "event_count":10}
        }
        if _, err := os.Stat(gradeState); err == nil { task["admission_ready"] = false }
        if request["operation"] == "grade" && mode == "graded_stale_head" { receipt["ledger"] = map[string]any{"verified":true, "session_id":sessionID, "head_event_id":strings.Repeat("3",32), "head_checksum":strings.Repeat("4",64), "event_count":10} }
        if request["operation"] == "finalize" && mode != "graded_unknown_cleanup_blocked" { os.WriteFile(finalizedState, []byte("yes"), 0600) }
        if _, err := os.Stat(finalizedState); err == nil && (request["operation"] == "finalize" || request["operation"] == "inspect") {
            receipt["ledger"] = map[string]any{"verified":true, "session_id":sessionID, "head_event_id":strings.Repeat("5",32), "head_checksum":strings.Repeat("6",64), "event_count":11}
        }
        if request["operation"] == "finalize" && mode == "graded_unknown_cleanup_blocked" { receipt["task_state"] = "infra_error"; receipt["error_code"] = "admission_finalization_unavailable"; receipt["admission_finalized"] = false; json.NewEncoder(os.Stdout).Encode(receipt); os.Exit(3) }
        if request["operation"] == "inspect" && mode == "provider_failed_inspect" { json.NewEncoder(os.Stdout).Encode(receipt); os.Exit(3) }
        json.NewEncoder(os.Stdout).Encode(receipt)
        return
    }
    if request["task_id"] == "sequence" {
        sessionID := "fixture-" + request["run_id"].(string)
        if request["lifecycle"] != "hold" { os.Exit(4) }
        if supplied, ok := request["session_id"].(string); ok && supplied != sessionID { os.Exit(4) }
        second := request["turn_id"] == "turn-001"
        calls := 1
        head := strings.Repeat("a",64)
        if second { calls = 2; head = strings.Repeat("b",64) }
        if request["operation"] == "run" {
            if second && request["session_id"] != sessionID { os.Exit(4) }
            data, _ := json.Marshal(map[string]any{"active":request["message"]})
            os.WriteFile(filepath.Join(request["workspace"].(string), "current-rule.json"), data, 0600)
        }
        state := "completed"
        if request["operation"] == "finalize" {
            if request["finalize_mode"] != "finish" || request["session_id"] != sessionID { os.Exit(4) }
            state = "finalized"
        }
        if request["operation"] == "finalize" || request["operation"] == "inspect" { head = strings.Repeat("c",64) }
        receipt := map[string]any{"type":"receipt", "schema_version":1, "run_id":request["run_id"], "task_id":request["task_id"], "session_id":sessionID, "product_run_id":"fixture-run", "trace_id":"0123456789abcdef0123456789abcdef", "pid":os.Getpid(), "workspace":request["workspace"], "execution_path":"go-session-runner-grpc", "model":request["model"], "policy":request["policy"], "budget":request["budget"], "lifecycle":"hold", "turn_id":request["turn_id"], "task_state":state, "run_state":"completed", "admission_finalized":state=="finalized", "usage":map[string]any{"state":"known", "input_tokens":10*calls, "output_tokens":2*calls, "confirmed_tokens":12*calls, "reserved_tokens":0, "unknown_calls":0, "call_count":calls}, "ledger":map[string]any{"verified":true, "session_id":sessionID, "head_checksum":head, "event_count":6+calls}, "execution_evidence":map[string]any{"verified":true, "root_compaction_count":calls-1, "compaction_count":calls-1, "duplicate_effects":nil, "physical_execution_count":nil}}
        json.NewEncoder(os.Stdout).Encode(receipt)
        return
    }
    if request["task_id"] == "recover" || request["task_id"] == "json-bool" || request["task_id"] == "corrupt-agents" {
        policy := request["policy"].(map[string]any)
        sessionID := "fixture-" + request["run_id"].(string)
        if supplied, ok := request["session_id"].(string); ok && supplied != sessionID { os.Exit(4) }
        base := map[string]any{"schema_version":1, "run_id":request["run_id"], "task_id":request["task_id"], "session_id":sessionID, "product_run_id":"fixture-run", "trace_id":"0123456789abcdef0123456789abcdef", "pid":os.Getpid(), "workspace":request["workspace"], "execution_path":"go-session-runner-grpc", "model":request["model"], "policy":policy, "budget":request["budget"]}
        _, continuedBefore := os.Stat(filepath.Join(request["workspace"].(string), ".fixture-continued"))
        if request["operation"] == "run" && policy["fault_window"] != nil && continuedBefore != nil {
            child := exec.Command(os.Args[0], "--child")
            if child.Start() != nil { os.Exit(3) }
            base["type"] = "started"
            json.NewEncoder(os.Stdout).Encode(base)
            base["type"] = "fault_ready"
            base["window"] = policy["fault_window"]
            base["tool_name"] = "Write"
            base["tool_call_id"] = "fixture-tool"
            base["child_pid"] = child.Process.Pid
            boundary := map[string]any{"schema_version":1,"window":policy["fault_window"],"session_id":sessionID,"product_run_id":"fixture-run","tool_call_id":"fixture-tool","tool_name":"Write","ready":map[string]any{"session_id":sessionID,"event_id":strings.Repeat("b",32),"seq":5,"checksum":strings.Repeat("c",64)},"process":map[string]any{"host":strings.Repeat("d",64),"platform":runtime.GOOS,"pid":os.Getpid(),"started":"fixture-start"},"hold_deadline_unix_nano":time.Now().Add(60*time.Second).UnixNano()}
            if os.Getenv("CODEOPS_FIXTURE_FAULT_PROOF") == "expired_ready" { boundary["hold_deadline_unix_nano"] = time.Now().Add(-time.Second).UnixNano() }
            base["fault_boundary"] = boundary
            if os.Getenv("CODEOPS_FIXTURE_FAULT_PROOF") == "legacy_ready" { delete(base, "fault_boundary") }
            encoded, _ := json.Marshal(boundary)
            os.WriteFile(filepath.Join(request["workspace"].(string), ".fixture-fault-boundary.json"), encoded, 0600)
            json.NewEncoder(os.Stdout).Encode(base)
            time.Sleep(120*time.Second)
            return
        }
        if request["operation"] == "run" || request["operation"] == "continue" {
            if request["operation"] == "continue" { os.WriteFile(filepath.Join(request["workspace"].(string), ".fixture-continued"), []byte("yes"), 0600) }
            current, _ := json.Marshal(map[string]any{"active":request["message"]})
            os.WriteFile(filepath.Join(request["workspace"].(string), "current-rule.json"), current, 0600)
            if request["task_id"] == "json-bool" {
                os.WriteFile(filepath.Join(request["workspace"].(string), "settings.json"), []byte("{\"schema_version\":true}"), 0600)
            } else {
                os.WriteFile(filepath.Join(request["workspace"].(string), "calculator.py"), []byte("def add(a, b):\n    return a + b\n"), 0600)
                if request["task_id"] == "corrupt-agents" { os.WriteFile(filepath.Join(request["workspace"].(string), "AGENTS.md"), []byte("changed instructions"), 0600) }
            }
        }
        base["type"] = "receipt"
        base["task_state"] = "completed"
        base["run_state"] = "completed"
        if request["operation"] == "continue" && os.Getenv("CODEOPS_FIXTURE_ABANDON") == "continue_failure" { base["task_state"] = "failed"; base["run_state"] = "failed"; base["error_code"] = "fixture_agent_failure" }
        if request["operation"] == "inspect" && policy["fault_window"] != nil {
            if _, err := os.Stat(filepath.Join(request["workspace"].(string), ".fixture-continued")); err != nil {
                base["task_state"] = "running"; base["run_state"] = "running"
                if os.Getenv("CODEOPS_FIXTURE_ABANDON") == "wrong_run" { base["product_run_id"] = "fixture-other-run" }
            }
        }
        if request["lifecycle"] != nil { base["lifecycle"] = request["lifecycle"] }
        if request["turn_id"] != nil { base["turn_id"] = request["turn_id"] }
        if request["operation"] == "finalize" {
            base["task_state"] = "finalized"
            base["admission_finalized"] = true
            if request["finalize_mode"] == "abandon" { base["run_state"] = "canceled" }
        }
        calls := 1
        if request["turn_id"] == "turn-001" { calls = 2 }
        base["usage"] = map[string]any{"state":"known", "input_tokens":10*calls, "output_tokens":2*calls, "confirmed_tokens":12*calls, "reserved_tokens":0, "unknown_calls":0, "call_count":calls, "cost_status":"unknown"}
        if request["operation"] == "finalize" && request["finalize_mode"] == "abandon" {
            if os.Getenv("CODEOPS_FIXTURE_ABANDON") == "unknown_usage" { base["usage"] = map[string]any{"state":"unknown", "input_tokens":10, "output_tokens":2, "confirmed_tokens":12, "reserved_tokens":4000, "unknown_calls":1, "call_count":2, "cost_status":"unknown"} }
            if os.Getenv("CODEOPS_FIXTURE_ABANDON") == "refused" { base["task_state"] = "infra_error"; base["admission_finalized"] = false; base["error_code"] = "tool_outcome_unknown" }
        }
        if os.Getenv("CODEOPS_FIXTURE_ABANDON") == "missing_restart_usage" && strings.HasSuffix(request["run_id"].(string), "--restart") { base["usage"] = map[string]any{"state":"known", "output_tokens":2, "confirmed_tokens":12, "reserved_tokens":0, "unknown_calls":0, "call_count":1} }
        base["ledger"] = map[string]any{"verified":true, "session_id":sessionID, "head_checksum":strings.Repeat("a",64), "event_count":6}
        base["execution_evidence"] = map[string]any{"verified":true, "root_compaction_count":calls-1, "compaction_count":calls-1, "duplicate_effects":nil, "physical_execution_count":nil}
        if data, err := os.ReadFile(filepath.Join(request["workspace"].(string), ".fixture-fault-boundary.json")); err == nil {
            proof := map[string]any{}
            decoder := json.NewDecoder(strings.NewReader(string(data)))
            decoder.UseNumber()
            decoder.Decode(&proof)
            proof["verified"], proof["result_order_verified"], proof["process_stopped"] = true, true, true
            proof["released"], proof["run_terminal"], proof["cancel_requested"] = false, base["run_state"] != "running", false
            mode := os.Getenv("CODEOPS_FIXTURE_FAULT_PROOF")
            if request["operation"] == "inspect" && continuedBefore != nil {
                if mode == "released" { proof["released"] = true }
                if mode == "canceled" { proof["cancel_requested"] = true }
                if mode == "terminal" { proof["run_terminal"] = true }
                if mode == "wrong_reference" { proof["ready"].(map[string]any)["checksum"] = strings.Repeat("0",64) }
                if mode == "unordered" { proof["result_order_verified"] = false }
            }
            base["execution_evidence"].(map[string]any)["fault_boundaries"] = []any{proof}
        }
        strategy := map[string]any{"verified":true, "child_session_ids":[]string{}, "child_model_call_count":0, "visible_verification_count":0}
        if policy["execution_strategy"] == "verify_retry" && os.Getenv("CODEOPS_FIXTURE_STRATEGY") != "missing_verification" {
            observation := os.Getenv("CODEOPS_FIXTURE_STRATEGY")
            count := 1
            if observation == "retry_performed" { count = 2 }
            if observation == "extra_retry" { count = 3 }
            attempts := []any{}
            for index := 0; index < count; index++ {
                code, state := 0, "passed"
                if index < count-1 { code, state = 1, "failed" }
                marker := string(rune('a'+index))
                attempts = append(attempts, map[string]any{"session_id":sessionID, "run_id":"fixture-run", "tool_call_id":"verification-"+marker, "command_sha256":strings.Repeat("d",64), "pending_event_id":"1"+strings.Repeat(marker,31), "pending_checksum":"1"+strings.Repeat(marker,63), "dispatch_event_id":"2"+strings.Repeat(marker,31), "dispatch_checksum":"2"+strings.Repeat(marker,63), "result_event_id":"3"+strings.Repeat(marker,31), "result_checksum":"3"+strings.Repeat(marker,63), "state":state, "exit_code":code})
            }
            strategy["visible_verification_count"] = count
            strategy["retry_evidence"] = map[string]any{"attempt_count":count, "retry_observed":count>1, "bound_enforced":observation!="missing_bound", "attempts":attempts}
        }
        if policy["execution_strategy"] == "subagent" && os.Getenv("CODEOPS_FIXTURE_STRATEGY") != "missing_child" { strategy["child_session_ids"] = []string{"fixture-child"}; strategy["child_model_call_count"] = 1 }
        base["strategy_evidence"] = strategy
        json.NewEncoder(os.Stdout).Encode(base)
        if base["task_state"] == "running" || base["task_state"] == "infra_error" || base["task_state"] == "failed" { os.Exit(1) }
        return
    }
    request["type"] = "receipt"
    request["task_state"] = "blocked"
    if task, ok := request["task_id"].(string); ok && strings.HasSuffix(task, "infra_error") { request["task_state"] = "infra_error" }
    request["session_id"] = ""
    request["execution_path"] = "go-session-runner-grpc"
    request["error_code"] = "orchestrator_startup_unavailable"
    request["diagnostic"] = map[string]string{"stage":"orchestrator_startup", "grpc_status":"Unavailable"}
    delete(request, "workspace")
    delete(request, "message")
    json.NewEncoder(os.Stdout).Encode(request)
    os.Exit(1)
}
''', encoding="utf-8")
    binary = tmp_path / ("driver-fixture.exe" if os.name == "nt" else "driver-fixture")
    subprocess.run(["go", "build", "-o", str(binary), str(source)], cwd=source_snapshot, check=True, capture_output=True, timeout=60)
    return binary


@pytest.fixture
def refusal_driver(tmp_path: Path, refusal_driver_template: Path) -> Path:
    binary = tmp_path / refusal_driver_template.name
    shutil.copy2(refusal_driver_template, binary)
    return binary


def test_public_plan_freezes_inputs_and_keeps_every_planned_trial(tmp_path: Path) -> None:
    config = experiment_config(tmp_path)
    result = command("plan", "--config", str(config), "--output", str(tmp_path / "output"))

    assert result.returncode == 0, result.stderr
    plan = json.loads((tmp_path / "output/public-pilot/plan.json").read_text(encoding="utf-8"))
    assert plan["schema_version"] == 1
    assert plan["config_sha256"] == hashlib.sha256(config.read_bytes()).hexdigest()
    assert plan["planned_trials"] == 2
    assert [row["repeat"] for row in plan["trials"]] == [0, 1]
    assert all(row["status"] == "not_run" and row["success"] is None for row in plan["trials"])
    assert all(row["tokens_in"] is None and row["cost"] is None for row in plan["trials"])
    assert "return a + b" not in json.dumps(plan)
    assert (tmp_path / "output/public-pilot/checksums.sha256").is_file()


@pytest.mark.parametrize("contract", ["legacy", "production_opensandbox"])
def test_public_plan_freezes_the_runtime_contract_in_every_arm(tmp_path: Path, contract: str) -> None:
    config = experiment_config(tmp_path, repeats=1)
    payload = json.loads(config.read_text(encoding="utf-8"))
    payload.update(experiment="context_memory", runtime_contract=contract)
    if contract == "production_opensandbox":
        approved = tmp_path / "approved-runtime"
        approved.mkdir()
        payload["approved_workspace_root"] = str(approved)
    config.write_text(json.dumps(payload), encoding="utf-8")

    result = command("plan", "--config", str(config), "--output", str(tmp_path / "output"))

    assert result.returncode == 0, result.stderr
    plan = json.loads((tmp_path / "output/public-pilot/plan.json").read_text(encoding="utf-8"))
    assert plan["runtime_contract"] == contract
    assert len(plan["trials"]) == 4 and all(row["runtime_contract"] == contract for row in plan["trials"])
    if contract == "production_opensandbox":
        assert plan["approved_workspace_root_sha256"] == hashlib.sha256(str(approved.resolve()).encode("utf-8")).hexdigest()


def test_public_production_plan_keeps_hidden_checks_outside_the_runtime_root(tmp_path: Path) -> None:
    config = experiment_config(tmp_path, repeats=1)
    payload = json.loads(config.read_text(encoding="utf-8"))
    approved = tmp_path / "approved-runtime"
    approved.mkdir()
    hidden = approved / "hidden.json"
    hidden.write_bytes((tmp_path / "checks.json").read_bytes())
    payload.update(runtime_contract="production_opensandbox", approved_workspace_root=str(approved))
    payload["tasks"][0]["checks_file"] = str(hidden)
    config.write_text(json.dumps(payload), encoding="utf-8")

    result = command("plan", "--config", str(config), "--output", str(tmp_path / "output"))

    assert result.returncode == 2 and "outside" in result.stderr
    assert not (tmp_path / "output/public-pilot").exists()


@pytest.mark.parametrize("count", [256, 257])
def test_public_production_plan_checks_the_native_grader_limit_before_dispatch(tmp_path: Path, count: int) -> None:
    config = experiment_config(tmp_path, repeats=1)
    payload = json.loads(config.read_text(encoding="utf-8"))
    approved = tmp_path / "approved-runtime"
    approved.mkdir()
    payload.update(runtime_contract="production_opensandbox", approved_workspace_root=str(approved))
    config.write_text(json.dumps(payload), encoding="utf-8")
    checks = tmp_path / "checks.json"
    checks.write_text(json.dumps({"schema_version": 1, "files": [{"path": "calculator.py", "contains": "return a + b"}] * count}), encoding="utf-8")

    result = command("plan", "--config", str(config), "--output", str(tmp_path / "output"))

    if count == 256:
        assert result.returncode == 0, result.stderr
    else:
        assert result.returncode == 2 and "256" in result.stderr
        assert not (tmp_path / "output/public-pilot").exists()


def test_public_plan_refuses_unknown_evidence_kind_before_execution(tmp_path: Path) -> None:
    config = experiment_config(tmp_path)
    payload = json.loads(config.read_text(encoding="utf-8"))
    payload["evidence_kind"] = "provder"
    config.write_text(json.dumps(payload), encoding="utf-8")

    result = command("plan", "--config", str(config), "--output", str(tmp_path / "output"))

    assert result.returncode == 2
    assert "evidence_kind" in result.stderr
    assert not (tmp_path / "output/public-pilot").exists()


def test_public_plan_canonicalizes_explicit_disabled_compaction_like_the_go_receipt(tmp_path: Path) -> None:
    config = experiment_config(tmp_path, repeats=1)
    payload = json.loads(config.read_text(encoding="utf-8"))
    payload["policy"] = {"force_compaction": False}
    config.write_text(json.dumps(payload), encoding="utf-8")

    result = command("plan", "--config", str(config), "--output", str(tmp_path / "output"))

    assert result.returncode == 0, result.stderr
    plan = json.loads((tmp_path / "output/public-pilot/plan.json").read_text(encoding="utf-8"))
    assert "force_compaction" not in plan["policy"]


def test_public_run_refuses_changed_inputs_before_product_dispatch(tmp_path: Path) -> None:
    config = experiment_config(tmp_path, repeats=1)
    output = tmp_path / "output"
    assert command("plan", "--config", str(config), "--output", str(output)).returncode == 0
    original = (output / "public-pilot/plan.json").read_bytes()
    changed = json.loads(config.read_text(encoding="utf-8"))
    changed["tasks"][0]["message"] = "An unplanned task"
    config.write_text(json.dumps(changed), encoding="utf-8")

    result = command("run", "--config", str(config), "--output", str(output), "--agent-binary", sys.executable)

    assert result.returncode == 2
    assert "frozen plan" in result.stderr
    assert (output / "public-pilot/plan.json").read_bytes() == original
    assert not (output / "public-pilot/predictions.jsonl").exists()


def test_public_run_keeps_driver_failure_output_and_unrun_denominator(tmp_path: Path) -> None:
    config = experiment_config(tmp_path, repeats=1)
    second_root = tmp_path / "second"
    second_root.mkdir()
    second_config = experiment_config(second_root, repeats=1)
    payload = json.loads(config.read_text(encoding="utf-8"))
    second_task = json.loads(second_config.read_text(encoding="utf-8"))["tasks"][0]
    second_task["task_id"] = "addition-second"
    payload["tasks"].append(second_task)
    config.write_text(json.dumps(payload), encoding="utf-8")
    output = tmp_path / "output"
    assert command("plan", "--config", str(config), "--output", str(output)).returncode == 0

    result = command("run", "--config", str(config), "--output", str(output), "--agent-binary", sys.executable)

    assert result.returncode == 3, result.stderr
    summary = json.loads((output / "public-pilot/summary.json").read_text(encoding="utf-8"))
    assert summary["planned_trials"] == 2
    assert [row["status"] for row in summary["trials"]] == ["infra_error", "not_run"]
    assert all(row["success"] is None and row["cost"] is None for row in summary["trials"])
    stderr = output / "public-pilot/scorer/addition--000-driver-stderr.txt"
    assert "eval-trial" in stderr.read_text(encoding="utf-8")


def test_public_run_reports_failed_trials_without_claiming_real_model_evidence(tmp_path: Path) -> None:
    config = experiment_config(tmp_path, repeats=1)
    output = tmp_path / "output"
    assert command("plan", "--config", str(config), "--output", str(output)).returncode == 0

    result = command("run", "--config", str(config), "--output", str(output), "--agent-binary", sys.executable)

    assert result.returncode == 3, result.stderr
    report = json.loads((output / "public-pilot/report/summary.json").read_text(encoding="utf-8"))
    assert report["groups"][0]["planned_trials"] == 1
    assert report["groups"][0]["evidence_kind"] == "unknown"
    assert report["groups"][0]["successful_trials"] == 0
    assert report["provenance_verified"] is False
    assert (output / "public-pilot/report/trials.csv").is_file()


@pytest.mark.parametrize("checks", [
    {"schema_version": 0, "files": [{"path": "calculator.py", "contains": "sum"}]},
    {"schema_version": 1, "files": [{"path": "../outside.py", "contains": "sum"}]},
    {"schema_version": 1, "files": [{"path": "calculator.py", "command": "python calculator.py"}]},
])
def test_public_plan_refuses_invalid_hidden_checks_before_any_dispatch(tmp_path: Path, checks: dict) -> None:
    config = experiment_config(tmp_path, repeats=1)
    (tmp_path / "checks.json").write_text(json.dumps(checks), encoding="utf-8")

    result = command("plan", "--config", str(config), "--output", str(tmp_path / "output"))

    assert result.returncode == 2
    assert "hidden checks" in result.stderr
    assert not (tmp_path / "output/public-pilot").exists()


def test_public_context_plan_pairs_four_arms_without_losing_unrun_evidence(tmp_path: Path) -> None:
    config = experiment_config(tmp_path, repeats=2)
    payload = json.loads(config.read_text(encoding="utf-8"))
    payload.update(experiment="context_memory", evidence_kind="provider")
    payload["policy"] = {"frozen_memory": [{"session_id": "prior", "event_id": "experience", "checksum": "a" * 64}]}
    config.write_text(json.dumps(payload), encoding="utf-8")

    result = command("plan", "--config", str(config), "--output", str(tmp_path / "output"))

    assert result.returncode == 0, result.stderr
    plan = json.loads((tmp_path / "output/public-pilot/plan.json").read_text(encoding="utf-8"))
    assert plan["planned_trials"] == 8
    assert {(row["policy"]["context_strategy"], row["policy"]["memory_read"]) for row in plan["trials"]} == {
        ("retrieval_pre_read", False), ("retrieval_pre_read", True),
        ("metadata_on_demand", False), ("metadata_on_demand", True),
    }
    assert len({row["task_sha256"] for row in plan["trials"]}) == 1
    assert len({row["trial_id"] for row in plan["trials"]}) == 8
    assert all(row["status"] == "not_run" and row["evidence_kind"] == "provider" and row["provenance_verified"] is False for row in plan["trials"])
    assert all(row["budget_tokens"] == 4000 and row["policy"]["execution_strategy"] == "single" for row in plan["trials"])


def test_public_matrix_plan_freezes_unique_ids_within_the_go_trace_join_limit(tmp_path: Path) -> None:
    config = experiment_config(tmp_path, repeats=1)
    payload = json.loads(config.read_text(encoding="utf-8"))
    payload.update(run_id="a" * 80, experiment="context_memory")
    payload["tasks"][0]["task_id"] = "b" * 80
    config.write_text(json.dumps(payload), encoding="utf-8")

    result = command("plan", "--config", str(config), "--output", str(tmp_path / "output"))

    assert result.returncode == 0, result.stderr
    plan = json.loads((tmp_path / "output" / payload["run_id"] / "plan.json").read_text(encoding="utf-8"))
    assert len({row["trial_id"] for row in plan["trials"]}) == 4
    assert len({row["product_request_id"] for row in plan["trials"]}) == 4
    assert all(len(row["trial_id"]) <= 96 and len(row["product_request_id"]) <= 96 for row in plan["trials"])


def test_public_budget_plan_crosses_strategies_with_the_same_budget_points(tmp_path: Path) -> None:
    config = experiment_config(tmp_path, repeats=2)
    payload = json.loads(config.read_text(encoding="utf-8"))
    payload.update(experiment="budget", budget_points=[1000, 4000])
    config.write_text(json.dumps(payload), encoding="utf-8")

    result = command("plan", "--config", str(config), "--output", str(tmp_path / "output"))

    assert result.returncode == 0, result.stderr
    plan = json.loads((tmp_path / "output/public-pilot/plan.json").read_text(encoding="utf-8"))
    assert plan["planned_trials"] == 12
    assert {(row["arm"], row["budget_tokens"]) for row in plan["trials"]} == {
        (strategy, ceiling) for strategy in ("single", "verify_retry", "subagent") for ceiling in (1000, 4000)
    }
    assert len({row["trial_id"] for row in plan["trials"]}) == 12
    assert all(row["policy"]["execution_strategy"] == row["arm"] and row["budget"]["max_tokens"] == row["budget_tokens"] for row in plan["trials"])
    assert all(row["budget"]["wall_clock_seconds"] == 60 for row in plan["trials"])
    assert len({row["task_sha256"] for row in plan["trials"]}) == 1


def test_public_recovery_plan_keeps_fault_windows_and_controls_distinct(tmp_path: Path) -> None:
    config = experiment_config(tmp_path, repeats=1)
    payload = json.loads(config.read_text(encoding="utf-8"))
    payload["experiment"] = "recovery"
    config.write_text(json.dumps(payload), encoding="utf-8")

    result = command("plan", "--config", str(config), "--output", str(tmp_path / "output"))

    assert result.returncode == 0, result.stderr
    plan = json.loads((tmp_path / "output/public-pilot/plan.json").read_text(encoding="utf-8"))
    assert plan["planned_trials"] == 7
    assert {(row["recovery_mode"], row["failure_window"]) for row in plan["trials"]} == {
        ("none", None),
        *((mode, window) for mode in ("continue", "restart") for window in ("before_effect", "after_effect", "after_record")),
    }
    assert all(row["actually_interrupted"] is False and row["fault_event_count"] == 0 for row in plan["trials"])


def test_public_context_run_prepares_isolated_same_head_workspaces_for_all_cells(tmp_path: Path) -> None:
    config = experiment_config(tmp_path, repeats=2)
    payload = json.loads(config.read_text(encoding="utf-8"))
    payload.update(experiment="context_memory", evidence_kind="provider")
    config.write_text(json.dumps(payload), encoding="utf-8")
    output = tmp_path / "output"
    assert command("plan", "--config", str(config), "--output", str(output)).returncode == 0

    result = command("run", "--config", str(config), "--output", str(output), "--agent-binary", sys.executable)

    assert result.returncode == 3, result.stderr
    summary = json.loads((output / "public-pilot/summary.json").read_text(encoding="utf-8"))
    assert len(summary["trials"]) == 8
    workspaces = [output / "public-pilot" / row["workspace_relpath"] for row in summary["trials"]]
    assert len(set(workspaces)) == 8
    baseline = Path(payload["tasks"][0]["workspace"])
    baseline_head = subprocess.run(["git", "rev-parse", "HEAD"], cwd=baseline, check=True, capture_output=True, text=True).stdout.strip()
    for workspace in workspaces[:1]:
        assert workspace != baseline
        assert not (workspace / "checks.json").exists()
        assert subprocess.run(["git", "rev-parse", "HEAD"], cwd=workspace, check=True, capture_output=True, text=True).stdout.strip() == baseline_head
    assert all(not workspace.exists() for workspace in workspaces[1:])
    assert (baseline / "calculator.py").read_text(encoding="utf-8") == "def add(a, b):\n    return a - b\n"
    assert [row["status"] for row in summary["trials"]] == ["infra_error"] + ["not_run"] * 7
    assert all(row["evidence_kind"] == "provider" for row in summary["trials"])


@pytest.mark.parametrize("state", ["blocked", "infra_error"])
def test_public_run_preserves_pre_session_refusal_and_its_safe_diagnosis(tmp_path: Path, refusal_driver: Path, state: str) -> None:
    config = experiment_config(tmp_path, repeats=1)
    payload = json.loads(config.read_text(encoding="utf-8"))
    payload["evidence_kind"] = "fixture"
    payload["tasks"][0]["task_id"] = f"addition-{state}"
    config.write_text(json.dumps(payload), encoding="utf-8")
    output = tmp_path / "output"
    assert command("plan", "--config", str(config), "--output", str(output)).returncode == 0

    result = command("run", "--config", str(config), "--output", str(output), "--agent-binary", str(refusal_driver))

    assert result.returncode == 3, result.stderr
    summary = json.loads((output / "public-pilot/summary.json").read_text(encoding="utf-8"))
    row = summary["trials"][0]
    assert row["status"] == "infra_error"
    assert row["error_code"] == "orchestrator_startup_unavailable"
    assert row["stage"] == "orchestrator_startup"
    assert row["grpc_status"] == "Unavailable"
    assert row["success"] is None and row["provenance_verified"] is False
    receipt = json.loads((output / "public-pilot" / row["receipt_path"]).read_text(encoding="utf-8"))
    assert receipt["task_state"] == state and receipt["session_id"] == ""
    assert json.loads(result.stdout)["status"] == "BLOCKED"


@pytest.mark.parametrize("code", ["provider_model_identity_unconfirmed", "tool_outcome_unknown"])
def test_public_run_preserves_infrastructure_failure_after_a_model_dispatch(tmp_path: Path, refusal_driver: Path, monkeypatch, code: str) -> None:
    config = experiment_config(tmp_path, repeats=2)
    payload = json.loads(config.read_text(encoding="utf-8"))
    payload["tasks"][0]["task_id"] = "infrastructure-refusal"
    config.write_text(json.dumps(payload), encoding="utf-8")
    monkeypatch.setenv("CODEOPS_FIXTURE_INFRASTRUCTURE_CODE", code)
    output = tmp_path / "output"
    assert command("plan", "--config", str(config), "--output", str(output)).returncode == 0

    result = command("run", "--config", str(config), "--output", str(output), "--agent-binary", str(refusal_driver))

    assert result.returncode == 3, result.stderr
    rows = json.loads((output / "public-pilot/summary.json").read_text(encoding="utf-8"))["trials"]
    assert rows[0]["status"] == "infra_error" and rows[0]["success"] is None
    assert rows[0]["source_status"] == "failed" and rows[0]["error_code"] == code
    assert rows[0]["usage"]["call_count"] == 1 and rows[0]["tokens_in"] == 10
    assert rows[1]["status"] == "not_run" and rows[1]["success"] is None


def test_public_timeout_reaps_the_owned_process_tree_and_keeps_other_processes(tmp_path: Path, refusal_driver: Path) -> None:
    config = experiment_config(tmp_path, repeats=1)
    payload = json.loads(config.read_text(encoding="utf-8"))
    payload["tasks"][0]["task_id"] = "timeout"
    payload["budget"]["wall_clock_seconds"] = 0.01
    config.write_text(json.dumps(payload), encoding="utf-8")
    output = tmp_path / "output"
    assert command("plan", "--config", str(config), "--output", str(output)).returncode == 0
    unrelated = subprocess.Popen([str(refusal_driver), "--child"])
    child_pid = None
    child_stopped = False
    try:
        result = command("run", "--config", str(config), "--output", str(output), "--agent-binary", str(refusal_driver))
        assert result.returncode == 3, result.stderr
        raw = (output / "public-pilot/scorer/timeout--000-driver.ndjson").read_text(encoding="utf-8")
        child_pid = json.loads(raw.splitlines()[0])["child_pid"]
        assert unrelated.poll() is None
        if os.name == "nt":
            processes = subprocess.run(["tasklist", "/FI", f"PID eq {child_pid}", "/FO", "CSV", "/NH"], capture_output=True, text=True, encoding="utf-8", errors="replace", check=True).stdout
            assert f'"{child_pid}"' not in processes
        else:
            status = Path(f"/proc/{child_pid}/stat")
            assert not status.exists() or status.read_text().split(")", 1)[1].strip().startswith("Z")
        child_stopped = True
    finally:
        unrelated.kill()
        unrelated.wait(timeout=10)
        if child_pid is not None and not child_stopped:
            if os.name == "nt":
                subprocess.run(["taskkill", "/PID", str(child_pid), "/T", "/F"], capture_output=True, check=False)
            else:
                try:
                    os.kill(child_pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass


@pytest.mark.skipif(os.name == "nt", reason="POSIX process-group behavior requires a POSIX runtime")
def test_public_posix_cleanup_reaps_children_after_the_driver_has_exited(tmp_path: Path, refusal_driver: Path) -> None:
    config = experiment_config(tmp_path, repeats=1)
    payload = json.loads(config.read_text(encoding="utf-8"))
    payload["tasks"][0]["task_id"] = "exit-child"
    config.write_text(json.dumps(payload), encoding="utf-8")
    output = tmp_path / "output"
    assert command("plan", "--config", str(config), "--output", str(output)).returncode == 0
    child_pid = None
    stopped = False
    try:
        result = command("run", "--config", str(config), "--output", str(output), "--agent-binary", str(refusal_driver))
        assert result.returncode == 3, result.stderr
        rows = json.loads((output / "public-pilot/summary.json").read_text(encoding="utf-8"))["trials"]
        receipt = json.loads((output / "public-pilot" / rows[0]["receipt_path"]).read_text(encoding="utf-8"))
        child_pid = receipt["child_pid"]
        for _ in range(50):
            state = subprocess.run(["ps", "-p", str(child_pid), "-o", "stat="], capture_output=True, text=True, check=False).stdout.strip()
            if not state or state.startswith("Z"):
                stopped = True
                break
            time.sleep(0.02)
        assert stopped
    finally:
        if child_pid is not None and not stopped:
            try:
                os.kill(child_pid, signal.SIGKILL)
            except ProcessLookupError:
                pass


@pytest.mark.parametrize("proof_mode", ["expired_ready", "legacy_ready"])
def test_public_recovery_refuses_stale_or_legacy_readiness_before_fault_injection(tmp_path: Path, refusal_driver: Path, monkeypatch, proof_mode: str) -> None:
    config = experiment_config(tmp_path, repeats=1)
    payload = json.loads(config.read_text(encoding="utf-8"))
    payload.update(experiment="recovery", evidence_kind="fixture", failure_windows=["after_record"])
    payload["tasks"][0]["task_id"] = "recover"
    payload["budget"]["wall_clock_seconds"] = 60
    config.write_text(json.dumps(payload), encoding="utf-8")
    monkeypatch.setenv("CODEOPS_FIXTURE_FAULT_PROOF", proof_mode)
    output = tmp_path / "output"
    assert command("plan", "--config", str(config), "--output", str(output)).returncode == 0
    result = command("run", "--config", str(config), "--output", str(output), "--agent-binary", str(refusal_driver))
    assert result.returncode == 3, result.stderr
    rows = json.loads((output / "public-pilot/summary.json").read_text(encoding="utf-8"))["trials"]
    row = next(item for item in rows if item["recovery_mode"] == "continue")
    assert row["status"] == "infra_error" and row["success"] is None
    assert row["actually_interrupted"] is False and row["fault_event_count"] == 0
    assert "interruption_confirmation" not in row
    process = next((output / "public-pilot/processes").glob("*.json"))
    physical = json.loads(process.read_text(encoding="utf-8"))
    assert physical["physical_termination_confirmed"] is False and physical["confirmed"] is False


@pytest.mark.parametrize("proof_mode", ["released", "canceled", "terminal", "wrong_reference", "unordered"])
def test_public_recovery_does_not_count_unconfirmed_fault_boundaries(tmp_path: Path, refusal_driver: Path, monkeypatch, proof_mode: str) -> None:
    config = experiment_config(tmp_path, repeats=1)
    payload = json.loads(config.read_text(encoding="utf-8"))
    payload.update(experiment="recovery", evidence_kind="fixture", failure_windows=["after_record"])
    payload["tasks"][0]["task_id"] = "recover"
    payload["budget"]["wall_clock_seconds"] = 60
    config.write_text(json.dumps(payload), encoding="utf-8")
    monkeypatch.setenv("CODEOPS_FIXTURE_FAULT_PROOF", proof_mode)
    output = tmp_path / "output"
    assert command("plan", "--config", str(config), "--output", str(output)).returncode == 0
    result = command("run", "--config", str(config), "--output", str(output), "--agent-binary", str(refusal_driver))
    assert result.returncode == 3, result.stderr
    rows = json.loads((output / "public-pilot/summary.json").read_text(encoding="utf-8"))["trials"]
    row = next(item for item in rows if item["recovery_mode"] == "continue")
    assert row["status"] == "infra_error" and row["success"] is None and row["recovered"] is None
    assert row["error_code"] == "fault_boundary_unconfirmed"
    assert row["physical_termination_confirmed"] is True
    assert row["actually_interrupted"] is None and row["fault_event_count"] is None
    physical = json.loads((output / "public-pilot" / row["interruption_record"]).read_text(encoding="utf-8"))
    assert physical["physical_termination_confirmed"] is True and physical["confirmed"] is False
    assert "interruption_confirmation" not in row
    assert rows[-1]["status"] == "not_run"


@pytest.mark.parametrize("abandon_state", ["known", "unknown_usage", "refused", "missing_restart_usage", "continue_failure", "multi_turn", "wrong_run"])
def test_public_recovery_confirms_faults_then_compares_same_session_and_from_start(tmp_path: Path, refusal_driver: Path, monkeypatch, abandon_state: str) -> None:
    config = experiment_config(tmp_path, repeats=1)
    payload = json.loads(config.read_text(encoding="utf-8"))
    payload.update(experiment="recovery", evidence_kind="fixture", failure_windows=["after_record"])
    payload["tasks"][0]["task_id"] = "recover"
    if abandon_state == "multi_turn":
        task = payload["tasks"][0]
        task.pop("message")
        task.update(messages=["Use the old rule.", "Replace the old rule with the current rule."], min_root_compactions=1)
        (tmp_path / "checks.json").write_text(json.dumps({"schema_version": 1, "files": [{"path": "current-rule.json", "json_equals": {"active": "Replace the old rule with the current rule."}}]}), encoding="utf-8")
    payload["budget"]["wall_clock_seconds"] = 60
    config.write_text(json.dumps(payload), encoding="utf-8")
    protocol = tmp_path / "protocol.ndjson"
    monkeypatch.setenv("CODEOPS_FIXTURE_PROTOCOL_LOG", str(protocol))
    monkeypatch.setenv("CODEOPS_FIXTURE_ABANDON", abandon_state)
    output = tmp_path / "output"
    assert command("plan", "--config", str(config), "--output", str(output)).returncode == 0

    result = command("run", "--config", str(config), "--output", str(output), "--agent-binary", str(refusal_driver))

    assert result.returncode == (0 if abandon_state in {"known", "multi_turn"} else 3), result.stderr
    summary = json.loads((output / "public-pilot/summary.json").read_text(encoding="utf-8"))
    continued = next(row for row in summary["trials"] if row["recovery_mode"] == "continue")
    if abandon_state == "wrong_run":
        assert continued["actually_interrupted"] is None
        assert continued["fault_event_count"] is None
        assert continued["status"] == "infra_error" and continued["success"] is None
        assert continued["error_code"] == "interrupted_run_changed"
        assert continued["recovered"] is None
        assert summary["trials"][-1]["status"] == "not_run"
        return
    assert continued["actually_interrupted"] is True
    assert continued["fault_event_count"] == 1
    assert continued["recovered"] is (abandon_state != "continue_failure")
    assert continued["success"] is (abandon_state != "continue_failure")
    assert continued["provenance_verified"] is False and continued["evidence_kind"] == "fixture"
    directory = output / "public-pilot"
    termination = json.loads((directory / continued["interruption_record"]).read_text(encoding="utf-8"))
    assert termination["physical_termination_confirmed"] is True
    assert termination["confirmed"] is False
    confirmation = json.loads((directory / continued["interruption_confirmation"]).read_text(encoding="utf-8"))
    assert confirmation["confirmed"] is True
    receipt = json.loads((directory / continued["receipt_path"]).read_text(encoding="utf-8"))
    assert termination["fault_ready"]["session_id"] == receipt["session_id"]
    assert (directory / continued["workspace_relpath"] / "calculator.py").read_text(encoding="utf-8") == "def add(a, b):\n    return a + b\n"
    restarted = next(row for row in summary["trials"] if row["recovery_mode"] == "restart")
    requests = [json.loads(line) for line in protocol.read_text(encoding="utf-8").splitlines()]
    abandoned = next(index for index, request in enumerate(requests) if request.get("finalize_mode") == "abandon")
    if abandon_state in {"unknown_usage", "refused"}:
        assert restarted["status"] == "infra_error" and restarted["success"] is None
        assert restarted["tokens_in"] is None and restarted["cost"] is None
        assert restarted["error_code"] == ("restart_budget_unknown" if abandon_state == "unknown_usage" else "tool_outcome_unknown")
        assert len(requests) == abandoned + 1
        assert not (directory / "restarts").exists()
        if abandon_state == "unknown_usage":
            assert restarted["interrupted_usage"]["unknown_calls"] == 1
            assert restarted["interrupted_usage"]["reserved_tokens"] == 4000
        return
    if abandon_state == "missing_restart_usage":
        assert restarted["status"] == "infra_error" and restarted["success"] is None
        assert restarted["tokens_in"] is None and restarted["provenance_verified"] is False
        assert restarted["error_code"] == "invalid_usage_receipt"
        receipt = json.loads((directory / restarted["receipt_path"]).read_text(encoding="utf-8"))
        assert receipt["usage"]["state"] == "known" and "input_tokens" not in receipt["usage"]
        return
    assert restarted["success"] is True and restarted["completed_from_start"] is True
    assert restarted["recovered"] is None and restarted["actually_interrupted"] is True
    assert restarted["original_session_id"] != restarted["session_id"]
    assert restarted["tokens_in"] == (30 if abandon_state == "multi_turn" else 20)
    assert restarted["tokens_out"] == (6 if abandon_state == "multi_turn" else 4)
    if abandon_state == "multi_turn":
        assert all(row["completed_turns"] == 2 and row["root_compaction_count"] == 1 for row in summary["trials"])
    original = directory / restarted["original_workspace_relpath"]
    assert (original / "calculator.py").read_text(encoding="utf-8") == "def add(a, b):\n    return a - b\n"
    assert (directory / restarted["workspace_relpath"] / "calculator.py").read_text(encoding="utf-8") == "def add(a, b):\n    return a + b\n"
    fresh = requests[abandoned + 1]
    if abandon_state == "multi_turn":
        assert fresh["lifecycle"] == "hold" and fresh["turn_id"] == "turn-000"
    assert fresh["operation"] == "run" and "session_id" not in fresh
    assert fresh["run_id"] != requests[abandoned]["run_id"]
    assert fresh["budget"]["max_tokens"] == 3988
    assert "fault_window" not in fresh["policy"]
    assert requests[abandoned]["expected_session_head_checksum"] == "a" * 64
    validation = json.loads((directory / "receipt-validation.json").read_text(encoding="utf-8"))
    restart_validation = next(item for item in validation["trials"] if item["trial_id"] == restarted["trial_id"])
    assert restart_validation["checks"]["interrupted_session_abandoned"] is True
    assert (directory / restart_validation["abandon_receipt"]["path"]).is_file()


def test_public_final_scorer_distinguishes_json_booleans_from_numbers(tmp_path: Path, refusal_driver: Path) -> None:
    config = experiment_config(tmp_path, repeats=1)
    payload = json.loads(config.read_text(encoding="utf-8"))
    payload["tasks"][0].update(task_id="json-bool", message="Set settings.json schema_version to the number 1.")
    payload["evidence_kind"] = "fixture"
    config.write_text(json.dumps(payload), encoding="utf-8")
    (tmp_path / "checks.json").write_text(json.dumps({"schema_version": 1, "files": [{"path": "settings.json", "json_equals": {"schema_version": 1}}]}), encoding="utf-8")
    output = tmp_path / "output"
    assert command("plan", "--config", str(config), "--output", str(output)).returncode == 0

    result = command("run", "--config", str(config), "--output", str(output), "--agent-binary", str(refusal_driver))

    assert result.returncode == 3, result.stderr
    summary = json.loads((output / "public-pilot/summary.json").read_text(encoding="utf-8"))
    assert summary["trials"][0]["source_status"] == "completed"
    assert summary["trials"][0]["success"] is False
    assert json.loads(result.stdout)["status"] == "FAIL"


@pytest.mark.parametrize("changed", [False, True])
def test_public_final_scorer_checks_unchanged_file_hashes(tmp_path: Path, refusal_driver: Path, changed: bool) -> None:
    config = experiment_config(tmp_path, repeats=1)
    payload = json.loads(config.read_text(encoding="utf-8"))
    workspace = Path(payload["tasks"][0]["workspace"])
    instructions = b"Keep repository changes narrow.\n"
    (workspace / "AGENTS.md").write_bytes(instructions)
    for arguments in (["git", "add", "AGENTS.md"], ["git", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--quiet", "-m", "instructions"]):
        subprocess.run(arguments, cwd=workspace, capture_output=True, check=True)
    payload["tasks"][0]["task_id"] = "corrupt-agents" if changed else "recover"
    payload["evidence_kind"] = "fixture"
    config.write_text(json.dumps(payload), encoding="utf-8")
    (tmp_path / "checks.json").write_text(json.dumps({"schema_version": 1, "files": [
        {"path": "calculator.py", "contains": "return a + b"},
        {"path": "AGENTS.md", "sha256": hashlib.sha256(instructions).hexdigest()},
    ]}), encoding="utf-8")
    output = tmp_path / "output"
    planned = command("plan", "--config", str(config), "--output", str(output))
    assert planned.returncode == 0, planned.stderr

    result = command("run", "--config", str(config), "--output", str(output), "--agent-binary", str(refusal_driver))

    assert result.returncode == (3 if changed else 0), result.stderr
    row = json.loads((output / "public-pilot/summary.json").read_text(encoding="utf-8"))["trials"][0]
    assert row["success"] is (not changed)


@pytest.mark.parametrize("target", ["config", "next_checks"])
def test_public_run_stops_on_pin_changes_even_after_a_failed_attempt(tmp_path: Path, refusal_driver: Path, monkeypatch, target: str) -> None:
    config = experiment_config(tmp_path, repeats=1)
    second_root = tmp_path / "second"
    second_root.mkdir()
    second_config = experiment_config(second_root, repeats=1)
    payload = json.loads(config.read_text(encoding="utf-8"))
    payload["tasks"][0]["task_id"] = "mutate-next"
    second_task = json.loads(second_config.read_text(encoding="utf-8"))["tasks"][0]
    second_task["task_id"] = "recover"
    payload["tasks"].append(second_task)
    payload["evidence_kind"] = "fixture"
    config.write_text(json.dumps(payload), encoding="utf-8")
    dispatch_log = tmp_path / "dispatch.log"
    monkeypatch.setenv("CODEOPS_FIXTURE_DISPATCH_LOG", str(dispatch_log))
    monkeypatch.setenv("CODEOPS_FIXTURE_MUTATE_PATH", str(config if target == "config" else second_task["checks_file"]))
    output = tmp_path / "output"
    assert command("plan", "--config", str(config), "--output", str(output)).returncode == 0

    result = command("run", "--config", str(config), "--output", str(output), "--agent-binary", str(refusal_driver))

    assert result.returncode == 3, result.stderr
    rows = json.loads((output / "public-pilot/summary.json").read_text(encoding="utf-8"))["trials"]
    assert [row["status"] for row in rows] == ["infra_error", "not_run"]
    assert rows[0]["error_code"] == "frozen_pins_changed"
    assert dispatch_log.read_text(encoding="utf-8").splitlines() == ["mutate-next"]


def test_public_clone_failure_keeps_the_full_matrix_and_finalizes_checksums(tmp_path: Path, refusal_driver: Path, monkeypatch) -> None:
    config = experiment_config(tmp_path, repeats=1)
    payload = json.loads(config.read_text(encoding="utf-8"))
    payload.update(experiment="context_memory", evidence_kind="fixture")
    config.write_text(json.dumps(payload), encoding="utf-8")
    output = tmp_path / "output"
    assert command("plan", "--config", str(config), "--output", str(output)).returncode == 0
    real_git = shutil.which("git")
    assert real_git is not None
    shim = tmp_path / "git-bin"
    shim.mkdir()
    shutil.copy2(refusal_driver, shim / ("git.exe" if os.name == "nt" else "git"))
    monkeypatch.setenv("CODEOPS_REAL_GIT", real_git)
    monkeypatch.setenv("PATH", str(shim) + os.pathsep + os.environ["PATH"])

    result = command("run", "--config", str(config), "--output", str(output), "--agent-binary", str(refusal_driver))

    assert result.returncode == 3, result.stderr
    directory = output / "public-pilot"
    summary = json.loads((directory / "summary.json").read_text(encoding="utf-8"))
    assert summary["planned_trials"] == 4
    assert [row["status"] for row in summary["trials"]] == ["infra_error", "not_run", "not_run", "not_run"]
    assert len((directory / "failures.jsonl").read_text(encoding="utf-8").splitlines()) == 1
    for line in (directory / "checksums.sha256").read_text(encoding="utf-8").splitlines():
        expected, relative = line.split("  ", 1)
        assert hashlib.sha256((directory / relative).read_bytes()).hexdigest() == expected


@pytest.mark.parametrize("minimum,exit_code", [(1, 0), (2, 3)])
def test_public_multiturn_sequence_keeps_one_session_and_finalizes_before_scoring(tmp_path: Path, refusal_driver: Path, monkeypatch, minimum: int, exit_code: int) -> None:
    config = experiment_config(tmp_path, repeats=1)
    payload = json.loads(config.read_text(encoding="utf-8"))
    task = payload["tasks"][0]
    task.pop("message")
    task.update(task_id="sequence", messages=["Use the old rule.", "Replace the old rule with the current rule."], min_root_compactions=minimum)
    payload["evidence_kind"] = "fixture"
    config.write_text(json.dumps(payload), encoding="utf-8")
    (tmp_path / "checks.json").write_text(json.dumps({"schema_version": 1, "files": [{"path": "current-rule.json", "json_equals": {"active": "Replace the old rule with the current rule."}}]}), encoding="utf-8")
    protocol = tmp_path / "protocol.ndjson"
    monkeypatch.setenv("CODEOPS_FIXTURE_PROTOCOL_LOG", str(protocol))
    output = tmp_path / "output"
    planned = command("plan", "--config", str(config), "--output", str(output))
    assert planned.returncode == 0, planned.stderr

    result = command("run", "--config", str(config), "--output", str(output), "--agent-binary", str(refusal_driver))

    assert result.returncode == exit_code, result.stderr
    requests = [json.loads(line) for line in protocol.read_text(encoding="utf-8").splitlines()]
    assert [request["operation"] for request in requests] == ["run", "run", "finalize", "inspect"]
    assert [request["turn_id"] for request in requests[:2]] == ["turn-000", "turn-001"]
    assert "session_id" not in requests[0]
    assert all(request["session_id"] == requests[1]["session_id"] for request in requests[1:])
    assert requests[2]["finalize_mode"] == "finish"
    root = output / "public-pilot"
    row = json.loads((root / "summary.json").read_text(encoding="utf-8"))["trials"][0]
    if exit_code:
        assert row["status"] == "infra_error" and row["success"] is None
        assert row["error_code"] == "required_compaction_not_observed"
        return
    assert row["success"] is True and row["completed_turns"] == 2 and row["planned_turns"] == 2
    assert row["tokens_in"] == 20 and row["tokens_out"] == 4
    assert row["root_compaction_count"] == 1 and row["duplicate_effects"] is None
    validation = json.loads((root / "receipt-validation.json").read_text(encoding="utf-8"))["trials"][0]
    assert len(validation["turn_receipts"]) == 2
    assert validation["checks"]["admission_finalized"] is True
    assert (root / validation["finalization_receipt"]["path"]).is_file()


@pytest.mark.parametrize("observations", ["known", "retry_performed", "missing_verification", "missing_child", "missing_bound", "extra_retry"])
def test_public_budget_requires_observed_execution_for_each_strategy(tmp_path: Path, refusal_driver: Path, monkeypatch, observations: str) -> None:
    config = experiment_config(tmp_path, repeats=1)
    payload = json.loads(config.read_text(encoding="utf-8"))
    payload.update(experiment="budget", evidence_kind="fixture", budget_points=[4000, 8000])
    payload["tasks"][0]["task_id"] = "recover"
    config.write_text(json.dumps(payload), encoding="utf-8")
    monkeypatch.setenv("CODEOPS_FIXTURE_STRATEGY", observations)
    output = tmp_path / "output"
    assert command("plan", "--config", str(config), "--output", str(output)).returncode == 0

    result = command("run", "--config", str(config), "--output", str(output), "--agent-binary", str(refusal_driver))

    valid = observations in {"known", "retry_performed"}
    assert result.returncode == (0 if valid else 3), result.stderr
    rows = json.loads((output / "public-pilot/summary.json").read_text(encoding="utf-8"))["trials"]
    assert len(rows) == 6
    if valid:
        assert all(row["success"] is True for row in rows)
        for row in rows:
            if row["arm"] == "verify_retry":
                retry = row["strategy_evidence"]["retry_evidence"]
                assert retry["attempt_count"] == (2 if observations == "retry_performed" else 1)
                assert retry["retry_observed"] is (observations == "retry_performed")
    else:
        failure_index = 4 if observations == "missing_child" else 2
        failed = rows[failure_index]
        assert failed["status"] == "infra_error" and failed["success"] is None
        assert failed["error_code"] == "execution_strategy_not_observed"
        assert all(row["status"] == "not_run" for row in rows[failure_index + 1:])
        assert failed["source_status"] == "completed" and failed["provenance_verified"] is False


@pytest.mark.parametrize("missing", ["strategy", "compaction"])
def test_public_production_finalizes_a_completed_candidate_when_required_execution_was_not_observed(tmp_path: Path, refusal_driver: Path, monkeypatch, missing: str) -> None:
    config = experiment_config(tmp_path, repeats=2)
    payload = json.loads(config.read_text(encoding="utf-8"))
    approved = tmp_path / "approved-runtime"
    approved.mkdir()
    payload.update(runtime_contract="production_opensandbox", approved_workspace_root=str(approved), evidence_kind="fixture")
    payload["tasks"][0]["task_id"] = "production"
    if missing == "strategy":
        payload.update(experiment="budget", budget_points=[4000, 8000])
    else:
        payload["tasks"][0]["min_root_compactions"] = 1
    config.write_text(json.dumps(payload), encoding="utf-8")
    monkeypatch.setenv("CODEOPS_FIXTURE_APPROVED_ROOT", str(approved))
    monkeypatch.setenv("CODEOPS_FIXTURE_PRODUCTION", "graded_pass")
    protocol = tmp_path / "protocol.ndjson"
    monkeypatch.setenv("CODEOPS_FIXTURE_PROTOCOL_LOG", str(protocol))
    output = tmp_path / "output"
    assert command("plan", "--config", str(config), "--output", str(output)).returncode == 0

    result = command("run", "--config", str(config), "--output", str(output), "--agent-binary", str(refusal_driver))

    assert result.returncode == 3, result.stderr
    directory = output / "public-pilot"
    rows = json.loads((directory / "summary.json").read_text(encoding="utf-8"))["trials"]
    row = rows[0]
    assert row["status"] == "infra_error" and row["success"] is None
    assert row["source_status"] == "completed" and row["ledger_valid"] is True
    assert row["error_code"] == ("execution_strategy_not_observed" if missing == "strategy" else "required_compaction_not_observed")
    assert row["tokens_in"] == 10 and row["tokens_out"] == 2
    assert row.get("admission_finalized") is True and not row.get("cleanup_blocked", False)
    assert all(later["status"] == "not_run" for later in rows[1:])
    requests = [json.loads(line) for line in protocol.read_text(encoding="utf-8").splitlines()]
    assert [request["operation"] for request in requests] == ["run", "inspect", "finalize", "inspect"]
    assert not (directory / f"trials/{row['trial_id']}/final-score.json").exists()


@pytest.mark.parametrize("mode", ["good", "wrong_contract", "workspace_escape", "fake_workspace_id", "unknown_stop", "missing_runtime", "source_mutation", "bad_event_ref"])
def test_public_production_runtime_requires_owned_workspace_and_confirmed_osb_evidence(tmp_path: Path, refusal_driver: Path, monkeypatch, mode: str) -> None:
    config = experiment_config(tmp_path, repeats=2)
    payload = json.loads(config.read_text(encoding="utf-8"))
    approved = tmp_path / "approved-runtime"
    approved.mkdir()
    payload.update(runtime_contract="production_opensandbox", approved_workspace_root=str(approved), evidence_kind="fixture")
    payload["tasks"][0]["task_id"] = "production"
    config.write_text(json.dumps(payload), encoding="utf-8")
    monkeypatch.setenv("CODEOPS_FIXTURE_APPROVED_ROOT", str(approved))
    monkeypatch.setenv("CODEOPS_FIXTURE_PRODUCTION", mode)
    protocol = tmp_path / "protocol.ndjson"
    monkeypatch.setenv("CODEOPS_FIXTURE_PROTOCOL_LOG", str(protocol))
    output = tmp_path / "output"
    assert command("plan", "--config", str(config), "--output", str(output)).returncode == 0

    result = command("run", "--config", str(config), "--output", str(output), "--agent-binary", str(refusal_driver))

    assert result.returncode == 3, result.stderr
    directory = output / "public-pilot"
    rows = json.loads((directory / "summary.json").read_text(encoding="utf-8"))["trials"]
    row = rows[0]
    assert row["success"] is None and row["provenance_verified"] is False
    assert rows[1]["status"] == "not_run"
    if mode == "good":
        assert row["status"] == "scorer_error" and row["error_code"] == "production_grader_unavailable"
        assert row["execution_workspace_sha256"] == hashlib.sha256(str((approved / "fixture-task").resolve()).encode("utf-8")).hexdigest()
        validation = json.loads((directory / "receipt-validation.json").read_text(encoding="utf-8"))["trials"][0]
        assert validation["checks"]["production_workspace_owned"] is True
        assert validation["checks"]["opensandbox_execution_confirmed"] is True
        assert not (directory / f"trials/{row['trial_id']}/final-score.json").exists()
    else:
        assert row["status"] == "infra_error"
        assert row["error_code"] == ("frozen_pins_changed" if mode == "source_mutation" else "production_runtime_invalid")
    requests = [json.loads(line) for line in protocol.read_text(encoding="utf-8").splitlines()]
    assert all(request["runtime_contract"] == "production_opensandbox" for request in requests)
    if mode == "good":
        assert [request["operation"] for request in requests] == ["run", "inspect", "grade", "finalize", "inspect"]
        assert all(request.get("lifecycle") == "hold" for request in requests)
        assert requests[-2]["finalize_mode"] == "finish"
        assert validation["checks"]["production_finalized_after_grading"] is True
    assert str(tmp_path / "checks.json") not in json.dumps([request for request in requests if request["operation"] in {"run", "continue"}])
    assert (Path(payload["tasks"][0]["workspace"]) / "calculator.py").read_text(encoding="utf-8") == "def add(a, b):\n    return a - b\n"


@pytest.mark.parametrize(("state", "valid"), [("blocked", True), ("infra_error", True), ("infra_error", False)])
def test_public_production_keeps_post_prepare_refusal_identity_without_a_run(tmp_path: Path, refusal_driver: Path, monkeypatch, state: str, valid: bool) -> None:
    config = experiment_config(tmp_path, repeats=2)
    payload = json.loads(config.read_text(encoding="utf-8"))
    approved = tmp_path / "approved-runtime"
    approved.mkdir()
    payload.update(runtime_contract="production_opensandbox", approved_workspace_root=str(approved), evidence_kind="fixture")
    payload["tasks"][0]["task_id"] = "production"
    config.write_text(json.dumps(payload), encoding="utf-8")
    monkeypatch.setenv("CODEOPS_FIXTURE_APPROVED_ROOT", str(approved))
    monkeypatch.setenv("CODEOPS_FIXTURE_PRODUCTION", "post_prepare_valid" if valid else "post_prepare_bad_proof")
    monkeypatch.setenv("CODEOPS_FIXTURE_PREPARED_STATE", state)
    protocol = tmp_path / "protocol.ndjson"
    monkeypatch.setenv("CODEOPS_FIXTURE_PROTOCOL_LOG", str(protocol))
    output = tmp_path / "output"
    assert command("plan", "--config", str(config), "--output", str(output)).returncode == 0

    result = command("run", "--config", str(config), "--output", str(output), "--agent-binary", str(refusal_driver))

    assert result.returncode == 3, result.stderr
    directory = output / "public-pilot"
    rows = json.loads((directory / "summary.json").read_text(encoding="utf-8"))["trials"]
    row = rows[0]
    assert row["status"] == "infra_error" and row["success"] is None and row["provenance_verified"] is False
    assert rows[1]["status"] == "not_run" and rows[1]["success"] is None
    if valid:
        assert row["source_status"] == state and row["error_code"] == "production_workspace_integrity_unconfirmed"
        assert row["session_id"] == "fixture-production-session" and row["product_run_id"] == "" and not row.get("trace_id")
        assert row["stage"] == "workspace_admission"
        assert row["usage"]["call_count"] == 0 and row["usage"]["state"] == "unknown"
        assert row["tokens_in"] is None and row["tokens_out"] is None
        receipt = json.loads((directory / row["receipt_path"]).read_text(encoding="utf-8"))
        assert receipt["task_workspace"]["verified"] is True and receipt["task_workspace"]["admission_ready"] is False
    else:
        assert row["error_code"] == "production_runtime_invalid"
    requests = [json.loads(line) for line in protocol.read_text(encoding="utf-8").splitlines()]
    assert [request["operation"] for request in requests] == ["run"]
    assert not (directory / f"trials/{row['trial_id']}/final-score.json").exists()


@pytest.mark.parametrize("mode", ["exit", "timeout", "malformed_stdout", "invalid_final_identity"])
def test_public_production_recovers_owned_started_identity_when_final_receipt_is_missing(tmp_path: Path, refusal_driver: Path, monkeypatch, mode: str) -> None:
    config = experiment_config(tmp_path, repeats=2)
    payload = json.loads(config.read_text(encoding="utf-8"))
    approved = tmp_path / "approved-runtime"
    approved.mkdir()
    payload.update(runtime_contract="production_opensandbox", approved_workspace_root=str(approved), evidence_kind="fixture")
    if mode == "timeout":
        payload["budget"]["wall_clock_seconds"] = 0.01
    payload["tasks"][0]["task_id"] = "production"
    config.write_text(json.dumps(payload), encoding="utf-8")
    monkeypatch.setenv("CODEOPS_FIXTURE_APPROVED_ROOT", str(approved))
    monkeypatch.setenv("CODEOPS_FIXTURE_PRODUCTION", f"missing_receipt_{mode}")
    protocol = tmp_path / "protocol.ndjson"
    monkeypatch.setenv("CODEOPS_FIXTURE_PROTOCOL_LOG", str(protocol))
    output = tmp_path / "output"
    assert command("plan", "--config", str(config), "--output", str(output)).returncode == 0

    result = command("run", "--config", str(config), "--output", str(output), "--agent-binary", str(refusal_driver))

    assert result.returncode == 3, result.stderr
    directory = output / "public-pilot"
    rows = json.loads((directory / "summary.json").read_text(encoding="utf-8"))["trials"]
    row = rows[0]
    assert row["status"] == "infra_error" and row["success"] is None and row["provenance_verified"] is False
    if mode in {"malformed_stdout", "invalid_final_identity"}:
        assert row["error_code"] == ("JSONDecodeError" if mode == "malformed_stdout" else "ValueError")
    assert row.get("session_id") == "fixture-production-session" and row.get("product_run_id") == "fixture-product-run"
    assert row.get("admission_finalized") is True and not row.get("cleanup_blocked", False)
    assert row["usage"]["state"] == "unknown" and row["usage"]["unknown_calls"] == 1 and row["tokens_in"] is None
    assert rows[1]["status"] == "not_run" and rows[1]["success"] is None
    process = json.loads((directory / f"processes/{row['trial_id']}-turn-000.json").read_text(encoding="utf-8"))
    assert process["started"]["session_id"] == row["session_id"] and process["started"]["pid"] == process["pid"]
    assert process["owned_tree_stopped"] is True and process["timed_out"] is (mode == "timeout")
    child_pid = process["started"]["child_pid"]
    if os.name == "nt":
        processes = subprocess.run(["tasklist", "/FI", f"PID eq {child_pid}", "/FO", "CSV", "/NH"], capture_output=True, text=True, encoding="utf-8", errors="replace", check=True).stdout
        assert f'"{child_pid}"' not in processes
    else:
        status = Path(f"/proc/{child_pid}/stat")
        assert not status.exists() or status.read_text().split(")", 1)[1].strip().startswith("Z")
    assert not (directory / f"trials/{row['trial_id']}/product-receipt.json").exists()
    assert not (directory / f"trials/{row['trial_id']}/final-score.json").exists()
    requests = [json.loads(line) for line in protocol.read_text(encoding="utf-8").splitlines()]
    assert [request["operation"] for request in requests] == ["run", "inspect", "finalize", "inspect"]
    assert requests[2]["finalize_mode"] == "abandon"
    validation = json.loads((directory / "receipt-validation.json").read_text(encoding="utf-8"))["trials"][0]
    assert validation["checks"]["started_ledger_readback"] is True and validation["checks"]["finalized_ledger_readback"] is True
    final_inspected = json.loads((directory / validation["final_inspect_receipt"]["path"]).read_text(encoding="utf-8"))
    assert final_inspected["task_state"] == "finalized" and final_inspected["admission_finalized"] is True and final_inspected["run_state"] == "canceling"


@pytest.mark.parametrize(("mode", "operations", "error_code"), [
    ("bad_pid", ["run"], "product_started_identity_unavailable"),
    ("bad_runtime", ["run"], "product_started_identity_unavailable"),
    ("inspect_wrong_run", ["run", "inspect"], "started_ledger_readback_unconfirmed"),
    ("abandon_refused", ["run", "inspect", "finalize"], "tool_outcome_unknown"),
    ("stale_final_inspect", ["run", "inspect", "finalize", "inspect"], "finalized_ledger_readback_unconfirmed"),
])
def test_public_production_blocks_missing_receipt_cleanup_without_bound_canonical_proof(tmp_path: Path, refusal_driver: Path, monkeypatch, mode: str, operations: list[str], error_code: str) -> None:
    config = experiment_config(tmp_path, repeats=2)
    payload = json.loads(config.read_text(encoding="utf-8"))
    approved = tmp_path / "approved-runtime"
    approved.mkdir()
    payload.update(runtime_contract="production_opensandbox", approved_workspace_root=str(approved), evidence_kind="fixture")
    payload["tasks"][0]["task_id"] = "production"
    config.write_text(json.dumps(payload), encoding="utf-8")
    monkeypatch.setenv("CODEOPS_FIXTURE_APPROVED_ROOT", str(approved))
    monkeypatch.setenv("CODEOPS_FIXTURE_PRODUCTION", f"missing_receipt_{mode}")
    protocol = tmp_path / "protocol.ndjson"
    monkeypatch.setenv("CODEOPS_FIXTURE_PROTOCOL_LOG", str(protocol))
    output = tmp_path / "output"
    assert command("plan", "--config", str(config), "--output", str(output)).returncode == 0

    result = command("run", "--config", str(config), "--output", str(output), "--agent-binary", str(refusal_driver))

    assert result.returncode == 3, result.stderr
    directory = output / "public-pilot"
    rows = json.loads((directory / "summary.json").read_text(encoding="utf-8"))["trials"]
    row = rows[0]
    assert row["status"] == "infra_error" and row["success"] is None and row["provenance_verified"] is False
    assert row.get("cleanup_blocked") is True and row["cleanup_error"]["error_code"] == error_code
    assert row.get("admission_finalized") is not True and rows[1]["status"] == "not_run"
    assert not (directory / f"trials/{row['trial_id']}/product-receipt.json").exists()
    assert not (directory / f"trials/{row['trial_id']}/final-score.json").exists()
    requests = [json.loads(line) for line in protocol.read_text(encoding="utf-8").splitlines()]
    assert [request["operation"] for request in requests] == operations
    process = json.loads((directory / f"processes/{row['trial_id']}-turn-000.json").read_text(encoding="utf-8"))
    assert process["owned_tree_stopped"] is True
    assert (process["started"] is None) is (mode in {"bad_pid", "bad_runtime"})


def test_public_production_finalizes_a_known_model_failure_without_grading_it(tmp_path: Path, refusal_driver: Path, monkeypatch) -> None:
    config = experiment_config(tmp_path, repeats=1)
    payload = json.loads(config.read_text(encoding="utf-8"))
    approved = tmp_path / "approved-runtime"
    approved.mkdir()
    payload.update(runtime_contract="production_opensandbox", approved_workspace_root=str(approved), evidence_kind="fixture")
    payload["tasks"][0]["task_id"] = "production"
    config.write_text(json.dumps(payload), encoding="utf-8")
    monkeypatch.setenv("CODEOPS_FIXTURE_APPROVED_ROOT", str(approved))
    monkeypatch.setenv("CODEOPS_FIXTURE_PRODUCTION", "model_failed")
    protocol = tmp_path / "protocol.ndjson"
    monkeypatch.setenv("CODEOPS_FIXTURE_PROTOCOL_LOG", str(protocol))
    output = tmp_path / "output"
    assert command("plan", "--config", str(config), "--output", str(output)).returncode == 0

    result = command("run", "--config", str(config), "--output", str(output), "--agent-binary", str(refusal_driver))

    assert result.returncode == 3, result.stderr
    directory = output / "public-pilot"
    summary = json.loads((directory / "summary.json").read_text(encoding="utf-8"))
    row = summary["trials"][0]
    assert row["status"] == "agent_failure" and row["success"] is False
    assert row["source_status"] == "failed" and row["error_code"] == "fixture_agent_failure"
    assert row.get("admission_finalized") is True
    assert row["tokens_in"] == 10 and row["tokens_out"] == 2
    requests = [json.loads(line) for line in protocol.read_text(encoding="utf-8").splitlines()]
    assert [request["operation"] for request in requests] == ["run", "finalize", "inspect"]
    assert not (directory / f"trials/{row['trial_id']}/final-score.json").exists()


@pytest.mark.parametrize("code,attempted", [("provider_transient_unknown", 2), ("provider_timeout_unknown", 2), ("provider_transport_unknown", 2), ("provider_http_unknown", 1), ("provider_authentication_unknown", 1)])
def test_public_production_keeps_provider_failures_unknown_instead_of_scoring_agent_quality(tmp_path: Path, refusal_driver: Path, monkeypatch, code: str, attempted: int) -> None:
    config = experiment_config(tmp_path, repeats=2)
    payload = json.loads(config.read_text(encoding="utf-8"))
    approved = tmp_path / "approved-runtime"
    approved.mkdir()
    payload.update(runtime_contract="production_opensandbox", approved_workspace_root=str(approved), evidence_kind="fixture")
    payload["tasks"][0]["task_id"] = "production"
    config.write_text(json.dumps(payload), encoding="utf-8")
    monkeypatch.setenv("CODEOPS_FIXTURE_APPROVED_ROOT", str(approved))
    monkeypatch.setenv("CODEOPS_FIXTURE_PRODUCTION", "provider_unknown")
    monkeypatch.setenv("CODEOPS_FIXTURE_PROVIDER_ERROR", code)
    protocol = tmp_path / "protocol.ndjson"
    monkeypatch.setenv("CODEOPS_FIXTURE_PROTOCOL_LOG", str(protocol))
    output = tmp_path / "output"
    assert command("plan", "--config", str(config), "--output", str(output)).returncode == 0

    result = command("run", "--config", str(config), "--output", str(output), "--agent-binary", str(refusal_driver))

    assert result.returncode == 3, result.stderr
    directory = output / "public-pilot"
    rows = json.loads((directory / "summary.json").read_text(encoding="utf-8"))["trials"]
    row = rows[0]
    assert row["status"] == "infra_error" and row["success"] is None and row["error_code"] == code
    assert row["source_status"] == "failed" and row["admission_finalized"] is True
    assert row["usage"]["confirmed_tokens"] == 10578 and row["usage"]["reserved_tokens"] == 500000 and row["usage"]["unknown_calls"] == 1
    assert row["tokens_in"] is None and row["tokens_out"] is None
    if attempted == 2:
        assert rows[1]["status"] == "infra_error" and rows[1]["success"] is None
        assert rows[1]["admission_finalized"] is True and rows[1]["error_code"] == code
        assert rows[1]["usage"] == row["usage"]
    else:
        assert rows[1]["status"] == "not_run"
    requests = [json.loads(line) for line in protocol.read_text(encoding="utf-8").splitlines()]
    assert [request["operation"] for request in requests] == ["run", "finalize", "inspect"] * attempted
    assert not (directory / f"trials/{row['trial_id']}/final-score.json").exists()


def test_public_production_persists_failed_usage_before_historical_inspection_error(tmp_path: Path, refusal_driver: Path, monkeypatch) -> None:
    config = experiment_config(tmp_path, repeats=2)
    payload = json.loads(config.read_text(encoding="utf-8"))
    approved = tmp_path / "approved-runtime"
    approved.mkdir()
    payload.update(runtime_contract="production_opensandbox", approved_workspace_root=str(approved), evidence_kind="fixture")
    payload["tasks"][0]["task_id"] = "production"
    config.write_text(json.dumps(payload), encoding="utf-8")
    monkeypatch.setenv("CODEOPS_FIXTURE_APPROVED_ROOT", str(approved))
    monkeypatch.setenv("CODEOPS_FIXTURE_PRODUCTION", "provider_failed_inspect")
    protocol = tmp_path / "protocol.ndjson"
    monkeypatch.setenv("CODEOPS_FIXTURE_PROTOCOL_LOG", str(protocol))
    output = tmp_path / "output"
    monkeypatch.setenv("CODEOPS_FIXTURE_EARLY_ROW", str(output / "public-pilot/predictions.jsonl"))
    assert command("plan", "--config", str(config), "--output", str(output)).returncode == 0

    result = command("run", "--config", str(config), "--output", str(output), "--agent-binary", str(refusal_driver))

    assert result.returncode == 3, result.stderr
    directory = output / "public-pilot"
    rows = json.loads((directory / "summary.json").read_text(encoding="utf-8"))["trials"]
    row = rows[0]
    assert row["status"] == "infra_error" and row["success"] is None
    assert row["source_status"] == "failed" and row["error_code"] == "provider_model_identity_unconfirmed"
    assert row["session_id"] == "fixture-production-session" and row["product_run_id"] == "fixture-product-run"
    assert row["trace_id"] == "0123456789abcdef0123456789abcdef"
    assert row["tokens_in"] == 9184 and row["tokens_out"] == 287 and row["usage"]["confirmed_tokens"] == 9471
    assert row["admission_finalized"] is True and row["cleanup_blocked"] is True
    assert row["cleanup_error"]["stage"] == "final_inspect" and row["cleanup_error"]["error_code"] == "provider_model_identity_unconfirmed"
    cleanup = row["cleanup_error"]["receipt"]
    assert hashlib.sha256((directory / cleanup["path"]).read_bytes()).hexdigest() == cleanup["sha256"]
    validation = json.loads((directory / "receipt-validation.json").read_text(encoding="utf-8"))["trials"][0]
    assert validation["run_receipt"]["path"] == row["receipt_path"]
    assert (directory / row["receipt_path"]).is_file()
    assert rows[1]["status"] == "not_run" and rows[1]["success"] is None
    requests = [json.loads(line) for line in protocol.read_text(encoding="utf-8").splitlines()]
    assert [request["operation"] for request in requests] == ["run", "finalize", "inspect"]


@pytest.mark.parametrize("outcome", ["failed", "completed"])
def test_public_recovery_retains_the_source_outcome_when_the_fault_was_not_reached(tmp_path: Path, refusal_driver: Path, monkeypatch, outcome: str) -> None:
    config = experiment_config(tmp_path, repeats=1)
    payload = json.loads(config.read_text(encoding="utf-8"))
    approved = tmp_path / "approved-runtime"
    approved.mkdir()
    payload.update(experiment="recovery", failure_windows=["before_effect"], runtime_contract="production_opensandbox", approved_workspace_root=str(approved), evidence_kind="fixture")
    payload["tasks"][0]["task_id"] = "production"
    config.write_text(json.dumps(payload), encoding="utf-8")
    monkeypatch.setenv("CODEOPS_FIXTURE_APPROVED_ROOT", str(approved))
    monkeypatch.setenv("CODEOPS_FIXTURE_PRODUCTION", "graded_fault_missing_" + outcome)
    protocol = tmp_path / "protocol.ndjson"
    monkeypatch.setenv("CODEOPS_FIXTURE_PROTOCOL_LOG", str(protocol))
    output = tmp_path / "output"
    assert command("plan", "--config", str(config), "--output", str(output)).returncode == 0

    result = command("run", "--config", str(config), "--output", str(output), "--agent-binary", str(refusal_driver))

    assert result.returncode == 3, result.stderr
    directory = output / "public-pilot"
    rows = json.loads((directory / "summary.json").read_text(encoding="utf-8"))["trials"]
    assert len(rows) == 3 and rows[0]["success"] is True and rows[2]["status"] == "not_run"
    row = rows[1]
    assert row["source_status"] == outcome and row["status"] == "infra_error" and row["success"] is None
    assert row["error_code"] == ("provider_model_identity_unconfirmed" if outcome == "failed" else "fault_window_not_reached")
    assert row["tokens_in"] == (9184 if outcome == "failed" else 10)
    assert row["tokens_out"] == (287 if outcome == "failed" else 2)
    assert row["treatment_error"]["error_code"] == "fault_window_not_reached"
    assert row["admission_finalized"] is True and not row.get("cleanup_blocked", False)
    assert row["actually_interrupted"] is False and row["recovered"] is None
    assert (directory / row["receipt_path"]).is_file()
    requests = [json.loads(line) for line in protocol.read_text(encoding="utf-8").splitlines()]
    targeted = [request for request in requests if request["run_id"] == row["product_request_id"]]
    assert [request["operation"] for request in targeted] == ["run", "finalize", "inspect"]
    assert not (directory / f"trials/{row['trial_id']}/final-score.json").exists()


def test_public_production_keeps_unknown_grade_and_blocked_cleanup_separate(tmp_path: Path, refusal_driver: Path, monkeypatch) -> None:
    config = experiment_config(tmp_path, repeats=2)
    payload = json.loads(config.read_text(encoding="utf-8"))
    approved = tmp_path / "approved-runtime"
    approved.mkdir()
    payload.update(runtime_contract="production_opensandbox", approved_workspace_root=str(approved), evidence_kind="fixture")
    payload["tasks"][0]["task_id"] = "production"
    config.write_text(json.dumps(payload), encoding="utf-8")
    monkeypatch.setenv("CODEOPS_FIXTURE_APPROVED_ROOT", str(approved))
    monkeypatch.setenv("CODEOPS_FIXTURE_PRODUCTION", "graded_unknown_cleanup_blocked")
    protocol = tmp_path / "protocol.ndjson"
    monkeypatch.setenv("CODEOPS_FIXTURE_PROTOCOL_LOG", str(protocol))
    output = tmp_path / "output"
    assert command("plan", "--config", str(config), "--output", str(output)).returncode == 0

    result = command("run", "--config", str(config), "--output", str(output), "--agent-binary", str(refusal_driver))

    assert result.returncode == 3, result.stderr
    directory = output / "public-pilot"
    rows = json.loads((directory / "summary.json").read_text(encoding="utf-8"))["trials"]
    row = rows[0]
    assert row["status"] == "scorer_error" and row["success"] is None
    assert row["error_code"] == "grader_outcome_unknown" and row["grade"]["state"] == "unknown"
    assert row["grade"]["stopped"] is False and row["cleanup_blocked"] is True
    assert row["cleanup_error"]["error_code"] == "admission_finalization_unavailable"
    cleanup_ref = row["cleanup_error"]["receipt"]
    assert hashlib.sha256((directory / cleanup_ref["path"]).read_bytes()).hexdigest() == cleanup_ref["sha256"]
    assert rows[1]["status"] == "not_run" and rows[1]["success"] is None
    requests = [json.loads(line) for line in protocol.read_text(encoding="utf-8").splitlines()]
    assert [request["operation"] for request in requests] == ["run", "inspect", "grade", "finalize"]
    assert not any(request.get("finalize_mode") == "abandon" for request in requests)


@pytest.mark.parametrize("code", ["provider_model_identity_unconfirmed", "fixture_completed_error"])
def test_public_production_does_not_grade_a_completed_receipt_with_an_error(tmp_path: Path, refusal_driver: Path, monkeypatch, code: str) -> None:
    config = experiment_config(tmp_path, repeats=2)
    payload = json.loads(config.read_text(encoding="utf-8"))
    approved = tmp_path / "approved-runtime"
    approved.mkdir()
    payload.update(runtime_contract="production_opensandbox", approved_workspace_root=str(approved), evidence_kind="fixture")
    payload["tasks"][0]["task_id"] = "production"
    config.write_text(json.dumps(payload), encoding="utf-8")
    monkeypatch.setenv("CODEOPS_FIXTURE_APPROVED_ROOT", str(approved))
    monkeypatch.setenv("CODEOPS_FIXTURE_PRODUCTION", "graded_completed_error")
    monkeypatch.setenv("CODEOPS_FIXTURE_COMPLETED_ERROR", code)
    protocol = tmp_path / "protocol.ndjson"
    monkeypatch.setenv("CODEOPS_FIXTURE_PROTOCOL_LOG", str(protocol))
    output = tmp_path / "output"
    assert command("plan", "--config", str(config), "--output", str(output)).returncode == 0

    result = command("run", "--config", str(config), "--output", str(output), "--agent-binary", str(refusal_driver))

    assert result.returncode == 3, result.stderr
    directory = output / "public-pilot"
    rows = json.loads((directory / "summary.json").read_text(encoding="utf-8"))["trials"]
    row = rows[0]
    assert row["source_status"] == "completed" and row["error_code"] == code
    assert row["status"] == "infra_error" and row["success"] is None
    assert row["admission_finalized"] is True and row["provenance_verified"] is False
    assert row["tokens_in"] == 10 and row["tokens_out"] == 2
    assert rows[1]["status"] == "not_run" and rows[1]["success"] is None
    requests = [json.loads(line) for line in protocol.read_text(encoding="utf-8").splitlines()]
    assert [request["operation"] for request in requests] == ["run", "finalize", "inspect"]
    assert not (directory / f"trials/{row['trial_id']}/final-score.json").exists()


@pytest.mark.parametrize("identity", ["reported_model", "request_profile", "unknown_revision"])
def test_public_production_excludes_a_known_cross_purpose_model_substitution(tmp_path: Path, refusal_driver: Path, monkeypatch, identity: str) -> None:
    config = experiment_config(tmp_path, repeats=2)
    payload = json.loads(config.read_text(encoding="utf-8"))
    if identity == "unknown_revision":
        payload["repeats"] = 1
    approved = tmp_path / "approved-runtime"
    approved.mkdir()
    payload.update(runtime_contract="production_opensandbox", approved_workspace_root=str(approved), evidence_kind="fixture")
    payload["tasks"][0]["task_id"] = "production"
    config.write_text(json.dumps(payload), encoding="utf-8")
    monkeypatch.setenv("CODEOPS_FIXTURE_APPROVED_ROOT", str(approved))
    monkeypatch.setenv("CODEOPS_FIXTURE_PRODUCTION", "graded_identity_" + identity)
    protocol = tmp_path / "protocol.ndjson"
    monkeypatch.setenv("CODEOPS_FIXTURE_PROTOCOL_LOG", str(protocol))
    output = tmp_path / "output"
    assert command("plan", "--config", str(config), "--output", str(output)).returncode == 0

    result = command("run", "--config", str(config), "--output", str(output), "--agent-binary", str(refusal_driver))

    assert result.returncode == (0 if identity == "unknown_revision" else 3), result.stderr
    directory = output / "public-pilot"
    rows = json.loads((directory / "summary.json").read_text(encoding="utf-8"))["trials"]
    row = rows[0]
    assert row["source_status"] == "completed" and row["tokens_in"] == 10 and row["tokens_out"] == 2
    assert row["admission_finalized"] is True
    requests = [json.loads(line) for line in protocol.read_text(encoding="utf-8").splitlines()]
    if identity == "unknown_revision":
        assert all(item["success"] is True for item in rows)
        assert row["model_identity"]["revision_status"] == "unknown"
        assert any(request["operation"] == "grade" for request in requests)
    else:
        assert row["status"] == "infra_error" and row["success"] is None
        assert row["error_code"] == "provider_model_identity_unconfirmed"
        assert rows[1]["status"] == "not_run"
        assert [request["operation"] for request in requests] == ["run", "finalize", "inspect"]
        assert not (directory / f"trials/{row['trial_id']}/final-score.json").exists()


def test_public_production_keeps_a_grader_prerequisite_refusal_without_a_fake_operation(tmp_path: Path, refusal_driver: Path, monkeypatch) -> None:
    config = experiment_config(tmp_path, repeats=2)
    payload = json.loads(config.read_text(encoding="utf-8"))
    approved = tmp_path / "approved-runtime"
    approved.mkdir()
    payload.update(runtime_contract="production_opensandbox", approved_workspace_root=str(approved), evidence_kind="fixture")
    payload["tasks"][0]["task_id"] = "production"
    config.write_text(json.dumps(payload), encoding="utf-8")
    monkeypatch.setenv("CODEOPS_FIXTURE_APPROVED_ROOT", str(approved))
    monkeypatch.setenv("CODEOPS_FIXTURE_PRODUCTION", "candidate_freeze_unavailable")
    protocol = tmp_path / "protocol.ndjson"
    monkeypatch.setenv("CODEOPS_FIXTURE_PROTOCOL_LOG", str(protocol))
    output = tmp_path / "output"
    assert command("plan", "--config", str(config), "--output", str(output)).returncode == 0

    result = command("run", "--config", str(config), "--output", str(output), "--agent-binary", str(refusal_driver))

    assert result.returncode == 3, result.stderr
    directory = output / "public-pilot"
    rows = json.loads((directory / "summary.json").read_text(encoding="utf-8"))["trials"]
    row = rows[0]
    assert row["status"] == "scorer_error" and row["success"] is None
    assert row["error_code"] == "candidate_freeze_unavailable" and row["grade_source_error_code"] == "candidate_freeze_unavailable"
    assert row.get("grade") is None and row["tokens_in"] == 10 and row["tokens_out"] == 2
    assert rows[1]["status"] == "not_run"
    validation = json.loads((directory / "receipt-validation.json").read_text(encoding="utf-8"))["trials"][0]
    receipt = json.loads((directory / validation["grade_receipt"]["path"]).read_text(encoding="utf-8"))
    assert receipt["error_code"] == "candidate_freeze_unavailable" and "grade" not in receipt
    assert not (directory / f"trials/{row['trial_id']}/final-score.json").exists()
    requests = [json.loads(line) for line in protocol.read_text(encoding="utf-8").splitlines()]
    assert [request["operation"] for request in requests] == ["run", "inspect", "grade", "finalize", "inspect"]


@pytest.mark.parametrize("mode", ["graded_pass", "graded_fail", "graded_bad_checks_hash", "graded_unknown_stop", "graded_unknown", "graded_stale_head", "graded_no_model_osb"])
def test_public_production_grades_only_in_the_go_owned_independent_runtime(tmp_path: Path, refusal_driver: Path, monkeypatch, mode: str) -> None:
    config = experiment_config(tmp_path, repeats=1)
    payload = json.loads(config.read_text(encoding="utf-8"))
    approved = tmp_path / "approved-runtime"
    approved.mkdir()
    payload.update(runtime_contract="production_opensandbox", approved_workspace_root=str(approved), evidence_kind="fixture")
    payload["tasks"][0]["task_id"] = "production"
    config.write_text(json.dumps(payload), encoding="utf-8")
    monkeypatch.setenv("CODEOPS_FIXTURE_APPROVED_ROOT", str(approved))
    monkeypatch.setenv("CODEOPS_FIXTURE_PRODUCTION", mode)
    protocol = tmp_path / "protocol.ndjson"
    monkeypatch.setenv("CODEOPS_FIXTURE_PROTOCOL_LOG", str(protocol))
    output = tmp_path / "output"
    assert command("plan", "--config", str(config), "--output", str(output)).returncode == 0

    result = command("run", "--config", str(config), "--output", str(output), "--agent-binary", str(refusal_driver))

    passed = mode in {"graded_pass", "graded_no_model_osb"}
    known = passed or mode == "graded_fail"
    assert result.returncode == (0 if passed else 3), result.stderr
    directory = output / "public-pilot"
    summary = json.loads((directory / "summary.json").read_text(encoding="utf-8"))
    row = summary["trials"][0]
    assert row["status"] == ("completed" if known else "scorer_error")
    assert row["success"] is (passed if known else None)
    assert row["provenance_verified"] is False and row["evidence_kind"] == "fixture"
    if mode == "graded_fail":
        assert row["error_code"] == "final_state_check_failed"
    requests = [json.loads(line) for line in protocol.read_text(encoding="utf-8").splitlines()]
    grade_request = next(request for request in requests if request["operation"] == "grade")
    assert grade_request["grade_checks_path"] == str(tmp_path / "checks.json")
    assert grade_request["grade_checks_sha256"] == hashlib.sha256((tmp_path / "checks.json").read_bytes()).hexdigest()
    assert grade_request["expected_session_head_checksum"] == "a" * 64
    assert all("grade_checks_path" not in request for request in requests if request["operation"] in {"run", "continue"})
    assert requests[-2]["operation"] == "finalize" and requests[-1]["operation"] == "inspect"
    validation_record = json.loads((directory / "receipt-validation.json").read_text(encoding="utf-8"))
    validation = validation_record["trials"][0]
    assert validation_record["rows_sha256"] == hashlib.sha256(json.dumps(summary["trials"], sort_keys=True, separators=(",", ":")).encode("utf-8")).hexdigest()
    assert [json.loads(line) for line in (directory / "predictions.jsonl").read_text(encoding="utf-8").splitlines()] == summary["trials"]
    assert (directory / validation["grade_receipt"]["path"]).is_file()
    if known:
        assert validation["checks"]["independent_production_grade_verified"] is True
        assert row["grade_source_status"] == "graded"
        verdict = json.loads((directory / f"trials/{row['trial_id']}/final-score.json").read_text(encoding="utf-8"))
        assert verdict["success"] is passed
    else:
        assert validation["checks"]["independent_scorer_completed"] is False
    assert (Path(payload["tasks"][0]["workspace"]) / "calculator.py").read_text(encoding="utf-8") == "def add(a, b):\n    return a - b\n"
