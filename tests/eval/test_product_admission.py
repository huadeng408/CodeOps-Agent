"""Temp SQLite and public CLI only; native/provider/backend calls stay mocked."""
import hashlib
import json
import sqlite3

import pytest
from eval.harness import trace_contract as C
from test_product_intent_cli import cli_case

SELECTOR = "product-integrity-v3-admission-v1"
sha = lambda raw: hashlib.sha256(raw).hexdigest()


@pytest.fixture
def admission_case(cli_case):
    module, argv, root, pins, joined, save, state = cli_case
    argv[argv.index("product-integrity-v3")] = SELECTOR
    for span in state["spans"]:
        span.intent_observation = None
    artifact = state["capture_path"].parent / state["capture"]["runs"][0]["artifact"]
    document = json.loads(artifact.read_bytes())
    document["spans"] = [span.to_dict() for span in state["spans"]]
    state["capture"]["runs"][0]["sha256"] = save(artifact, document)
    state["original"]["trace_backend"]["id"] = "c" * 64
    original_sha = save(state["original_path"], state["original"])
    state["capture"]["inputs_sha256"] = original_sha
    capture_sha = save(state["capture_path"], state["capture"])
    joined["input_sha256"][str(state["original_path"])]=original_sha
    joined["input_sha256"][str(state["capture_path"])]=capture_sha
    pins.update(module._v3_projection(state["original"]))
    pins["product_integrity_v3"]["original_inputs"]["sha256"] = original_sha
    pins["product_integrity_v3"]["capture_index"]["sha256"] = capture_sha
    pins["product_integrity_v3"]["native_join"]["sha256"] = save(state["join_path"], joined)
    copy_root = root / "stopped-copy"
    copy_root.mkdir()
    database = copy_root / "phoenix.db"
    extra = state["spans"][-1]
    attributes = {"model": {"call_id": extra.attributes["model.call_id"], "purpose": extra.attributes["model.purpose"]},
                  "gen_ai": {"request": {"model": extra.attributes["gen_ai.request.model"]}}}
    with sqlite3.connect(database) as db:
        db.executescript("CREATE TABLE traces(id INTEGER,trace_id TEXT); CREATE TABLE spans(trace_rowid INTEGER,span_id TEXT,parent_id TEXT,name TEXT,end_time TEXT,attributes TEXT,events TEXT,status_code TEXT);")
        db.execute("INSERT INTO traces VALUES(1,?)", (extra.trace_id,))
        db.execute("INSERT INTO spans VALUES(1,?,?,?,?,?,?,?)", (extra.span_id, extra.parent_span_id, extra.name,
                   "2026-10-15T00:00:01Z", json.dumps(attributes), "[]", extra.status))
        main = state["spans"][0]
        db.execute("INSERT INTO spans VALUES(1,?,'','agent.main','ended',?,'[]','OK')",
            (main.span_id, json.dumps(main.attributes)))
    capture = {"schema": "stopped-phoenix-readonly-snapshot-v1", "container_id": "c" * 64,
        "completed_utc": "2026-10-16T00:00:00Z",
        "before": {"image": state["original"]["trace_backend"]["image"], "state": "exited"},
        "after": {"image": state["original"]["trace_backend"]["image"], "state": "exited"},
        "container_started": False, "source_data_modified_by_operation": False,
        "attempts": [{"name": "phoenix.db", "exit_code": 0, "state": "COPIED", "bytes": database.stat().st_size,
                      "sha256": sha(database.read_bytes()), "stdout_sha256": sha(b""), "stderr_sha256": sha(b"")},
                     {"name": "phoenix.db-wal", "exit_code": 1, "state": "ABSENT"},
                     {"name": "phoenix.db-shm", "exit_code": 1, "state": "ABSENT"}]}
    capture_path = copy_root / "capture.json"
    pins["product_integrity_v3"]["raw_backend_snapshot"] = {"path": str(database), "sha256": sha(database.read_bytes()),
        "capture_receipt": {"path": str(capture_path), "sha256": save(capture_path, capture)}}
    save(state["analysis_path"], pins)
    state.update(raw_db=database, raw_capture=capture, raw_capture_path=capture_path, raw_attributes=attributes)
    return module, argv, root, pins, joined, save, state


def test_go_only_cli_classifies_admission_without_inventing_missing_links(admission_case):
    module, argv, root, pins, joined, save, state = admission_case
    assert module.main(argv) == 0
    receipt = json.loads((root / "output/receipt.json").read_bytes())
    report = receipt["report"]
    assert report["contract_version"] == SELECTOR and report["scope"] == "go-only-admission-classification"
    assert report["metadata_complete"] is False and receipt["capture_state"] == "admission-accounted"
    assert report["span_count"] == len(state["spans"]) and report["error_spans"]
    assert report["model_intents"]["unadmitted_failed"] == 1
    extra = report["model_intents"]["unadmitted"][0]
    assert extra["admission"] == "absent" and extra["links"] == extra["billing"] == extra["exact_failure_stage"] == "unknown"
    assert extra["metadata_complete"] is False
    assert receipt["previous_integrity_report"]["verdict"] == receipt["strict_integrity_report"]["verdict"] == "FAIL"
    assert receipt["full_product_acceptance"] is False


def reseal_raw(case):
    module, argv, root, pins, joined, save, state = case
    raw = state["raw_db"].read_bytes()
    state["raw_capture"]["attempts"][0].update(sha256=sha(raw), bytes=len(raw))
    ref = pins["product_integrity_v3"]["raw_backend_snapshot"]
    ref["sha256"] = sha(raw)
    ref["capture_receipt"]["sha256"] = save(state["raw_capture_path"], state["raw_capture"])
    save(state["analysis_path"], pins)


@pytest.mark.parametrize("path,value", [
    (("cost",), {}), (("gen_ai", "usage", "total_tokens"), 0), (("gen_ai", "usage", "unknown"), False),
    (("http", "status_code"), 0), (("http", "was_sent"), False), (("gen_ai", "response", "any"), 0),
    (("model", "retry_of"), ""), (("model", "outcome"), False), (("cost", "total"), 0),
    (("llm", "token_count"), {}), (("llm", "token_count", "total"), 0),
    (("llm", "token_count", "unknown"), False), (("other", "usage", "value"), 0), (("links",), ["unknown"]),
])
def test_go_only_cli_refuses_raw_accounting_presence_even_zero_false_or_empty(admission_case, path, value):
    module, argv, root, pins, joined, save, state = admission_case
    attributes = state["raw_attributes"]
    for part in path[:-1]:
        attributes = attributes.setdefault(part, {})
    attributes[path[-1]] = value
    with sqlite3.connect(state["raw_db"]) as db:
        db.execute("UPDATE spans SET attributes=? WHERE name='provider.call'", (json.dumps(state["raw_attributes"]),))
    reseal_raw(admission_case)
    assert module.main(argv) == 3
    receipt = json.loads((root / "output/receipt.json").read_bytes())
    assert receipt["status"] == "BLOCKED" and receipt.get("report", {}).get("verdict") != "PASS"


@pytest.mark.parametrize("case", ["raw-sha", "receipt-sha", "container", "image", "before-live", "after-live",
    "started", "modified", "wal-copied", "duplicate-copy", "missing-shm", "size", "copy-hash"])
def test_go_only_cli_refuses_bad_raw_copy_authority(admission_case, case):
    module, argv, root, pins, joined, save, state = admission_case
    capture = state["raw_capture"]
    if case == "container": capture["container_id"] = "e" * 64
    elif case == "image": capture["after"]["image"] = "sha256:" + "e" * 64
    elif case == "before-live": capture["before"]["state"] = "running"
    elif case == "after-live": capture["after"]["state"] = "running"
    elif case == "started": capture["container_started"] = True
    elif case == "modified": capture["source_data_modified_by_operation"] = True
    elif case == "wal-present": state["raw_db"].with_name("phoenix.db-wal").write_bytes(b"unreconciled")
    elif case == "wal-copied": capture["attempts"][1].update(state="COPIED", exit_code=0)
    elif case == "duplicate-copy": capture["attempts"].append(dict(capture["attempts"][0]))
    elif case == "missing-shm": capture["attempts"].pop()
    elif case == "size": capture["attempts"][0]["bytes"] += 1
    elif case == "copy-hash": capture["attempts"][0]["sha256"] = "0" * 64
    ref = pins["product_integrity_v3"]["raw_backend_snapshot"]
    ref["capture_receipt"]["sha256"] = save(state["raw_capture_path"], capture)
    if case == "raw-sha": ref["sha256"] = "0" * 64
    elif case == "receipt-sha": ref["capture_receipt"]["sha256"] = "0" * 64
    save(state["analysis_path"], pins)
    assert module.main(argv) == 3
    receipt = json.loads((root / "output/receipt.json").read_bytes())
    assert receipt["status"] == "BLOCKED" and receipt["attempts"] == []


@pytest.mark.parametrize("case", ["missing", "duplicate", "body", "events", "malformed", "links-column", "links-table"])
def test_go_only_cli_refuses_ambiguous_or_changed_raw_span(admission_case, case):
    module, argv, root, pins, joined, save, state = admission_case
    with sqlite3.connect(state["raw_db"]) as db:
        if case == "missing": db.execute("DELETE FROM spans")
        elif case == "duplicate": db.execute("INSERT INTO spans SELECT * FROM spans")
        elif case == "body": db.execute("UPDATE spans SET name='other-provider'")
        elif case == "events": db.execute("UPDATE spans SET events='[{\"name\":\"unknown\"}]'")
        elif case == "malformed": db.execute("UPDATE spans SET attributes='[]'")
        elif case == "links-column": db.execute("ALTER TABLE spans ADD links TEXT")
        elif case == "links-table": db.execute("CREATE TABLE span_links(value TEXT)")
    reseal_raw(admission_case)
    before = state["raw_db"].read_bytes()
    assert module.main(argv) == 3
    assert state["raw_db"].read_bytes() == before


def test_go_only_cli_real_reader_still_refuses_llm_token_count_zero(admission_case, monkeypatch):
    from eval.harness import phoenix as P
    from test_product_intent_raw import public_read, raw_page
    module, argv, root, pins, joined, save, state = admission_case
    rows = raw_page(state["spans"], "clean")
    rows[-1]["attributes"]["llm"] = {"token_count": {"total": 0}}
    state["raw_attributes"]["llm"] = {"token_count": {"total": 0}}
    with sqlite3.connect(state["raw_db"]) as db:
        db.execute("UPDATE spans SET attributes=? WHERE name='provider.call'", (json.dumps(state["raw_attributes"]),))
    reseal_raw(admission_case)
    normalized = public_read(monkeypatch, rows, state["session"]["events"][0]["session_id"], state["capture"]["runs"][0]["run_id"])
    assert "llm.token_count.total" not in normalized[-1].attributes
    monkeypatch.setattr(module, "read_product_spans", P.read_product_spans)
    assert module.main(argv) == 3
    receipt = json.loads((root / "output/receipt.json").read_bytes())
    assert receipt["report"]["verdict"] == "FAIL"


@pytest.mark.parametrize("case", ["unknown-source", "observed-links", "late-body"])
def test_go_only_cli_keeps_existing_source_and_observed_counterfacts(admission_case, monkeypatch, case):
    module, argv, root, pins, joined, save, state = admission_case
    if case == "unknown-source": monkeypatch.setattr(module, "_V3_MANAGED_SOURCE_INVENTORIES", frozenset())
    elif case == "observed-links": state["spans"][-1].intent_observation = {"schema_version": 1, "complete": True, "transport_attribute_present": False, "raw_links_present": True}
    elif case == "late-body": state["spans"][-1].attributes["model.call_id"] = "late-unbound-intent"
    assert module.main(argv) == 3
    assert json.loads((root / "output/receipt.json").read_bytes())["report"]["verdict"] == "FAIL"


def test_go_only_cli_refuses_ambiguous_raw_call_identity(admission_case):
    module, argv, root, pins, joined, save, state = admission_case
    state["raw_attributes"]["model.call_id"] = "different-flat-identity"
    with sqlite3.connect(state["raw_db"]) as db:
        db.execute("UPDATE spans SET attributes=? WHERE name='provider.call'", (json.dumps(state["raw_attributes"]),))
    reseal_raw(admission_case)
    assert module.main(argv) == 3


def test_go_only_cli_refuses_duplicate_raw_parent_keys(admission_case):
    module, argv, root, pins, joined, save, state = admission_case
    raw = '{"gen_ai":{"usage":{"total_tokens":0}},' + json.dumps(state["raw_attributes"])[1:]
    with sqlite3.connect(state["raw_db"]) as db:
        db.execute("UPDATE spans SET attributes=? WHERE name='provider.call'", (raw,))
    reseal_raw(admission_case)
    assert module.main(argv) == 3


def test_go_only_cli_ignores_local_reader_sidecars_without_deleting(admission_case):
    module, argv, root, pins, joined, save, state = admission_case
    sidecars = {state["raw_db"].with_name("phoenix.db-wal"): b"local-reader-derivative",
                state["raw_db"].with_name("phoenix.db-shm"): b"\0" * 32768}
    for path, raw in sidecars.items():
        path.write_bytes(raw)
    before = state["raw_db"].read_bytes()
    assert module.main(argv) == 0
    assert state["raw_db"].read_bytes() == before
    assert all(path.read_bytes() == raw for path, raw in sidecars.items())
    receipt = json.loads((root / "output/receipt.json").read_bytes())
    assert receipt["raw_snapshot_observation"]["local_auxiliary_ignored_by_immutable"] == ["phoenix.db-wal", "phoenix.db-shm"]


def test_go_only_cli_matches_observed_closed_capture_through_basebody(admission_case):
    module, argv, root, pins, joined, save, state = admission_case
    extra = state["spans"][-1]
    extra.intent_observation = {"schema_version": 1, "complete": False, "transport_attribute_present": False, "raw_links_present": False}
    artifact = state["capture_path"].parent / state["capture"]["runs"][0]["artifact"]
    document = json.loads(artifact.read_bytes())
    document["spans"] = [span.to_dict() for span in state["spans"]]
    state["capture"]["runs"][0]["sha256"] = save(artifact, document)
    capture_sha = save(state["capture_path"], state["capture"])
    joined["input_sha256"][str(state["capture_path"])] = capture_sha
    pins["product_integrity_v3"]["capture_index"]["sha256"] = capture_sha
    pins["product_integrity_v3"]["native_join"]["sha256"] = save(state["join_path"], joined)
    save(state["analysis_path"], pins)
    assert module.main(argv) == 0
    receipt = json.loads((root / "output/receipt.json").read_bytes())
    assert receipt["report"]["metadata_complete"] is False
    assert receipt["strict_integrity_report"]["verdict"] == "FAIL"


@pytest.mark.parametrize("case", ["missing-completed", "before-native", "missing-root", "duplicate-root", "open-root", "wrong-owner"])
def test_go_only_cli_requires_final_copy_time_and_native_root_coverage(admission_case, case):
    module, argv, root, pins, joined, save, state = admission_case
    if case == "missing-completed": state["raw_capture"].pop("completed_utc")
    elif case == "before-native": state["raw_capture"]["completed_utc"] = "2026-10-14T00:00:00Z"
    else:
        with sqlite3.connect(state["raw_db"]) as db:
            if case == "missing-root": db.execute("DELETE FROM spans WHERE name='agent.main'")
            elif case == "duplicate-root": db.execute("INSERT INTO spans SELECT * FROM spans WHERE name='agent.main'")
            elif case == "open-root": db.execute("UPDATE spans SET end_time=NULL WHERE name='agent.main'")
            elif case == "wrong-owner": db.execute("UPDATE spans SET attributes='{}' WHERE name='agent.main'")
    reseal_raw(admission_case)
    assert module.main(argv) == 3
    receipt = json.loads((root / "output/receipt.json").read_bytes())
    assert receipt["status"] == "BLOCKED" and receipt["attempts"] == []


@pytest.mark.parametrize("events", ["null", "{}"])
def test_go_only_cli_refuses_falsey_raw_events_with_wrong_shape(admission_case, events):
    module, argv, root, pins, joined, save, state = admission_case
    with sqlite3.connect(state["raw_db"]) as db:
        db.execute("UPDATE spans SET events=? WHERE name='provider.call'", (events,))
    reseal_raw(admission_case)
    assert module.main(argv) == 3


@pytest.fixture
def recovered_root_case(admission_case):
    module, argv, root, pins, joined, save, state = admission_case
    session = state["session"]
    terminal = next(event for event in session["events"] if event["type"] == "session/run-completed")
    run, recovered_lease = terminal["payload"]["run_id"], "fixture-recovered-lease"
    old_lease = next(event for event in session["events"] if event["type"] == "session/run-leased")
    new_lease = json.loads(json.dumps(old_lease))
    new_lease.update(event_id="b" * 32, checksum="c" * 64)
    new_lease["payload"].update(attempt=2, lease_id=recovered_lease)
    session["events"].insert(terminal["seq"], new_lease)
    terminal["payload"].update(attempt=2, lease_id=recovered_lease)
    for number, event in enumerate(session["events"]):
        event["seq"] = number
    session["observation"] = {"count": len(session["events"]),
        "head": {key: session["events"][-1][key] for key in ("seq", "event_id", "checksum")},
        "sha256": sha(json.dumps(session["events"], sort_keys=True).encode())}
    session_path = state["current"] / "final-session.json"
    joined["input_sha256"][str(session_path)] = save(session_path, session)
    checkpoint_path = state["current"] / "native-checkpoints.jsonl"
    checkpoint = json.loads(checkpoint_path.read_bytes())
    checkpoint["session"] = session["observation"]
    checkpoint_path.write_text(json.dumps(checkpoint) + "\n", encoding="utf-8")
    joined["input_sha256"][str(checkpoint_path)] = sha(checkpoint_path.read_bytes())
    state["capture"]["native_checkpoint"] = checkpoint
    terminal_root = state["spans"][0]
    earlier_root = C.CapturedSpan("agent.main", terminal_root.trace_id, "9" * 16, attributes=dict(terminal_root.attributes))
    terminal_root.parent_span_id = earlier_root.span_id
    state["spans"].append(earlier_root)
    row = state["capture"]["runs"][0]
    artifact = state["capture_path"].parent / row["artifact"]
    body = json.loads(artifact.read_bytes())
    body["spans"] = [span.to_dict() for span in state["spans"]]
    row.update(sha256=save(artifact, body), spans=len(body["spans"]))
    capture_sha = save(state["capture_path"], state["capture"])
    joined["input_sha256"][str(state["capture_path"])] = capture_sha
    pins["product_integrity_v3"]["capture_index"]["sha256"] = capture_sha
    pins["product_integrity_v3"]["native_join"]["sha256"] = save(state["join_path"], joined)
    with sqlite3.connect(state["raw_db"]) as db:
        db.execute("UPDATE spans SET parent_id=? WHERE span_id=?", (earlier_root.span_id, terminal_root.span_id))
        db.execute("INSERT INTO spans VALUES(1,?,'','agent.main','ended',?,'[]','OK')",
            (earlier_root.span_id, json.dumps(earlier_root.attributes)))
    reseal_raw(admission_case)
    return module, argv, root, pins, joined, save, state


@pytest.mark.parametrize("case", ["valid", "wrong-owner", "open", "duplicate"])
def test_go_only_cli_covers_native_terminal_root_after_same_run_recovery(recovered_root_case, case):
    module, argv, root, pins, joined, save, state = recovered_root_case
    terminal_root = state["spans"][0]
    if case != "valid":
        with sqlite3.connect(state["raw_db"]) as db:
            if case == "wrong-owner": db.execute("UPDATE spans SET attributes='{}' WHERE span_id=?", (terminal_root.span_id,))
            elif case == "open": db.execute("UPDATE spans SET end_time=NULL WHERE span_id=?", (terminal_root.span_id,))
            elif case == "duplicate": db.execute("INSERT INTO spans SELECT * FROM spans WHERE span_id=?", (terminal_root.span_id,))
        reseal_raw(recovered_root_case)
    result = module.main(argv)
    receipt = json.loads((root / "output/receipt.json").read_bytes())
    assert result == (0 if case == "valid" else 3)
    if case == "valid":
        assert len([span for span in state["spans"] if span.name == "agent.main"]) == 2
        assert terminal_root.parent_span_id and receipt["report"]["verdict"] == "PASS"
        assert receipt["intent_authority_scope_reuse"]["native_results"] == 1
        assert receipt["raw_snapshot_observation"]["final_ended_roots"] == 1
        assert receipt["report"]["model_intents"]["unadmitted_failed"] == 1
        assert receipt["report"]["metadata_complete"] is False
    else:
        assert receipt["status"] == "BLOCKED" and receipt.get("report", {}).get("verdict") != "PASS"
