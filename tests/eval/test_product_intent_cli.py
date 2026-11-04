"""Public CLI fixtures only: sealed native result/owned runtime are synthetic."""
from pathlib import Path
from datetime import datetime, timedelta
import hashlib
import json

import pytest
from eval import product_trace
from eval.harness import trace_contract as C

ROOT = Path(__file__).resolve().parents[2]
sha = lambda raw: hashlib.sha256(raw).hexdigest()


@pytest.fixture
def cli_case(tmp_path, monkeypatch):
    helper = module = product_trace
    root = tmp_path / "fixture-root"
    root.mkdir()
    def save(path, value):
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(json.dumps(value, separators=(",", ":")).encode())
        return sha(path.read_bytes())
    def observation(events):
        return {"count": len(events), "head": {k: events[-1][k] for k in ("seq", "event_id", "checksum")},
                "sha256": sha(json.dumps(events, sort_keys=True).encode())}
    fixture = ROOT / "tests/fixtures/product_trace"
    session, before, after = [json.loads((fixture / (name + ".json")).read_bytes()) for name in ("session", "before", "after")]
    continued = [event for event in session["events"] if event["type"] == "session/continued"]
    terminals = [event for event in session["events"] if event["type"].startswith("session/run-") and event["type"] in {"session/run-completed", "session/run-failed", "session/run-canceled"}]
    assert len(continued) == len(terminals) == 1
    run = continued[0]["payload"]["run_id"]
    prefix = session["events"][:continued[0]["seq"]]
    session.update(verified=True, observation=observation(session["events"]))
    after.update(verified=True, observation=observation(after["events"]))
    runtime = root / "bundle"
    members = {"CodeOps-Agent.exe": sha(b"fixture launcher"), "bin/code-agent-server.exe": sha(b"fixture server"), "python/python.exe": sha(b"fixture python")}
    for name, body in (("CodeOps-Agent.exe", b"fixture launcher"), ("bin/code-agent-server.exe", b"fixture server"), ("python/python.exe", b"fixture python")):
        path = runtime / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(body)
    source_map = dict(helper._V3_MANAGED_CORE_VARIANTS[0]) | {"fixture/transitive.py": "f" * 64}
    for name, digest in helper._V3_MANAGED_CORE_VARIANTS[0].items():
        if name.startswith("orchestrator/"):
            members["app/" + name] = digest
    manifest = {"source_commit": "e" * 40, "source_files": source_map, "files": members}
    manifest_path = runtime / "bundle-manifest.json"
    manifest_sha = save(manifest_path, manifest)
    created = datetime.fromisoformat(continued[0]["created_at"].replace("Z", "+00:00"))
    launch = created - timedelta(seconds=10)
    stamp = lambda value: value.isoformat()
    current, prior = root / "receipts/current", root / "receipts/prior"
    current.mkdir(parents=True)
    prior.mkdir(parents=True)
    source_inventory = sha(json.dumps(source_map, sort_keys=True, separators=(",", ":")).encode())
    # Synthetic runtime only; this is not a reviewed production source variant.
    monkeypatch.setattr(module, "_V3_MANAGED_SOURCE_INVENTORIES", frozenset({source_inventory}), raising=False)
    original = {"identity": "fixture-current", "source_commit": "e" * 40, "source_sha256": source_map,
        "source_binding": {"source_pin": {"git_sha": "e" * 40, "dirty_hash": sha(b""), "untracked_files": 0}},
        "runtime_files": members, "bundle": "bundle", "bundle_manifest_sha256": manifest_sha,
        "task_images": {"go": "sha256:" + "c" * 64}, "trace_backend": {"image": "sha256:" + "d" * 64},
        "model": "fixture", "receipt_root": "receipts/current", "resume": {"receipt_root": "receipts/prior"},
        "frozen_at": stamp(launch - timedelta(seconds=1)),
        "initial_observer": {"session": observation(prefix), "admission": {"count": len(before["events"])}}}
    original_path = root / "original-inputs.json"
    original_sha = save(original_path, original)
    session_path, before_path, after_path = current / "final-session.json", root / "before.json", current / "final-admission.json"
    for path, document in ((session_path, session), (before_path, before), (after_path, after),
                           (prior / "final-session.json", {"events": prefix}), (prior / "final-admission.json", before)):
        save(path, document)
    start = {"pid": 10, "executable_path": str(runtime / "CodeOps-Agent.exe"), "started_utc": stamp(launch), "cim_started_utc": stamp(launch),
        "observed_identity": {"pid": 10, "executable_path": str(runtime / "CodeOps-Agent.exe"), "started_utc": stamp(launch)},
        "job_children": [{"pid": 11, "executable_path": str(runtime / "bin/code-agent-server.exe"), "started_utc": stamp(launch + timedelta(seconds=1))},
                         {"pid": 12, "executable_path": str(runtime / "python/python.exe"), "started_utc": stamp(launch + timedelta(seconds=2))}]}
    stop = {"launcher": 10, "browser_closed": True, "product_stop_confirmed": True, "resources_restored": True,
        "controls_restore_allowed": True, "observer_errors": [], "cleanup_errors": [], "active_task_egress_members": [],
        "resources": [{"initial_state": "exited", "final_state": "exited"}],
        "admission_before_restore": {"ledger_verified": True, "admission": after["observation"], "batch": {"ActiveCalls": 0, "TaskID": ""}}}
    save(current / "initial-start.json", start)
    save(current / "driver-stop.json", stop)
    checkpoint = {"ledger_verified": True, "session": session["observation"], "admission": after["observation"]}
    checkpoint_path = current / "native-checkpoints.jsonl"
    checkpoint_path.write_text(json.dumps(checkpoint) + "\n", encoding="utf-8")
    outcome = terminals[0]["type"].removeprefix("session/run-")
    capture = {"status": "CUMULATIVE_SNAPSHOTS_CAPTURED", "errors": [], "unknown_runs": [], "input_drift": [],
        "actual_runs": 1, "inputs_sha256": original_sha, "segment": original["identity"], "source_commit": original["source_commit"],
        "execution_start": original["frozen_at"], "terminal_inventory_unchanged": True, "absent_native_suffixes_unchanged": True,
        "native_checkpoint": checkpoint, "runs": [{"run_id": run, "capture_state": "CAPTURED", "segment": original["identity"],
            "source_commit": original["source_commit"], "outcome": outcome, "native_terminal_event_id": terminals[0]["event_id"],
            "native_terminal_checksum": terminals[0]["checksum"]}]}
    capture_path = root / "capture-index.json"
    capture_sha = save(capture_path, capture)
    auditor = root / "auditor/trace-audit.exe"
    auditor.parent.mkdir()
    auditor.write_bytes(b"fixture cached verifier; NOT EXECUTED")
    save(auditor.parent / "build.json", {"exit_code": 0, "binary_sha256": sha(auditor.read_bytes()),
        "source_commit": original["source_commit"], "source_sha256": source_map["cmd/trace-audit/main.go"],
        "compiled_source_inventory_sha256": source_inventory})
    bound = {str(path.resolve()): sha(path.read_bytes()) for path in root.rglob("*") if path.is_file()}
    bound.update({str((runtime / name).resolve()): digest for name, digest in members.items()})
    scopes = {"native_exports": "VERIFIED_ALL_ACTUAL_RUNS", "native_prefixes_and_observations": "VERIFIED_ALL_SEGMENTS",
        "fresh_backend": "VERIFIED_ALL_COMPLETE_TREES_WITH_PRIOR_SPANS_RETAINED", "protected_originals": "VERIFIED_FILES_INDEX_HEAD_STATUS",
        "protected_session": "VERIFIED_FROZEN_COUNT_HEAD_HASH_AND_FENCE", "negative_preservation": "VERIFIED_ALL_PREDECESSORS_ZERO_RUN_AND_MANUAL_RECOVERY_HISTORY",
        "lifecycle": "VERIFIED_CURRENT_OWNED_STOP_AND_RESTORE; PRIOR_P3_FAILURE_AND_P4_NORMAL_STOP_RETAINED", "trace_integrity": "FAIL"}
    joined = {"errors": [], "input_drift": [], "scopes": scopes, "full_product_acceptance": False, "input_sha256": bound,
        "native_runs": [{"run_id": run, "outcome": outcome, "status": "VERIFIED", "exit_code": 0, "stderr": "",
            "command": [str(auditor), str(session_path), str(before_path), str(after_path), run]}],
        "native_run_inventory": [{"run_id": run, "outcome": outcome}], "denominator": {"actual_runs": 1},
        "segments": [{"segment": original["identity"], "source_commit": original["source_commit"], "actual_runs": 1}],
        "runtime_bindings": {original["identity"]: {"source_commit": original["source_commit"], "manifest_sha256": manifest_sha,
            "source_inventory_sha256": source_inventory, "runtime_files": len(members)}},
        "terminal_root_bindings": [{"run_id": run, "terminal_event_id": terminals[0]["event_id"], "trace_id": "a" * 32, "span_id": "1" * 16}],
        "unconfirmed_span_execution_sources": [{"fixture_unknown": True}]}
    join_path = root / "native-join.json"
    join_sha = save(join_path, joined)
    pins = helper._v3_projection(original) | {"product_integrity_v3": {"schema_version": 1,
        "original_inputs": {"path": str(original_path), "sha256": original_sha},
        "native_join": {"path": str(join_path), "sha256": join_sha},
        "capture_index": {"path": str(capture_path), "sha256": capture_sha}}}
    analysis_path = root / "analysis-inputs.json"
    save(analysis_path, pins)
    for name, source in (("product_trace.py", ROOT / "eval/product_trace.py"), ("harness/trace_contract.py", ROOT / "eval/harness/trace_contract.py"),
                         ("harness/phoenix.py", ROOT / "eval/harness/phoenix.py")):
        path = root / "eval" / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(source.read_bytes())
    monkeypatch.setattr(module, "__file__", str(root / "eval/product_trace.py"))
    expected = helper._expectation(session, before, after, [run])
    spans = [C.CapturedSpan("agent.main", "a" * 32, "1" * 16, attributes={"session.id": expected["session_id"], "session.run_id": run}),
             C.CapturedSpan("chat", "a" * 32, "2" * 16, "1" * 16)]
    counter = 3
    for call_id, tool in expected["tool_calls_by_run"][run].items():
        spans.append(C.CapturedSpan("execute_tool " + tool, "a" * 32, f"{counter:016x}", "1" * 16, attributes={"gen_ai.tool.call.id": call_id, "gen_ai.tool.name": tool}))
        counter += 1
    for call_id, purpose in expected["model_calls"].items():
        logical = f"{counter:016x}"
        spans.append(C.CapturedSpan("provider.call", "a" * 32, logical, "2" * 16, attributes={"model.call_id": call_id, "model.purpose": purpose, "gen_ai.request.model": "fixture"}))
        counter += 1
        spans.append(C.CapturedSpan("provider.attempt", "a" * 32, f"{counter:016x}", logical, attributes={"model.call_id": call_id, "model.purpose": purpose, "gen_ai.request.model": "fixture"}))
        counter += 1
    spans.append(C.CapturedSpan("provider.call", "a" * 32, f"{counter:016x}", "2" * 16, status="ERROR", attributes={"model.call_id": "unadmitted-fixture", "model.purpose": "compaction", "gen_ai.request.model": "fixture"}))
    spans[-1].intent_observation = {"schema_version": 1, "complete": True,
        "transport_attribute_present": False, "raw_links_present": False}
    artifact = capture_path.parent / "fixture-run.json"
    artifact_sha = save(artifact, {"run_id": run, "segment": original["identity"], "source_commit": original["source_commit"],
        "native_terminal_event_id": terminals[0]["event_id"], "native_terminal_checksum": terminals[0]["checksum"],
        "spans": [span.to_dict() for span in spans]})
    capture["runs"][0].update(artifact=artifact.name, sha256=artifact_sha, spans=len(spans))
    capture_sha = save(capture_path, capture)
    joined["input_sha256"][str(capture_path)] = capture_sha
    pins["product_integrity_v3"]["capture_index"]["sha256"] = capture_sha
    pins["product_integrity_v3"]["native_join"]["sha256"] = save(join_path, joined)
    save(analysis_path, pins)
    forbidden = lambda *a, **k: (_ for _ in ()).throw(AssertionError("native/Go/network forbidden in pure fixture"))
    monkeypatch.setattr(module.subprocess, "run", forbidden)
    monkeypatch.setattr(module, "_verify_native_exports", forbidden)
    monkeypatch.setattr(module, "read_product_spans", lambda *a, **k: spans)
    argv = ["--phoenix-url", "http://127.0.0.1:1", "--project", "fixture-only", "--started-at", original["frozen_at"],
        "--contract-version", "product-integrity-v3", "--runtime-manifest", str(manifest_path), "--run-id", run,
        "--session-events", str(session_path), "--budget-before", str(before_path), "--budget-after", str(after_path),
        "--inputs", str(analysis_path), "--binary", str(runtime / "bin/code-agent-server.exe"), "--output", str(root / "output"), "--attempts", "1", "--interval", "0"]
    state = {"original": original, "manifest": manifest, "capture": capture, "start": start, "stop": stop,
        "session": session, "before": before, "after": after, "spans": spans, "source_map": source_map,
        "original_path": original_path, "manifest_path": manifest_path, "capture_path": capture_path,
        "analysis_path": analysis_path, "join_path": join_path, "current": current, "prior": prior, "auditor": auditor}
    return module, argv, root, pins, joined, save, state


def test_v3_public_cli_reuses_typed_native_scope_and_keeps_v2_failure(cli_case):
    module, argv, root, pins, joined, save, state = cli_case
    result = module.main(argv)
    receipt = json.loads((root / "output/receipt.json").read_bytes())
    assert result == 0, receipt
    assert receipt["report"]["verdict"] == "PASS"
    assert receipt["previous_integrity_report"]["verdict"] == "FAIL"
    assert receipt["intent_authority_scope_reuse"]["original_unconfirmed_span_execution_sources"] == [{"fixture_unknown": True}]
    assert receipt["intent_authority_scope_reuse"]["native_results"] == 1
    assert receipt["full_product_acceptance"] is False


def test_v3_public_cli_rejects_extra_not_in_closed_capture(cli_case):
    module, argv, root, pins, joined, save, state = cli_case
    spans = module.read_product_spans()
    spans[-1].attributes["model.call_id"] = "late-unknown-source-intent"
    assert module.main(argv) == 3
    receipt = json.loads((root / "output/receipt.json").read_bytes())
    assert receipt["report"]["verdict"] == "FAIL", receipt


@pytest.mark.parametrize("case", ["missing-native", "failed-native", "wrong-command", "wrong-outcome",
    "wrong-denominator", "wrong-checkpoint", "wrong-owned-pid", "active-call", "wrong-prefix",
    "capture-unknown", "wrong-terminal-root", "capture-bytes", "capture-escape"])
def test_v3_public_cli_refuses_tampered_authority_even_when_reference_is_resealed(cli_case, case):
    module, argv, root, pins, joined, save, state = cli_case
    def bind(path, document):
        joined["input_sha256"][str(path.resolve())] = save(path, document)
    if case == "missing-native": joined["native_runs"] = []
    elif case == "failed-native": joined["native_runs"][0]["exit_code"] = 1
    elif case == "wrong-command": joined["native_runs"][0]["command"][2] = str(root / "other-before.json")
    elif case == "wrong-outcome": joined["native_runs"][0]["outcome"] = "failed"
    elif case == "wrong-denominator": joined["denominator"]["actual_runs"] = 2
    elif case == "wrong-checkpoint":
        document = json.loads((state["current"] / "native-checkpoints.jsonl").read_bytes())
        document["session"]["sha256"] = "0" * 64
        bind(state["current"] / "native-checkpoints.jsonl", document)
    elif case == "wrong-owned-pid":
        state["stop"]["launcher"] = 999
        bind(state["current"] / "driver-stop.json", state["stop"])
    elif case == "active-call":
        state["stop"]["admission_before_restore"]["batch"]["ActiveCalls"] = 1
        bind(state["current"] / "driver-stop.json", state["stop"])
    elif case == "wrong-prefix":
        prefix = json.loads((state["prior"] / "final-session.json").read_bytes())
        prefix["events"][0]["checksum"] = "0" * 64
        bind(state["prior"] / "final-session.json", prefix)
    elif case == "capture-unknown":
        state["capture"]["unknown_runs"] = ["unknown"]
        checksum = save(state["capture_path"], state["capture"])
        joined["input_sha256"][str(state["capture_path"])] = checksum
        pins["product_integrity_v3"]["capture_index"]["sha256"] = checksum
    elif case == "wrong-terminal-root": joined["terminal_root_bindings"][0]["terminal_event_id"] = "f" * 32
    elif case == "capture-bytes":
        artifact = state["capture_path"].parent / state["capture"]["runs"][0]["artifact"]
        artifact.write_bytes(artifact.read_bytes() + b" ")
    elif case == "capture-escape":
        state["capture"]["runs"][0]["artifact"] = "../untrusted.json"
        checksum = save(state["capture_path"], state["capture"])
        joined["input_sha256"][str(state["capture_path"])] = checksum
        pins["product_integrity_v3"]["capture_index"]["sha256"] = checksum
    pins["product_integrity_v3"]["native_join"]["sha256"] = save(state["join_path"], joined)
    save(state["analysis_path"], pins)
    assert module.main(argv) == 3
    receipt = json.loads((root / "output/receipt.json").read_bytes())
    assert receipt.get("input_error") is not None, receipt
    assert receipt["attempts"] == []


@pytest.mark.parametrize("extra", [False, True])
def test_v3_unreviewed_runtime_cannot_exempt_extra_but_keeps_native_only_comparisons(cli_case, extra):
    module, argv, root, pins, joined, save, state = cli_case
    source_map = state["source_map"] | {"unreviewed/source.py": "0" * 64}
    state["original"]["source_sha256"] = source_map
    state["manifest"]["source_files"] = source_map
    manifest_sha = save(state["manifest_path"], state["manifest"])
    state["original"]["bundle_manifest_sha256"] = manifest_sha
    original_sha = save(state["original_path"], state["original"])
    state["capture"]["inputs_sha256"] = original_sha
    capture_sha = save(state["capture_path"], state["capture"])
    inventory = sha(json.dumps(source_map, sort_keys=True, separators=(",", ":")).encode())
    joined["runtime_bindings"][state["original"]["identity"]].update(manifest_sha256=manifest_sha, source_inventory_sha256=inventory)
    build_path = state["auditor"].parent / "build.json"
    build = json.loads(build_path.read_bytes())
    build["compiled_source_inventory_sha256"] = inventory
    save(build_path, build)
    for path in (state["original_path"], state["manifest_path"], state["capture_path"], build_path):
        joined["input_sha256"][str(path)] = sha(path.read_bytes())
    join_sha = save(state["join_path"], joined)
    pins.update(module._v3_projection(state["original"]))
    for field, digest in (("original_inputs", original_sha), ("capture_index", capture_sha), ("native_join", join_sha)):
        pins["product_integrity_v3"][field]["sha256"] = digest
    save(state["analysis_path"], pins)
    if not extra:
        state["spans"].pop()
    assert module.main(argv) == (3 if extra else 0)
    receipt = json.loads((root / "output/receipt.json").read_bytes())
    assert "input_error" not in receipt, receipt
    assert receipt["report"]["verdict"] == ("FAIL" if extra else "PASS"), receipt
    assert receipt["intent_authority_scope_reuse"]["contained_runs"] == []
