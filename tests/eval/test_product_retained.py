"""Two-segment public CLI fixtures; every native result is synthetic and never executed."""
from copy import deepcopy
from datetime import datetime, timedelta
from pathlib import Path
import hashlib
import json
import sqlite3

import pytest
from eval.harness import trace_contract as C
from test_product_admission import admission_case, reseal_raw
from test_product_intent_cli import cli_case

sha = lambda raw: hashlib.sha256(raw).hexdigest()


@pytest.fixture
def retained_case(admission_case, monkeypatch):
    module, argv, root, pins, joined, save, state = admission_case
    old_refs = {key: deepcopy(pins["product_integrity_v3"][key]) for key in ("original_inputs", "native_join", "capture_index")}
    old_original, old_join = deepcopy(state["original"]), deepcopy(joined)
    old_raw = deepcopy(pins["product_integrity_v3"]["raw_backend_snapshot"])
    old_session, old_after = deepcopy(state["session"]), deepcopy(state["after"])
    old_run = state["capture"]["runs"][0]["run_id"]
    run, call = "fixture-final-run", "fixture-final-call"
    session, after = deepcopy(old_session), deepcopy(old_after)
    stamp = datetime.fromisoformat(old_session["events"][-1]["created_at"].replace("Z", "+00:00")) + timedelta(minutes=1)
    def append(document, kind, payload):
        number = len(document["events"])
        document["events"].append({"session_id": document["events"][0]["session_id"], "seq": number,
            "event_id": sha((kind + str(number)).encode())[:32], "checksum": sha((kind + str(number)).encode()),
            "type": kind, "created_at": stamp.isoformat(), "payload": payload})
    def observation(document):
        events = document["events"]
        return {"count": len(events), "head": {key: events[-1][key] for key in ("seq", "event_id", "checksum")},
            "sha256": sha(json.dumps(events, sort_keys=True).encode())}
    append(session, "session/continued", {"run_id": run})
    append(session, "session/run-leased", {"run_id": run, "attempt": 1, "lease_id": "fixture-final-lease"})
    append(session, "session/run-completed", {"run_id": run})
    owner = next(event["payload"]["owner_id"] for event in after["events"] if event["type"] == "admission/call_reserved")
    append(after, "admission/call_reserved", {"owner_id": owner, "task_id": session["events"][0]["session_id"], "call_id": call, "purpose": "foreground", "tokens": 10})
    append(after, "admission/call_settled", {"owner_id": owner, "call_id": call, "tokens": 5})
    session["observation"], after["observation"] = observation(session), observation(after)
    runtime, current = root / "bundle-final", root / "receipts/final"
    manifest = deepcopy(state["manifest"])
    manifest["source_commit"] = "f" * 40
    manifest["source_files"].update(module._V3_MANAGED_CORE_VARIANTS[1])
    for name in ("CodeOps-Agent.exe", "bin/code-agent-server.exe", "python/python.exe"):
        target = runtime / name
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes((root / "bundle" / name).read_bytes())
    manifest_path = runtime / "bundle-manifest.json"
    manifest_sha = save(manifest_path, manifest)
    inventory = sha(json.dumps(manifest["source_files"], sort_keys=True, separators=(",", ":")).encode())
    monkeypatch.setattr(module, "_V3_MANAGED_SOURCE_INVENTORIES", module._V3_MANAGED_SOURCE_INVENTORIES | {inventory})
    original = deepcopy(old_original)
    original.update(identity="fixture-final", source_commit="f" * 40, source_sha256=manifest["source_files"], bundle="bundle-final",
        bundle_manifest_sha256=manifest_sha, receipt_root="receipts/final", resume={"receipt_root": "receipts/current"},
        frozen_at=(stamp - timedelta(seconds=11)).isoformat(),
        initial_observer={"session": old_session["observation"], "admission": old_after["observation"]})
    original["source_binding"]["source_pin"]["git_sha"] = "f" * 40
    original_path, capture_path, join_path = root / "final-inputs.json", root / "final-capture.json", root / "final-native-join.json"
    original_sha = save(original_path, original)
    start, stop = deepcopy(state["start"]), deepcopy(state["stop"])
    start["started_utc"] = start["cim_started_utc"] = (stamp - timedelta(seconds=10)).isoformat()
    start["executable_path"] = str(runtime / "CodeOps-Agent.exe")
    start["observed_identity"].update(executable_path=start["executable_path"], started_utc=start["started_utc"])
    for child, name in zip(start["job_children"], ("bin/code-agent-server.exe", "python/python.exe")):
        child.update(executable_path=str(runtime / name), started_utc=(stamp - timedelta(seconds=8)).isoformat())
    stop["admission_before_restore"]["admission"] = after["observation"]
    session_path, after_path = current / "final-session.json", current / "final-admission.json"
    for path, document in ((session_path, session), (after_path, after), (current / "initial-start.json", start), (current / "driver-stop.json", stop)):
        save(path, document)
    checkpoint = {"ledger_verified": True, "session": session["observation"], "admission": after["observation"]}
    (current / "native-checkpoints.jsonl").write_text(json.dumps(checkpoint) + "\n", encoding="utf-8")
    spans = state["spans"]
    old_extra = spans[-1]
    old_extra.intent_observation = {"schema_version": 1, "complete": False, "transport_attribute_present": False, "raw_links_present": False}
    spans.extend([C.CapturedSpan("agent.main", "b" * 32, "1" * 16, attributes={"session.id": session["events"][0]["session_id"], "session.run_id": run}),
        C.CapturedSpan("chat", "b" * 32, "2" * 16, "1" * 16),
        C.CapturedSpan("provider.call", "b" * 32, "3" * 16, "2" * 16, attributes={"model.call_id": call, "model.purpose": "foreground", "gen_ai.request.model": "fixture"}),
        C.CapturedSpan("provider.attempt", "b" * 32, "4" * 16, "3" * 16, attributes={"model.call_id": call, "model.purpose": "foreground", "gen_ai.request.model": "fixture"})])
    terminal = session["events"][-1]
    capture = deepcopy(state["capture"])
    capture.update(actual_runs=2, inputs_sha256=original_sha, segment=original["identity"], source_commit=original["source_commit"],
        execution_start=original["frozen_at"], native_checkpoint=checkpoint)
    capture["runs"].append({"run_id": run, "capture_state": "CAPTURED", "segment": original["identity"], "source_commit": original["source_commit"],
        "outcome": "completed", "native_terminal_event_id": terminal["event_id"], "native_terminal_checksum": terminal["checksum"]})
    for row, trace in zip(capture["runs"], ("a" * 32, "b" * 32)):
        artifact = root / (row["run_id"].replace(":", "-") + "-final.json")
        body = {key: row[key] for key in ("run_id", "segment", "source_commit", "native_terminal_event_id", "native_terminal_checksum")}
        body["spans"] = [span.to_dict() for span in spans if span.trace_id == trace]
        row.update(artifact=artifact.name, sha256=save(artifact, body), spans=len(body["spans"]))
    capture_sha = save(capture_path, capture)
    final_join = deepcopy(joined)
    for row in final_join["native_runs"]:
        row["command"][1:4] = [str(session_path), str(root / "before.json"), str(after_path)]
    final_join["native_runs"].append(dict(final_join["native_runs"][0], run_id=run,
        command=[str(state["auditor"]), str(session_path), str(root / "before.json"), str(after_path), run]))
    final_join["native_run_inventory"].append({"run_id": run, "outcome": "completed"})
    final_join["denominator"]["actual_runs"] = 2
    final_join["segments"].append({"segment": original["identity"], "source_commit": original["source_commit"], "actual_runs": 1})
    final_join["runtime_bindings"][original["identity"]] = {"source_commit": original["source_commit"], "manifest_sha256": manifest_sha,
        "source_inventory_sha256": inventory, "runtime_files": len(manifest["files"])}
    final_join["terminal_root_bindings"].append({"run_id": run, "terminal_event_id": terminal["event_id"], "trace_id": "b" * 32, "span_id": "1" * 16})
    final_join["input_sha256"].update({str(path.resolve()): sha(path.read_bytes()) for path in root.rglob("*") if path.is_file()})
    final_join["input_sha256"].update({str((runtime / name).resolve()): value for name, value in manifest["files"].items()})
    refs = {"schema_version": 1, "original_inputs": {"path": str(original_path), "sha256": original_sha},
        "native_join": {"path": str(join_path), "sha256": save(join_path, final_join)},
        "capture_index": {"path": str(capture_path), "sha256": capture_sha}, "retained_segment_proofs": [old_refs]}
    database = root / "final-copy/phoenix.db"
    database.parent.mkdir()
    database.write_bytes(state["raw_db"].read_bytes())
    with sqlite3.connect(database) as db:
        db.execute("INSERT INTO traces VALUES(2,?)", ("b" * 32,))
        db.execute("INSERT INTO spans VALUES(2,?,'','agent.main','ended',?,'[]','OK')",
            ("1" * 16, json.dumps({"session.id": session["events"][0]["session_id"], "session.run_id": run})))
    raw_capture_path = database.parent / "capture.json"
    state["raw_capture"]["attempts"][0].update(sha256=sha(database.read_bytes()), bytes=database.stat().st_size)
    refs["raw_backend_snapshot"] = {"path": str(database), "sha256": sha(database.read_bytes()),
        "capture_receipt": {"path": str(raw_capture_path), "sha256": save(raw_capture_path, state["raw_capture"])}}
    pins.clear()
    pins.update(module._v3_projection(original) | {"product_integrity_v3": refs})
    save(state["analysis_path"], pins)
    for option, path in (("--runtime-manifest", manifest_path), ("--session-events", session_path), ("--budget-after", after_path), ("--binary", runtime / "bin/code-agent-server.exe")):
        argv[argv.index(option) + 1] = str(path)
    argv[argv.index("--started-at") + 1] = original["frozen_at"]
    argv.extend(["--run-id", run])
    state.update(original=original, manifest=manifest, capture=capture, start=start, stop=stop, session=session, after=after,
        original_path=original_path, manifest_path=manifest_path, capture_path=capture_path, join_path=join_path,
        current=current, raw_db=database, raw_capture_path=raw_capture_path, old_run=old_run, old_extra=old_extra,
        old_original=old_original, old_join=old_join, old_refs=old_refs, old_raw=old_raw, final_run=run)
    return module, argv, root, pins, final_join, save, state


def reseal_capture(case):
    module, argv, root, pins, joined, save, state = case
    for row in state["capture"]["runs"]:
        artifact = state["capture_path"].parent / row["artifact"]
        document = json.loads(artifact.read_bytes())
        trace = "a" * 32 if row["run_id"] == state["old_run"] else "b" * 32
        document["spans"] = [span.to_dict() for span in state["spans"] if span.trace_id == trace]
        row.update(sha256=save(artifact, document), spans=len(document["spans"]))
    checksum = save(state["capture_path"], state["capture"])
    pins["product_integrity_v3"]["capture_index"]["sha256"] = checksum
    joined["input_sha256"][str(state["capture_path"])]=checksum
    pins["product_integrity_v3"]["native_join"]["sha256"] = save(state["join_path"], joined)
    save(state["analysis_path"], pins)


def test_retained_public_cli_joins_old_source_to_fresh_all_n_authority(retained_case):
    module, argv, root, pins, joined, save, state = retained_case
    assert module.main(argv) == 0
    receipt = json.loads((root / "output/receipt.json").read_bytes())
    assert receipt["intent_authority_scope_reuse"]["native_results"] == 2
    assert receipt["intent_authority_scope_reuse"]["retained_segments"][0]["native_results"] == 1
    assert receipt["report"]["model_intents"]["admitted_logical"] == 2
    assert receipt["report"]["model_intents"]["physical_attempts"] == 2
    extra = receipt["report"]["model_intents"]["unadmitted"][0]
    assert extra["run_id"] == state["old_run"] and extra["source_commit"] == "e" * 40
    assert extra["metadata_complete"] is False and extra["links"] == extra["billing"] == "unknown"
    assert receipt["strict_integrity_report"]["verdict"] == receipt["previous_integrity_report"]["verdict"] == "FAIL"


def test_retained_public_cli_does_not_upgrade_strict_v3_from_new_derived_observation(retained_case):
    module, argv, root, pins, joined, save, state = retained_case
    state["old_extra"].intent_observation["complete"] = True
    reseal_capture(retained_case)
    assert module.main(argv) == 0
    receipt = json.loads((root / "output/receipt.json").read_bytes())
    assert receipt["report"]["model_intents"]["unadmitted_failed"] == 1
    assert receipt["strict_integrity_report"]["verdict"] == receipt["previous_integrity_report"]["verdict"] == "FAIL"


@pytest.mark.parametrize("change", ["old-prefix", "original-before", "old-root", "duplicate-retained", "unregistered-old-source", "old-observation-positive"])
def test_retained_public_cli_refuses_inconsistent_predecessor_authority(retained_case, monkeypatch, change):
    module, argv, root, pins, joined, save, state = retained_case
    refs = state["old_refs"]
    old_join = state["old_join"]
    if change == "old-prefix":
        path = Path(old_join["native_runs"][0]["command"][1])
        document = json.loads(path.read_bytes())
        document["events"][0]["payload"]["title"] = "changed historical prefix"
        old_join["input_sha256"][str(path)] = save(path, document)
    elif change == "original-before":
        path = root / "different-old-before.json"
        path.write_bytes((root / "before.json").read_bytes() + b"\n")
        old_join["input_sha256"][str(path)] = sha(path.read_bytes())
        old_join["native_runs"][0]["command"][2] = str(path)
    elif change == "old-root": old_join["terminal_root_bindings"][0]["span_id"] = "e" * 16
    elif change == "duplicate-retained": pins["product_integrity_v3"]["retained_segment_proofs"].append(deepcopy(refs))
    elif change == "unregistered-old-source":
        old_inventory = old_join["runtime_bindings"][state["old_original"]["identity"]]["source_inventory_sha256"]
        monkeypatch.setattr(module, "_V3_MANAGED_SOURCE_INVENTORIES", module._V3_MANAGED_SOURCE_INVENTORIES - {old_inventory})
    elif change == "old-observation-positive":
        capture_ref = refs["capture_index"]
        capture_path = Path(capture_ref["path"])
        capture = json.loads(capture_path.read_bytes())
        row = capture["runs"][0]
        artifact = capture_path.parent / row["artifact"]
        body = json.loads(artifact.read_bytes())
        body["spans"][-1]["intent_observation"] = {"schema_version": 1, "complete": True, "transport_attribute_present": True, "raw_links_present": False}
        row["sha256"] = save(artifact, body)
        capture_ref["sha256"] = save(capture_path, capture)
        old_join["input_sha256"][str(capture_path)] = capture_ref["sha256"]
    refs["native_join"]["sha256"] = save(Path(refs["native_join"]["path"]), old_join)
    save(state["analysis_path"], pins)
    assert module.main(argv) == 3
    receipt = json.loads((root / "output/receipt.json").read_bytes())
    assert receipt["status"] == "BLOCKED" and receipt.get("report", {}).get("verdict") != "PASS"


@pytest.mark.parametrize("change", ["late-old-run", "base-attribute", "base-parent", "base-status", "base-ended", "base-links",
    "missing-old-native", "missing-final-native", "old-raw-copy", "current-presence", "raw-presence", "no-retained-proof"])
def test_retained_public_cli_rejects_unproved_body_native_or_raw_copy(retained_case, change):
    module, argv, root, pins, joined, save, state = retained_case
    extra = state["old_extra"]
    if change == "late-old-run":
        state["spans"].append(C.CapturedSpan("provider.call", extra.trace_id, "f" * 16, extra.parent_span_id, status="ERROR",
            attributes=dict(extra.attributes, **{"model.call_id": "late-old-run-intent"})))
        reseal_capture(retained_case)
    elif change.startswith("base-"):
        if change == "base-attribute": extra.attributes["model.purpose"] = "memory_reflection"
        elif change == "base-parent": extra.parent_span_id = "1" * 16
        elif change == "base-status": extra.status = "OK"
        elif change == "base-ended": extra.ended = False
        elif change == "base-links": extra.links = [("a" * 32, "c" * 16)]
        reseal_capture(retained_case)
    elif change == "missing-old-native":
        state["old_join"]["native_runs"].clear()
        ref = state["old_refs"]["native_join"]
        ref["sha256"] = save(Path(ref["path"]), state["old_join"])
        save(state["analysis_path"], pins)
    elif change == "missing-final-native":
        joined["native_runs"].pop()
        pins["product_integrity_v3"]["native_join"]["sha256"] = save(state["join_path"], joined)
        save(state["analysis_path"], pins)
    elif change == "old-raw-copy":
        pins["product_integrity_v3"]["raw_backend_snapshot"] = state["old_raw"]
        save(state["analysis_path"], pins)
    elif change == "current-presence": extra.intent_observation["transport_attribute_present"] = True
    elif change == "raw-presence":
        attributes = deepcopy(state["raw_attributes"]) | {"llm": {"token_count": {"total": 0}}}
        with sqlite3.connect(state["raw_db"]) as db:
            db.execute("UPDATE spans SET attributes=? WHERE name='provider.call'", (json.dumps(attributes),))
        reseal_raw(retained_case)
    elif change == "no-retained-proof":
        pins["product_integrity_v3"].pop("retained_segment_proofs")
        save(state["analysis_path"], pins)
    assert module.main(argv) == 3
    receipt = json.loads((root / "output/receipt.json").read_bytes())
    assert receipt["status"] == "BLOCKED" and receipt.get("report", {}).get("verdict") != "PASS"
