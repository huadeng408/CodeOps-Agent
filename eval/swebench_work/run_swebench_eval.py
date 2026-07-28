#!/usr/bin/env python3
"""Run SWE-bench official evaluation for flask-5014 with the V4 Pro patch.

Usage (inside a Linux Docker container with docker.sock mounted):
    docker run --rm -v /var/run/docker.sock:/var/run/docker.sock \
      -v D:/vscode/localcode/eval/swebench_work:/work -w /work \
      python:3.11-slim python /work/run_swebench_eval.py
"""
import json
import subprocess
import sys
import time
from pathlib import Path

WORK = Path(__file__).resolve().parent if "__file__" in dir() else Path("/work")
PATCH = WORK / "flask5014_v4pro.patch"
PREDS = WORK / "predictions_v4pro.jsonl"
INSTANCE_ID = "pallets__flask-5014"

def main() -> int:
    print(f"=== SWE-bench harness eval for {INSTANCE_ID} ===", flush=True)

    # 1) Format V4 Pro patch into predictions.jsonl
    if not PATCH.exists():
        print(f"ERROR: patch not found: {PATCH}", file=sys.stderr)
        return 1
    patch = PATCH.read_text(encoding="utf-8")
    prediction = {
        "instance_id": INSTANCE_ID,
        "model_name_or_path": "deepseek-v4-pro-localcode",
        "model_patch": patch,
    }
    PREDS.write_text(json.dumps(prediction) + "\n", encoding="utf-8")
    print(f"WROTE {PREDS} ({len(patch)} byte patch)", flush=True)

    # 2) Install swebench + datasets if not already
    #    (assumes pip already available in container)
    subprocess.run(
        [sys.executable, "-m", "pip", "install", "-q",
         "swebench==4.1.0", "datasets"],
        check=True, timeout=300,
    )

    # 3) Run official evaluation
    print(f"\n=== Running official swebench evaluation ===", flush=True)
    t0 = time.time()
    r = subprocess.run(
        [sys.executable, "-m", "swebench.harness.run_evaluation",
         "--dataset_name", "princeton-nlp/SWE-bench_Verified",
         "--predictions_path", str(PREDS),
         "--max_workers", "1",
         "--run_id", "localcode-v4pro",
         "--namespace", "flask",
         "--instance_ids", INSTANCE_ID,
         ],
        capture_output=True, text=True, timeout=600,
    )
    elapsed = time.time() - t0
    print(f"\n=== Harness completed in {elapsed:.0f}s (exit {r.returncode}) ===", flush=True)
    if r.stdout:
        print("STDOUT:", r.stdout[:2000], flush=True)
    if r.stderr:
        print("STDERR:", r.stderr[:2000], flush=True)

    # 4) Parse results
    results_dir = WORK / "results" / "localcode-v4pro" / INSTANCE_ID
    results_json = results_dir / "report.json"
    if results_json.exists():
        report = json.loads(results_json.read_text(encoding="utf-8"))
        resolved = report.get(INSTANCE_ID, {}).get("resolved", False)
        print(f"\n=== RESULT: resolved={resolved} ===", flush=True)
        print(json.dumps(report.get(INSTANCE_ID, {}), indent=2, ensure_ascii=False), flush=True)
        return 0 if resolved else 1
    else:
        print(f"\nNo report at {results_json}. Checking alt locations...", flush=True)
        alt = list(Path(WORK).rglob("*.json"))
        for a in alt:
            print(f"  found: {a}", flush=True)
            d = json.loads(a.read_text(encoding="utf-8") or "{}")
            if INSTANCE_ID in d:
                resolved = d[INSTANCE_ID].get("resolved", False)
                print(f"    resolved={resolved} in {a.name}", flush=True)
        return 2

if __name__ == "__main__":
    sys.exit(main())
