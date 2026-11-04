"""Read a real product run from Phoenix; no synthetic root or release promotion."""
from __future__ import annotations

import argparse
import hashlib
import json
import re
import sqlite3
import subprocess
import tempfile
import time
from datetime import datetime
from contextlib import closing
from pathlib import Path
from urllib.parse import urlsplit

from eval.harness.phoenix import _allowed_attributes, _intent_observation, read_product_spans
from eval.harness.trace_contract import CapturedSpan, ProductIntentAuthority, evaluate_product_trace, find_secret_leak


_V3_MANAGED_CORE_VARIANTS = (
    {
        "cmd/server/local_core.go": "00070c95fd2ca39529b0c1d2fa70a6047329ac2d9eb4c2093de9f4fa0a7bb662",
        "cmd/server/main.go": "93b3b6bdce5781652a2b63a6d5841159bc506dcd25d513edd6713fd61dbc929d",
        "cmd/trace-audit/main.go": "3078acea171f824dc2180c545533caea4e693e3523383b665526eefd7ef66a03",
        "internal/admission/budget.go": "36ae3aa2e4721f36b2b4f105681bdec5a671e9a50ecdc3d0c92952ebbd823774",
        "internal/admission/gateway.go": "3d89331cd73d37b447d23d3cd599cce444e5ea0ef6429bd9a12ff7e6ce3eb075",
        "internal/orchestrator/client.go": "53c9c59161c2bb2daabb1caf0c6c79d8b34fbe0c3f36fad6e6ba9a2153f00b7d",
        "internal/session/runner.go": "d416901101ee451219af460b4973227658912dd98354f9a8890070d5891b6182",
        "orchestrator/llm/client.py": "ce11e4b4f9280ec3f39315f9d7989e8647ed3f9388b9b1ef42b67576ba2f2f03",
        "orchestrator/llm/gateway.py": "a4d0cb16165f60097f3910dc443ba119d1cd5db0c67d6b6639313cdff5b28738",
        "orchestrator/llm/providers/anthropic.py": "b842d2742f15e379da831ceb3343432bb7754a8cf21ea358de8c9cc2a6c1a23d",
        "orchestrator/llm/providers/openai.py": "bd0c46bafebfe5373414931d992ab55ce75a68d640699ffb376edae36af2b44a",
        "orchestrator/server.py": "03919764a7913504804fedf4e10ee4d56ef1baf4c9e38ae1e4b43f1c45e2b898",
        "scripts/build-windows.py": "13eb26119efd414eb6f2346dccd772125bc89abaf41080ecc850575d2d351e3a"
    },
)
_V3_MANAGED_CORE_VARIANTS += (_V3_MANAGED_CORE_VARIANTS[0] | {"internal/session/runner.go": "5e9d538cd3226f483b5f38d4c04382e95c886fd88ff55bb2ae805bdf4e90aad6"},)
_V3_MANAGED_SOURCE_INVENTORIES = frozenset({"97bb45ab89be3998cacf5688312c165bd99a9c0234e98a6904ad38659d4fbead",
                                        "6dba8aa77cb6acdf521fbd324361e7ce42ef1cfe77f48779a87249f15fc2cfaa"})


def _expectation(session: dict, before: dict, after: dict, run_ids: list[str]) -> dict:
    events = session["events"]
    session_id = events[0]["session_id"]
    if any(event["session_id"] != session_id for event in events):
        raise ValueError("mixed Session export")
    canonical_runs = [event["payload"]["run_id"] for event in events if event["type"] == "session/continued"]
    if len(canonical_runs) != len(run_ids) or set(canonical_runs) != set(run_ids):
        raise ValueError("all canonical Session runs are required")
    relevant = [event for event in events if event["payload"].get("run_id") in run_ids]
    terminal = {event["payload"]["run_id"]: event["type"] for event in relevant if event["type"] in {"session/run-completed", "session/run-failed", "session/run-canceled"}}
    if not any(event["type"] == "code/modified" for event in relevant):
        raise ValueError("real code modification required")
    tools = {run_id: {} for run_id in run_ids}
    for event in relevant:
        if event["type"] != "tool/call":
            continue
        fact = event["payload"]
        calls = tools[fact["run_id"]]
        if fact["tool_call_id"] in calls:
            raise ValueError("duplicate durable tool identity")
        calls[fact["tool_call_id"]] = fact["tool_name"]
    reservations = [event["payload"] for event in after["events"][len(before["events"]):]
                    if event["type"] == "admission/call_reserved" and event["payload"].get("task_id") == session_id]
    models = {fact["call_id"]: fact["purpose"] for fact in reservations}
    if len(models) != len(reservations):
        raise ValueError("duplicate model admission identities")
    settled = {event["payload"]["call_id"] for event in after["events"] if event["type"] == "admission/call_settled"}
    attempts, tool_facts = {run: [] for run in run_ids}, {run: {} for run in run_ids}
    for event in relevant:
        fact, kind = event["payload"], event["type"]
        run = fact["run_id"]
        if kind == "session/run-leased":
            attempts[run].append((fact.get("attempt"), fact.get("lease_id")))
        if kind in {"tool/call", "tool/dispatched", "tool/result", "tool/unknown", "approval/denied"}:
            tool_facts[run].setdefault(fact["tool_call_id"], []).append({
                "type": kind, "seq": event["seq"], "tool_name": fact["tool_name"],
                "arguments_sha256": hashlib.sha256(fact.get("arguments_json", "").encode()).hexdigest(),
                "decision": fact.get("decision"), "exit_code": fact.get("exit_code", 0),
                "has_error": bool(fact.get("error")), "outcome_unknown": fact.get("outcome_unknown", False),
                "runtime_started": bool(fact.get("runtime_id")), "stopped": fact.get("stopped", False)})
    return {"session_id": session_id, "run_id": run_ids[0], "run_ids": run_ids,
            "completed": all(terminal.get(run_id) == "session/run-completed" for run_id in run_ids),
            "run_outcomes": {run: terminal.get(run, "unknown").removeprefix("session/run-") for run in run_ids},
            "cancellation_states": {event["payload"]["run_id"]: event["payload"].get("cancellation_state", "unknown")
                                    for event in relevant if event["type"] == "session/run-canceled"},
            "attempts_by_run": {run: len(leases) for run, leases in attempts.items()},
            "run_attempts_valid": all([number for number, _ in leases] == list(range(1, len(leases) + 1))
                                      and all(lease for _, lease in leases) and len({lease for _, lease in leases}) == len(leases)
                                      for leases in attempts.values()),
            "tool_facts_by_run": tool_facts,
            "tool_calls_by_run": tools, "model_calls": models,
            "unsettled_model_calls": sorted(models.keys() - settled),
            "retry_of": {fact["call_id"]: fact.get("retry_of", "") for fact in reservations}}


def _verify_native_exports(documents: dict[str, bytes], run_id: str, audit_binary: Path | None = None) -> None:
    # Verify the same immutable bytes that are hashed and interpreted below.
    with tempfile.TemporaryDirectory(prefix="product-trace-audit-") as directory:
        paths = []
        for key in ("session_events", "budget_before", "budget_after"):
            path = Path(directory) / (key + ".json")
            path.write_bytes(documents[key])
            paths.append(str(path))
        command = [str(audit_binary)] if audit_binary is not None else ["go", "run", "./cmd/trace-audit"]
        result = subprocess.run([*command, *paths, run_id],
                                cwd=Path(__file__).resolve().parents[1], capture_output=True, timeout=120)
        if result.returncode:
            raise ValueError("native product audit refused")


def _v3_projection(original: dict) -> dict:
    return {"source_sha256": original["source_sha256"], "compiled_source_files": original["source_sha256"],
            "source_commit": original["source_commit"], "bundle_manifest_sha256": original["bundle_manifest_sha256"],
            "binary_sha256": original["runtime_files"]["bin/code-agent-server.exe"],
            "task_image": original["task_images"]["go"], "model": original["model"], "trace_backend": original["trace_backend"]}


def _intent_base_key(span):
    body = {key: value for key, value in span.items() if key != "intent_observation"}
    return span["trace_id"], span["span_id"], hashlib.sha256(json.dumps(body, sort_keys=True, separators=(",", ":")).encode()).hexdigest()


def _no_observed_presence(span):
    observation = span.get("intent_observation")
    return observation is None or (isinstance(observation, dict)
        and set(observation) == {"schema_version", "complete", "transport_attribute_present", "raw_links_present"}
        and type(observation["schema_version"]) is int and observation["schema_version"] == 1
        and type(observation["complete"]) is bool and observation["transport_attribute_present"] is False
        and observation["raw_links_present"] is False)


def _raw_attribute_absence(ref, original, bodies, admitted, roots, sources, load_ref, require, receipt):
    require(isinstance(ref, dict) and set(ref) == {"path", "sha256", "capture_receipt"}, "typed raw snapshot required")
    raw, path = load_ref({key: ref[key] for key in ("path", "sha256")}, binary=True)
    capture, capture_path = load_ref(ref["capture_receipt"])
    backend = original["trace_backend"]
    attempts = {row["name"]: row for row in capture["attempts"]}
    require(capture["schema"] == "stopped-phoenix-readonly-snapshot-v1"
            and capture["container_id"] == backend["id"] and path.parent == capture_path.parent
            and capture["container_started"] is False and capture["source_data_modified_by_operation"] is False
            and all(capture[phase]["state"] == "exited" and capture[phase]["image"] == backend["image"] for phase in ("before", "after"))
            and len(capture["attempts"]) == 3 and set(attempts) == {"phoenix.db", "phoenix.db-wal", "phoenix.db-shm"}
            and path.name == "phoenix.db" and attempts["phoenix.db"]["state"] == "COPIED" and attempts["phoenix.db"]["exit_code"] == 0
            and attempts["phoenix.db"]["sha256"] == ref["sha256"] and attempts["phoenix.db"]["bytes"] == len(raw)
            and datetime.fromisoformat(capture["completed_utc"].replace("Z", "+00:00")) >= max(
                datetime.fromisoformat(event["created_at"].replace("Z", "+00:00"))
                for key in ("session_events", "budget_after") for event in sources[key]["events"])
            and all(attempts[name]["state"] == "ABSENT" and attempts[name]["exit_code"] == 1
                    for name in ("phoenix.db-wal", "phoenix.db-shm")), "raw snapshot copy/lifecycle binding differs")
    receipt["raw_snapshot_observation"] = {"sqlite_mode": "ro&immutable=1", "source_auxiliary": "ABSENT_IN_CAPTURE_RECEIPT",
        "local_auxiliary_ignored_by_immutable": [name for name in ("phoenix.db-wal", "phoenix.db-shm") if path.with_name(name).exists()]}
    def unique(pairs):
        value = dict(pairs)
        require(len(value) == len(pairs), "duplicate raw JSON key")
        return value
    def flatten(attributes):
        require(isinstance(attributes, dict), "raw attributes incomplete")
        flat, pending = {}, [("", attributes)]
        while pending:
            prefix, values = pending.pop()
            for key, value in values.items():
                key = prefix + "." + key if prefix else key
                require(key not in flat or flat[key] == value, "ambiguous raw attribute path")
                flat[key] = value
                if isinstance(value, dict):
                    pending.append((key, value))
        return flat
    absent = set()
    with closing(sqlite3.connect(path.as_uri() + "?mode=ro&immutable=1", uri=True)) as db:
        db.execute("PRAGMA query_only=ON")
        # shortcut: this stopped schema omits links; review a new schema before accepting link storage.
        require(not any("link" in row[1].lower() for row in db.execute("PRAGMA table_info(spans)")), "raw links schema needs explicit review")
        require(db.execute("SELECT name FROM sqlite_master WHERE name LIKE '%link%'").fetchone() is None, "raw link storage needs explicit review")
        for run, trace_id, span_id in roots:
            rows = db.execute("SELECT s.name,s.parent_id,s.end_time,json(s.attributes) FROM spans s JOIN traces t ON t.id=s.trace_rowid WHERE t.trace_id=? AND s.span_id=?", (trace_id, span_id)).fetchall()
            require(len(rows) == 1 and rows[0][0] == "agent.main" and bool(rows[0][2]), "final raw root missing, ambiguous or open")
            attributes = flatten(json.loads(rows[0][3], object_pairs_hook=unique))
            require(attributes.get("session.id") == sources["expected"]["session_id"]
                    and attributes.get("session.run_id") == run, "final raw root owner differs")
        receipt["raw_snapshot_observation"].update(completed_utc=capture["completed_utc"], final_ended_roots=len(roots))
        for body in bodies:
            if body["name"] != "provider.call" or body["attributes"].get("model.call_id") in admitted:
                continue
            rows = db.execute("SELECT s.name,s.parent_id,s.status_code,s.end_time,json(s.attributes),json(s.events) FROM spans s JOIN traces t ON t.id=s.trace_rowid WHERE t.trace_id=? AND s.span_id=?", (body["trace_id"], body["span_id"])).fetchall()
            require(len(rows) == 1, "raw span identity missing or ambiguous")
            name, parent, status, ended, attributes, events = rows[0]
            attributes, events = json.loads(attributes, object_pairs_hook=unique), json.loads(events, object_pairs_hook=unique)
            require(isinstance(events, list), "raw events must be an explicit list")
            flat = flatten(attributes)
            projected = CapturedSpan(name, body["trace_id"], body["span_id"], parent or "", status,
                                     _allowed_attributes(flat), ended=bool(ended)).to_dict()
            base_body = {key: value for key, value in body.items() if key != "intent_observation"}
            require(projected == base_body, "raw span differs from exact original normalized basebody")
            if not _no_observed_presence(body):
                continue
            if events or _intent_observation({"attributes": attributes})["transport_attribute_present"]:
                continue
            absent.add(_intent_base_key(body))
    require(path.read_bytes() == raw, "raw snapshot changed during observation")
    return frozenset(absent)


def _v3_authority(pins, sources, documents, args, receipt, source_root):
    """Reuse one SHA-bound stopped native audit; never create a trust boolean."""
    def require(value, message):
        if not value:
            raise ValueError(message)

    def digest(raw):
        return hashlib.sha256(raw).hexdigest()

    def load_ref(ref, *, binary=False):
        require(isinstance(ref, dict) and set(ref) == {"path", "sha256"}
                and isinstance(ref["path"], str) and re.fullmatch(r"[0-9a-f]{64}", ref["sha256"]), "typed v3 artifact reference required")
        path = Path(ref["path"]).resolve()
        raw = path.read_bytes()
        require(digest(raw) == ref["sha256"], "v3 artifact fingerprint differs")
        receipt["input_sha256"][str(path)] = ref["sha256"]
        return (raw if binary else json.loads(raw)), path

    refs = pins.get("product_integrity_v3")
    admission = args.contract_version == "product-integrity-v3-admission-v1"
    require(isinstance(refs, dict) and set(refs) == {"schema_version", "original_inputs", "native_join", "capture_index"}
            | ({"raw_backend_snapshot"} if admission else set())
            | ({"retained_segment_proofs"} if admission and "retained_segment_proofs" in refs else set())
            and refs["schema_version"] == 1, "typed contained-run v3 evidence required")
    original, original_path = load_ref(refs["original_inputs"])
    require({key: value for key, value in pins.items() if key != "product_integrity_v3"} == _v3_projection(original),
            "analysis inputs differ from original runtime projection")
    joined, join_path = load_ref(refs["native_join"])
    capture, capture_path = load_ref(refs["capture_index"])
    require(joined["errors"] == [] and joined["input_drift"] == []
            and joined["scopes"]["native_exports"] == "VERIFIED_ALL_ACTUAL_RUNS"
            and joined["scopes"]["native_prefixes_and_observations"] == "VERIFIED_ALL_SEGMENTS"
            and joined["scopes"]["fresh_backend"] == "VERIFIED_ALL_COMPLETE_TREES_WITH_PRIOR_SPANS_RETAINED"
            and joined["scopes"]["protected_originals"] == "VERIFIED_FILES_INDEX_HEAD_STATUS"
            and joined["scopes"]["protected_session"] == "VERIFIED_FROZEN_COUNT_HEAD_HASH_AND_FENCE"
            and joined["scopes"]["negative_preservation"] == "VERIFIED_ALL_PREDECESSORS_ZERO_RUN_AND_MANUAL_RECOVERY_HISTORY"
            and joined["scopes"]["lifecycle"] == "VERIFIED_CURRENT_OWNED_STOP_AND_RESTORE; PRIOR_P3_FAILURE_AND_P4_NORMAL_STOP_RETAINED"
            and joined["full_product_acceptance"] is False, "complete stopped native scope reuse required")
    bound = joined["input_sha256"]
    require(bound.get(str(original_path)) == refs["original_inputs"]["sha256"]
            and bound.get(str(capture_path)) == refs["capture_index"]["sha256"], "original/capture not bound by native join")

    def pinned(path):
        path = Path(path).resolve()
        require(str(path) in bound, "required artifact is not in native audit input pins")
        return load_ref({"path": str(path), "sha256": bound[str(path)]})[0]

    session, before, after = (sources[key] for key in ("session_events", "budget_before", "budget_after"))
    paths = [Path(getattr(args, key)).resolve() for key in ("session_events", "budget_before", "budget_after")]
    require(all(bound.get(str(path)) == digest(documents[key]) for path, key in
                zip(paths, ("session_events", "budget_before", "budget_after"))), "native audit exports are not the exact CLI bytes")
    events = session["events"]
    continued = [event for event in events if event["type"] == "session/continued"]
    terminals = [event for event in events if event["type"] in {"session/run-completed", "session/run-failed", "session/run-canceled"}]
    run_ids = [event["payload"]["run_id"] for event in continued]
    native_rows = joined["native_runs"]
    native_outcomes = {event["payload"]["run_id"]: event["type"].removeprefix("session/run-") for event in terminals}
    require(run_ids == args.run_id and len(set(run_ids)) == len(run_ids) and len(terminals) == len(run_ids)
            and {event["payload"]["run_id"] for event in terminals} == set(run_ids)
            and [row["run_id"] for row in native_rows] == run_ids
            and [row["run_id"] for row in joined["native_run_inventory"]] == run_ids
            and joined["denominator"]["actual_runs"] == len(run_ids)
            and all(row["outcome"] == native_outcomes[row["run_id"]] for row in native_rows + joined["native_run_inventory"])
            and all(row["status"] == "VERIFIED" and row["exit_code"] == 0 and row["stderr"] == "" for row in native_rows),
            "all canonical native Run results required")
    auditor = Path(native_rows[0]["command"][0]).resolve()
    auditor_raw = auditor.read_bytes()
    require(bound.get(str(auditor)) == digest(auditor_raw)
            and all([str(Path(part).resolve()) for part in row["command"][:4]] == [str(auditor), *map(str, paths)]
                    and len(row["command"]) == 5 and row["command"][4] == row["run_id"] for row in native_rows),
            "native verifier command/input binding differs")
    build = pinned(auditor.parent / "build.json")
    require(build["exit_code"] == 0 and build["binary_sha256"] == digest(auditor_raw)
            and build["source_sha256"] == original["source_sha256"]["cmd/trace-audit/main.go"], "cached native verifier provenance differs")
    bundle = (source_root / original["bundle"]).resolve()
    manifest = pinned(bundle / "bundle-manifest.json")
    source_map = manifest["source_files"]
    require(manifest["source_commit"] == original["source_commit"] == original["source_binding"]["source_pin"]["git_sha"]
            and original["source_binding"]["source_pin"]["dirty_hash"] == digest(b"")
            and original["source_binding"]["source_pin"]["untracked_files"] == 0
            and source_map == original["source_sha256"] and manifest["files"] == original["runtime_files"],
            "original runtime source/member binding differs")
    reviewed_core = any(all(source_map.get(name) == checksum for name, checksum in variant.items())
        and all(manifest["files"].get("app/" + name) == checksum for name, checksum in variant.items() if name.startswith("orchestrator/"))
        for variant in _V3_MANAGED_CORE_VARIANTS)
    require(bound[str(bundle / "bundle-manifest.json")] == original["bundle_manifest_sha256"]
            and all((bundle / name).resolve().is_relative_to(bundle)
                    and bound.get(str((bundle / name).resolve())) == checksum for name, checksum in manifest["files"].items()),
            "complete frozen runtime member inventory missing")
    runtime = joined["runtime_bindings"][original["identity"]]
    source_sha = digest(json.dumps(source_map, sort_keys=True, separators=(",", ":")).encode())
    require(runtime == {"source_commit": original["source_commit"], "manifest_sha256": original["bundle_manifest_sha256"],
                        "source_inventory_sha256": source_sha, "runtime_files": len(manifest["files"])}
            and any(build["source_commit"] == value["source_commit"]
                    and build["compiled_source_inventory_sha256"] == value["source_inventory_sha256"]
                    for value in joined["runtime_bindings"].values()), "native/runtime source inventory differs")
    current = (source_root / original["receipt_root"]).resolve()
    start, stop = pinned(current / "initial-start.json"), pinned(current / "driver-stop.json")
    children = start["job_children"]
    expected_children = {bundle / "bin/code-agent-server.exe", bundle / "python/python.exe"}
    require(Path(start["executable_path"]).resolve() == bundle / "CodeOps-Agent.exe"
            and start["pid"] == start["observed_identity"]["pid"] == stop["launcher"]
            and Path(start["observed_identity"]["executable_path"]).resolve() == bundle / "CodeOps-Agent.exe"
            and start["cim_started_utc"] == start["observed_identity"]["started_utc"]
            and {Path(child["executable_path"]).resolve() for child in children} == expected_children
            and len(children) == len({child["pid"] for child in children}) == 2
            and stop["browser_closed"] is True and stop["product_stop_confirmed"] is True
            and stop["resources_restored"] is True and stop["controls_restore_allowed"] is True
            and stop["observer_errors"] == stop["cleanup_errors"] == stop["active_task_egress_members"] == []
            and stop["admission_before_restore"]["ledger_verified"] is True
            and stop["admission_before_restore"]["batch"]["ActiveCalls"] == 0
            and stop["admission_before_restore"]["batch"]["TaskID"] == ""
            and bool(stop["resources"])
            and all(row["initial_state"] == row["final_state"] for row in stop["resources"]), "owned frozen lifecycle differs")
    checkpoint_path = current / "native-checkpoints.jsonl"
    checkpoint_raw = checkpoint_path.read_bytes()
    require(bound.get(str(checkpoint_path)) == digest(checkpoint_raw), "final observer anchor pin differs")
    checkpoint = json.loads(checkpoint_raw.splitlines()[-1])
    for kind, native in (("session", session), ("admission", after)):
        observation = native["observation"]
        require(native["verified"] is True and [event["seq"] for event in native["events"]] == list(range(len(native["events"])))
                and observation["count"] == len(native["events"])
                and observation["head"] == {key: native["events"][-1][key] for key in ("seq", "event_id", "checksum")}
                and all(checkpoint[kind][key] == capture["native_checkpoint"][kind][key] == observation[key]
                        for key in ("count", "head", "sha256")), "final native/observer/capture frontier differs")
    require(checkpoint["ledger_verified"] is True and all(stop["admission_before_restore"]["admission"][key] == after["observation"][key]
            for key in ("count", "head", "sha256")), "stopped final admission anchor differs")
    prior_root = (source_root / original["resume"]["receipt_root"]).resolve()
    initial_count = original["initial_observer"]["session"]["count"]
    prior_session, prior_admission = pinned(prior_root / "final-session.json"), pinned(prior_root / "final-admission.json")
    require(len(prior_session["events"]) == initial_count and events[:initial_count] == prior_session["events"]
            and len(prior_admission["events"]) == original["initial_observer"]["admission"]["count"]
            and after["events"][:len(prior_admission["events"])] == prior_admission["events"]
            and before["events"] == after["events"][:len(before["events"])]
            and capture["status"] == "CUMULATIVE_SNAPSHOTS_CAPTURED" and capture["errors"] == []
            and capture["unknown_runs"] == [] and capture["input_drift"] == []
            and capture["terminal_inventory_unchanged"] is True
            and capture["absent_native_suffixes_unchanged"] is True
            and capture["actual_runs"] == len(run_ids) and capture["inputs_sha256"] == refs["original_inputs"]["sha256"]
            and capture["segment"] == original["identity"] and capture["source_commit"] == original["source_commit"]
            and capture["execution_start"] == original["frozen_at"]
            and [row["run_id"] for row in capture["runs"]] == run_ids, "complete frozen prefix/capture denominator differs")
    terminal_by_run = {event["payload"]["run_id"]: event for event in terminals}
    capture_by_run = {row["run_id"]: row for row in capture["runs"]}
    require(all(row["capture_state"] == "CAPTURED" and row["native_terminal_event_id"] == terminal_by_run[row["run_id"]]["event_id"]
                and row["native_terminal_checksum"] == terminal_by_run[row["run_id"]]["checksum"]
                and row["outcome"] == native_outcomes[row["run_id"]] for row in capture["runs"])
            and joined["segments"][-1]["segment"] == original["identity"]
            and joined["segments"][-1]["source_commit"] == original["source_commit"]
            and joined["segments"][-1]["actual_runs"] == sum(event["seq"] >= initial_count for event in continued),
            "capture terminal/contained segment denominator differs")
    roots = tuple((row["run_id"], row["trace_id"], row["span_id"]) for row in joined["terminal_root_bindings"])
    require(len(roots) == len(run_ids) and {row[0] for row in roots} == set(run_ids)
            and all(row["terminal_event_id"] == terminal_by_run[row["run_id"]]["event_id"] for row in joined["terminal_root_bindings"]),
            "native terminal/Go root binding differs")
    utc = lambda value: datetime.fromisoformat(value.replace("Z", "+00:00"))
    launch_time = max(utc(child["started_utc"]) for child in children)
    require(utc(original["frozen_at"]) <= utc(start["started_utc"]) <= launch_time, "owned launch time differs")
    contained = frozenset(event["payload"]["run_id"] for event in continued
        if reviewed_core and source_sha in _V3_MANAGED_SOURCE_INVENTORIES
        and event["seq"] >= initial_count and terminal_by_run[event["payload"]["run_id"]]["seq"] >= event["seq"]
        and launch_time <= utc(event["created_at"]) <= utc(terminal_by_run[event["payload"]["run_id"]]["created_at"])
        and capture_by_run[event["payload"]["run_id"]]["segment"] == original["identity"]
        and capture_by_run[event["payload"]["run_id"]]["source_commit"] == original["source_commit"])
    retained_refs = refs.get("retained_segment_proofs", [])
    require(isinstance(retained_refs, list), "retained segment references must be a list")
    retained, source_bindings = {}, [(run, original["identity"], original["source_commit"]) for run in sorted(contained)]
    retained_scopes, retained_inputs = [], set()
    for index, prior_refs in enumerate(retained_refs):
        require(isinstance(prior_refs, dict) and set(prior_refs) == {"original_inputs", "native_join", "capture_index"}, "typed retained segment references required")
        prior_original, prior_path = load_ref(prior_refs["original_inputs"])
        prior_join, _ = load_ref(prior_refs["native_join"])
        require(prior_path != original_path and prior_path not in retained_inputs, "duplicate or current retained segment")
        retained_inputs.add(prior_path)
        prior_paths = prior_join["native_runs"][0]["command"][1:4]
        prior_documents = {key: Path(path).read_bytes() for key, path in zip(("session_events", "budget_before", "budget_after"), prior_paths)}
        prior_sources = {key: json.loads(raw) for key, raw in prior_documents.items()}
        prior_runs = [event["payload"]["run_id"] for event in prior_sources["session_events"]["events"] if event["type"] == "session/continued"]
        prior_sources["expected"] = _expectation(prior_sources["session_events"], prior_sources["budget_before"], prior_sources["budget_after"], prior_runs)
        prior_args = argparse.Namespace(**dict(zip(("session_events", "budget_before", "budget_after"), prior_paths)),
                                        contract_version="product-integrity-v3", run_id=prior_runs)
        prior_receipt = {"input_sha256": {}}
        prior_proof = _v3_authority(_v3_projection(prior_original) | {"product_integrity_v3": {"schema_version": 1} | prior_refs},
            prior_sources, prior_documents, prior_args, prior_receipt, source_root)
        require(prior_proof.session_id == events[0]["session_id"] and prior_proof.model == original["model"]
                and prior_proof.run_ids == tuple(run_ids[:len(prior_runs)])
                and prior_documents["budget_before"] == documents["budget_before"]
                and prior_sources["session_events"]["events"] == events[:len(prior_sources["session_events"]["events"])]
                and prior_sources["budget_after"]["events"] == after["events"][:len(prior_sources["budget_after"]["events"])]
                and set(prior_proof.terminal_roots) <= set(roots) and bool(prior_proof.contained_runs)
                and not prior_proof.contained_runs & (contained | retained.keys()), "retained segment is not a disjoint immutable final prefix")
        for run in prior_proof.contained_runs:
            retained[run] = prior_proof
        source_bindings.extend(prior_proof.source_bindings)
        retained_scopes.append(prior_receipt["intent_authority_scope_reuse"])
        receipt["input_sha256"].update(prior_receipt["input_sha256"])
    all_contained = contained | frozenset(retained)
    captured_spans, bodies = set(), []
    for run in sorted(all_contained):
        row = capture_by_run[run]
        artifact_path = (capture_path.parent / row["artifact"]).resolve()
        require(artifact_path.is_relative_to(capture_path.parent), "capture artifact escapes frozen snapshot")
        snapshot, _ = load_ref({"path": str(artifact_path), "sha256": row["sha256"]})
        require(all(snapshot[key] == row[key] for key in ("run_id", "segment", "source_commit",
                    "native_terminal_event_id", "native_terminal_checksum"))
                and len(snapshot["spans"]) == row["spans"], "contained capture body/denominator differs")
        if run in retained:
            require((run, row["segment"], row["source_commit"]) in retained[run].source_bindings, "retained fresh capture source differs")
        eligible = [span for span in snapshot["spans"] if run not in retained
                    or _no_observed_presence(span) and _intent_base_key(span) in retained[run].captured_spans]
        full_keys = [(span["trace_id"], span["span_id"], digest(json.dumps(span, sort_keys=True, separators=(",", ":")).encode())) for span in eligible]
        captured_spans.update(key for key in full_keys if run not in retained or key in retained[run].captured_spans)
        captured_spans.update(_intent_base_key(span) for span in eligible if _no_observed_presence(span))
        bodies.extend(eligible)
    reservations = [event["payload"] for event in after["events"] if event["type"] == "admission/call_reserved"]
    all_ids = [fact["call_id"] for fact in reservations]
    require(len(all_ids) == len(set(all_ids)), "duplicate global native admission identity")
    selected_owners = {fact["owner_id"] for fact in reservations if fact["task_id"] == events[0]["session_id"]}
    require(len(selected_owners) == 1, "admission owner authority differs")
    calls = tuple(sorted((call_id, purpose, sources["expected"].get("retry_of", {}).get(call_id, ""))
                         for call_id, purpose in sources["expected"]["model_calls"].items()))
    evidence = (("join", refs["native_join"]["sha256"]), ("inputs", refs["original_inputs"]["sha256"]),
                ("manifest", original["bundle_manifest_sha256"]), ("capture", refs["capture_index"]["sha256"]))
    evidence += tuple((f"retained/{index}/{key}", value["sha256"]) for index, row in enumerate(retained_refs) for key, value in sorted(row.items()))
    proof = ProductIntentAuthority(events[0]["session_id"], tuple(run_ids), original["model"], calls, frozenset(all_ids), all_contained,
                                   roots, tuple(source_bindings), evidence,
                                   frozenset(captured_spans), _raw_attribute_absence(refs["raw_backend_snapshot"], original, bodies,
                                       frozenset(all_ids), roots, sources, load_ref, require, receipt) if admission else frozenset())
    receipt["intent_authority_scope_reuse"] = {"native_join_sha256": refs["native_join"]["sha256"],
        "native_results": len(native_rows), "native_auditor_sha256": digest(auditor_raw), "global_reserved_calls": len(all_ids),
        "admission_owner_id": next(iter(selected_owners)), "contained_runs": sorted(all_contained), "retained_segments": retained_scopes,
        "source_commit": original["source_commit"], "source_inventory_sha256": source_sha,
        "managed_variant": "reviewed" if reviewed_core and source_sha in _V3_MANAGED_SOURCE_INVENTORIES else "unconfirmed; admitted native comparisons only",
        "capture_order": "post-terminal/pre-owned-stop; final native heads rechecked; span wall timestamps not retained",
        "original_unconfirmed_span_execution_sources": joined["unconfirmed_span_execution_sources"],
        "full_product_acceptance": False, "exact_failure_stage": "unknown", "billing": "unknown"}
    return proof


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("phoenix-url", "project", "started-at", "session-events", "budget-before", "budget-after", "inputs", "binary", "output"):
        parser.add_argument("--" + name, required=True)
    parser.add_argument("--run-id", required=True, action="append", help="repeat for all runs in a multi-round coding task")
    parser.add_argument("--attempts", type=int, default=6)
    parser.add_argument("--interval", type=float, default=1)
    parser.add_argument("--contract-version", choices=("product-v1", "product-integrity-v2", "product-integrity-v3", "product-integrity-v3-admission-v1"), default="product-v1")
    parser.add_argument("--runtime-manifest", help="original pinned bundle manifest, required for integrity v2")
    args = parser.parse_args(argv)
    admission = args.contract_version == "product-integrity-v3-admission-v1"
    integrity = args.contract_version in {"product-integrity-v2", "product-integrity-v3"} or admission
    intents = args.contract_version == "product-integrity-v3" or admission
    origin = urlsplit(args.phoenix_url)
    if origin.scheme not in {"http", "https"} or not origin.hostname or origin.username or origin.password or origin.query or origin.fragment or origin.path not in {"", "/"}:
        parser.error("Phoenix must be an HTTP(S) origin without credentials")
    if not 1 <= args.attempts <= 10 or not 0 <= args.interval <= 10:
        parser.error("bounded attempts and interval required")
    if find_secret_leak(args.project) or find_secret_leak(args.run_id):
        parser.error("opaque non-sensitive project and run identities required")
    if len(set(args.run_id)) != len(args.run_id):
        parser.error("distinct run identities required")
    output = Path(args.output)
    output.mkdir(parents=True)
    receipt = {"schema_version": 1, "scope": "product-trace-readback", "status": "BLOCKED", "capture_state": "unknown",
               "backend_readback": False, "backend_origin": args.phoenix_url.rstrip("/"), "project": args.project,
               "attempts": [], "input_sha256": {}}
    if integrity:
        receipt.update(schema_version=2, scope="product-trace-integrity")
    if intents:
        receipt.update(schema_version=3, scope="product-trace-integrity-v3", full_product_acceptance=False)
    if admission:
        receipt.update(scope="go-only-admission-classification", metadata_complete=False)
    try:
        sources, documents = {}, {}
        for key in ("session_events", "budget_before", "budget_after", "inputs"):
            data = Path(getattr(args, key)).read_bytes()
            receipt["input_sha256"][key] = hashlib.sha256(data).hexdigest()
            documents[key] = data
            sources[key] = json.loads(data.decode("utf-8-sig"))
        pins = sources["inputs"]
        if find_secret_leak(pins):
            raise ValueError("frozen input contains a credential shape")
        fingerprints = pins.get("source_sha256")
        if (not isinstance(fingerprints, dict) or not fingerprints
                or any(not isinstance(value, str) or not re.fullmatch(r"[0-9a-f]{64}", value) for value in fingerprints.values())
                or not re.fullmatch(r"[0-9a-f]{64}", str(pins.get("binary_sha256", "")))
                or not re.fullmatch(r"sha256:[0-9a-f]{64}", str(pins.get("task_image", "")))
                or not re.fullmatch(r"sha256:[0-9a-f]{64}", str(pins.get("trace_backend", {}).get("image", "")))
                or not isinstance(pins.get("model"), str) or not pins["model"] or find_secret_leak(pins["model"])):
            raise ValueError("source, binary, task image, model and backend pins required")
        source_root = Path(__file__).resolve().parents[1]
        for name, checksum in fingerprints.items():
            source = (source_root / name).resolve()
            if not source.is_relative_to(source_root) or (not integrity and hashlib.sha256(source.read_bytes()).hexdigest() != checksum):
                raise ValueError("runtime source differs from frozen inputs")
        if integrity:
            if not args.runtime_manifest:
                raise ValueError("original runtime manifest required")
            manifest_path = Path(args.runtime_manifest).resolve()
            data = manifest_path.read_bytes()
            manifest_sha = hashlib.sha256(data).hexdigest()
            manifest = json.loads(data.decode("utf-8-sig"))
            compiled = manifest.get("source_files")
            if (not isinstance(compiled, dict) or not compiled
                    or any(not isinstance(name, str) or Path(name).is_absolute()
                           or not (source_root / name).resolve().is_relative_to(source_root)
                           or not isinstance(checksum, str) or not re.fullmatch(r"[0-9a-f]{64}", checksum)
                           for name, checksum in compiled.items())):
                raise ValueError("pinned runtime source manifest is malformed")
            recorded = pins.get("compiled_source_files")
            if not isinstance(recorded, dict):
                recorded = {name: fingerprints.get(name) for name in compiled or {}}
            binary = Path(args.binary).resolve()
            if (manifest_sha != pins.get("bundle_manifest_sha256") or not compiled or recorded != compiled
                    or not re.fullmatch(r"[0-9a-f]{40}", str(pins.get("source_commit", "")))
                    or manifest.get("source_commit") != pins["source_commit"]
                    or not binary.is_relative_to(manifest_path.parent)
                    or manifest.get("files", {}).get(binary.relative_to(manifest_path.parent).as_posix()) != pins["binary_sha256"]):
                raise ValueError("original runtime build binding differs from frozen inputs")
            receipt["runtime_binding"] = {"manifest_sha256": manifest_sha, "source_commit": pins["source_commit"],
                                          "source_sha256": compiled, "binary_sha256": pins["binary_sha256"]}
            receipt["analysis_source_sha256"] = {name: hashlib.sha256((source_root / name).read_bytes()).hexdigest()
                for name in ("eval/product_trace.py", "eval/harness/trace_contract.py", "eval/harness/phoenix.py")}
        if hashlib.sha256(Path(args.binary).read_bytes()).hexdigest() != pins["binary_sha256"]:
            raise ValueError("product binary differs from frozen inputs")
        receipt["pins_verified"] = {"source_files": len(compiled) if integrity else len(fingerprints), "binary_sha256": pins["binary_sha256"],
                                    "task_image": pins["task_image"], "model": pins["model"],
                                    "trace_backend": {key: pins["trace_backend"][key] for key in ("image", "id", "project") if key in pins["trace_backend"]}}
        if integrity and not intents:
            with tempfile.TemporaryDirectory(prefix="product-trace-native-") as directory:
                audit_binary = Path(directory) / "trace-audit.exe"
                subprocess.run(["go", "build", "-o", str(audit_binary), "./cmd/trace-audit"], cwd=source_root,
                               capture_output=True, check=True, timeout=120)
                receipt["native_audit_sha256"] = hashlib.sha256(audit_binary.read_bytes()).hexdigest()
                for run_id in args.run_id:
                    _verify_native_exports(documents, run_id, audit_binary)
        elif not intents:
            for run_id in args.run_id:
                _verify_native_exports(documents, run_id)
        expected = _expectation(sources["session_events"], sources["budget_before"], sources["budget_after"], args.run_id)
        expected["model"] = pins["model"]
        if intents:
            sources["expected"] = expected
            expected["intent_authority"] = _v3_authority(pins, sources, documents, args, receipt, source_root)
        if find_secret_leak(expected):
            raise ValueError("product call identity contains a credential shape")
        receipt["model_usage"] = {"state": "unknown" if expected["unsettled_model_calls"] else "complete",
                                  "unsettled_call_ids": expected["unsettled_model_calls"]}
        for attempt in range(args.attempts):
            try:
                spans, unavailable = [], []
                inconsistent = False
                for run_id in args.run_id:
                    try:
                        captured = read_product_spans(args.phoenix_url, args.project, args.started_at, expected["session_id"], run_id,
                            **({"retain_intent_evidence": True} if intents else {}))
                        if integrity:
                            for trace_id in sorted({span.trace_id for span in captured}):
                                previous = sorted((span for span in spans if span.trace_id == trace_id), key=lambda span: span.span_id)
                                current = sorted((span for span in captured if span.trace_id == trace_id), key=lambda span: span.span_id)
                                # Reuse identical full trees; retain conflicting snapshots for rejection.
                                if current != previous:
                                    inconsistent = inconsistent or bool(previous)
                                    spans.extend(current)
                        else:
                            spans.extend(captured)
                    except Exception as error:
                        unavailable.append({"run_id": run_id, "error": type(error).__name__})
                report = evaluate_product_trace(spans, expected, contract_version=args.contract_version)
                if inconsistent:
                    report["verdict"] = "FAIL"
                    report["problems"].append("inconsistent backend snapshots for a shared trace")
                legacy_report = evaluate_product_trace(spans, expected) if integrity else None
                previous_integrity_report = evaluate_product_trace(spans, expected, contract_version="product-integrity-v2") if intents else None
                strict_integrity_report = evaluate_product_trace(spans, expected, contract_version="product-integrity-v3") if admission else None
                capture = json.dumps({"spans": [span.to_dict() for span in spans], "report": report,
                                      **({"legacy_report": legacy_report} if integrity else {}),
                                      **({"previous_integrity_report": previous_integrity_report} if intents else {}),
                                      **({"strict_integrity_report": strict_integrity_report} if admission else {})}, indent=2).encode()
                name = f"readback-{attempt + 1}.json"
                with (output / name).open("xb") as stream:
                    stream.write(capture)
                result = {"verdict": report["verdict"], "artifact": name, "sha256": hashlib.sha256(capture).hexdigest()}
                if unavailable:
                    result.update(unavailable_runs=unavailable, error=unavailable[0]["error"])
                receipt["attempts"].append(result)
                if spans or not receipt["backend_readback"]:
                    receipt.update(capture_state="complete" if report["verdict"] == "PASS" and not unavailable else "partial" if spans else "unknown",
                                   backend_readback=bool(spans), report=report)
                    if integrity:
                        receipt["legacy_report"] = legacy_report
                    if intents:
                        receipt["previous_integrity_report"] = previous_integrity_report
                    if admission:
                        receipt["strict_integrity_report"] = strict_integrity_report
                        if report["verdict"] == "PASS" and not unavailable:
                            receipt["capture_state"] = "admission-accounted"
                if report["verdict"] == "PASS" and not unavailable:
                    if not expected["unsettled_model_calls"]:
                        receipt["status"] = "VERIFIED"
                    break
            except Exception as error:
                receipt["attempts"].append({"error": type(error).__name__})
            if attempt + 1 < args.attempts:
                time.sleep(args.interval)
    except Exception as error:
        receipt["input_error"] = type(error).__name__
    with (output / "receipt.json").open("x", encoding="utf-8") as stream:
        json.dump(receipt, stream, indent=2)
        stream.write("\n")
    print(json.dumps({"status": receipt["status"], "capture_state": receipt["capture_state"], "attempts": len(receipt["attempts"])}))
    return 0 if receipt["status"] == "VERIFIED" else 3


if __name__ == "__main__":
    raise SystemExit(main())
