"""Read a real product run from Phoenix; no synthetic root or release promotion."""
from __future__ import annotations

import argparse
import hashlib
import json
import re
import subprocess
import tempfile
import time
from pathlib import Path
from urllib.parse import urlsplit

from eval.harness.phoenix import read_product_spans
from eval.harness.trace_contract import evaluate_product_trace, find_secret_leak


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
    return {"session_id": session_id, "run_id": run_ids[0], "run_ids": run_ids,
            "completed": all(terminal.get(run_id) == "session/run-completed" for run_id in run_ids),
            "tool_calls_by_run": tools, "model_calls": models,
            "unsettled_model_calls": sorted(models.keys() - settled),
            "retry_of": {fact["call_id"]: fact.get("retry_of", "") for fact in reservations}}


def _verify_native_exports(documents: dict[str, bytes], run_id: str) -> None:
    # Verify the same immutable bytes that are hashed and interpreted below.
    with tempfile.TemporaryDirectory(prefix="product-trace-audit-") as directory:
        paths = []
        for key in ("session_events", "budget_before", "budget_after"):
            path = Path(directory) / (key + ".json")
            path.write_bytes(documents[key])
            paths.append(str(path))
        result = subprocess.run(["go", "run", "./cmd/trace-audit", *paths, run_id],
                                cwd=Path(__file__).resolve().parents[1], capture_output=True, timeout=120)
        if result.returncode:
            raise ValueError("native product audit refused")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("phoenix-url", "project", "started-at", "session-events", "budget-before", "budget-after", "inputs", "binary", "output"):
        parser.add_argument("--" + name, required=True)
    parser.add_argument("--run-id", required=True, action="append", help="repeat for all runs in a multi-round coding task")
    parser.add_argument("--attempts", type=int, default=6)
    parser.add_argument("--interval", type=float, default=1)
    args = parser.parse_args(argv)
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
            if not source.is_relative_to(source_root) or hashlib.sha256(source.read_bytes()).hexdigest() != checksum:
                raise ValueError("runtime source differs from frozen inputs")
        if hashlib.sha256(Path(args.binary).read_bytes()).hexdigest() != pins["binary_sha256"]:
            raise ValueError("product binary differs from frozen inputs")
        receipt["pins_verified"] = {"source_files": len(fingerprints), "binary_sha256": pins["binary_sha256"],
                                    "task_image": pins["task_image"], "model": pins["model"],
                                    "trace_backend": {key: pins["trace_backend"][key] for key in ("image", "id", "project") if key in pins["trace_backend"]}}
        for run_id in args.run_id:
            _verify_native_exports(documents, run_id)
        expected = _expectation(sources["session_events"], sources["budget_before"], sources["budget_after"], args.run_id)
        expected["model"] = pins["model"]
        if find_secret_leak(expected):
            raise ValueError("product call identity contains a credential shape")
        receipt["model_usage"] = {"state": "unknown" if expected["unsettled_model_calls"] else "complete",
                                  "unsettled_call_ids": expected["unsettled_model_calls"]}
        for attempt in range(args.attempts):
            try:
                spans, unavailable = [], []
                for run_id in args.run_id:
                    try:
                        spans.extend(read_product_spans(args.phoenix_url, args.project, args.started_at, expected["session_id"], run_id))
                    except Exception as error:
                        unavailable.append({"run_id": run_id, "error": type(error).__name__})
                report = evaluate_product_trace(spans, expected)
                capture = json.dumps({"spans": [span.to_dict() for span in spans], "report": report}, indent=2).encode()
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
