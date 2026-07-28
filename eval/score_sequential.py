#!/usr/bin/env python3
"""EvalPlus sequential scoring INSIDE Docker (Linux, has 'resource').

Usage (run inside the evalplus-scorer container with /data = the eval/ dir):
    SAMPLES_FILE=/data/samples_humaneval_v4pro.jsonl EVAL_MODEL=deepseek-v4-pro \
      python /data/score_sequential.py humaneval

Reads the samples file (SAMPLES_FILE env, or /data/samples_{dataset}.jsonl), scores
each sample with the official evalplus.check_correctness sandbox, and writes
/data/eval_final_{dataset}_{tag}.json.

Integrity notes (hardened after an audit caught a stale-samples mislabeling):
  - SAMPLES_FILE is read EXPLICITLY; its path + SHA256 + line count are printed at
    start AND recorded in the output JSON, binding the result to the exact samples
    file. This prevents the result from silently reflecting the wrong model's file.
  - base_only=False, fast_check=False (the strict setting) — never weakened.
  - The problem's PREAMBLE (everything in the prompt before `def {entry}` — imports
    AND helper functions) is prepended to the solution before exec. This fixes the
    symmetric NameError artifact on List[...] (23/164 HumanEval) AND on helper
    functions the prompt defines (e.g. is_palindrome in HumanEval/10, poly in /32).
    Applied to every model identically.
  - The old silent `if plus_pass == 0: plus_pass = base_pass` fallback is removed;
    a warning is printed if plus==0 while base>0 (never silently inflated).
"""
import hashlib
import json
import os
import sys
import time

from evalplus.data import (
    get_human_eval_plus,
    get_mbpp_plus,
    get_human_eval_plus_hash,
    get_mbpp_plus_hash,
)
from evalplus.evaluate import check_correctness, get_groundtruth, MBPP_OUTPUT_NOT_NONE_TASKS

dataset = sys.argv[1] if len(sys.argv) > 1 else "humaneval"

MODEL = os.environ.get("EVAL_MODEL", "unknown")
if MODEL == "deepseek-chat":
    TAG = "v3"
elif MODEL == "deepseek-v4-pro":
    TAG = "v4pro"
else:
    import re
    TAG = re.sub(r"[^a-z0-9]+", "-", MODEL.lower()).strip("-") or "unknown"

SAMPLES_FILE = os.environ.get("SAMPLES_FILE") or f"/data/samples_{dataset}.jsonl"

if dataset == "humaneval":
    problems = get_human_eval_plus()
    dhash = get_human_eval_plus_hash()
    not_none = []
else:
    problems = get_mbpp_plus()
    dhash = get_mbpp_plus_hash()
    not_none = MBPP_OUTPUT_NOT_NONE_TASKS

print(f"Loading {dataset}+ ({len(problems)} problems)... [model={MODEL} tag={TAG}]", flush=True)
# get_groundtruth is deferred until after optional batch slicing so the memory
# peak scales with the batch size, not the full dataset (avoids Docker VM OOM
# when get_groundtruth materializes expected outputs for all 378 MBPP problems).


def extract_preamble(prompt: str, entry: str) -> str:
    """Return everything in the prompt before the `def {entry}` line.

    This captures BOTH leading imports (from/import) AND helper functions the
    prompt defines before the entry (e.g. is_palindrome in HumanEval/10, poly in
    HumanEval/32). The model is instructed to start at `def {entry}`, so without
    this prefix a reference to such a name raises NameError at exec time despite
    correct logic. The prefix is applied symmetrically to every model.
    """
    marker = f"def {entry}"
    lines = prompt.splitlines(keepends=True)
    for i, ln in enumerate(lines):
        s = ln.lstrip()
        if s.startswith(marker) and (len(s) == len(marker) or s[len(marker)] in "( :"):
            return "".join(lines[:i])
    return ""


# Load samples from the EXPLICIT path; bind the result to this file's hash.
if not os.path.exists(SAMPLES_FILE):
    sys.exit(f"ERROR: SAMPLES_FILE not found: {SAMPLES_FILE}")
with open(SAMPLES_FILE, "rb") as _fh:
    SAMPLES_SHA = hashlib.sha256(_fh.read()).hexdigest()
samples = {}
samples_lines = 0
for line in open(SAMPLES_FILE, encoding="utf-8"):
    samples_lines += 1
    line = line.strip()
    if not line:
        continue
    r = json.loads(line)
    if "solution" not in r and "completion" not in r:
        sys.exit(f"ERROR: sample for {r.get('task_id')} has neither 'solution' nor 'completion'")
    samples[r["task_id"]] = r.get("solution", r.get("completion", ""))
print(f"SAMPLES_FILE={SAMPLES_FILE}", flush=True)
print(f"SAMPLES_SHA256={SAMPLES_SHA} lines={samples_lines} loaded={len(samples)}", flush=True)

valid_all = sorted(set(problems) & set(samples))

# Optional batching: BATCH_RANGE="lo:hi" slices valid_all to reduce the
# get_groundtruth memory peak. Output file is tagged with the batch index.
BATCH_RANGE = os.environ.get("BATCH_RANGE")
BATCH_IDX = os.environ.get("BATCH_IDX", "")
if BATCH_RANGE:
    lo, hi = BATCH_RANGE.split(":")
    valid_all = valid_all[int(lo):int(hi)]
    print(f"BATCH idx={BATCH_IDX} range={BATCH_RANGE} -> {len(valid_all)} problems", flush=True)

# Compute expected outputs only for THIS batch's problems (memory-bounded).
batch_problems = {tid: problems[tid] for tid in valid_all}
expected = get_groundtruth(batch_problems, dhash, not_none)
print(f"Expected outputs: {len(expected)}", flush=True)

valid = [t for t in valid_all if t in expected]
print(f"Scoring {len(valid)} valid problems...", flush=True)

base_pass = plus_pass = 0
per_task = []
t0 = time.time()

for i, tid in enumerate(valid):
    prob = problems[tid]
    preamble = extract_preamble(prob.get("prompt", ""), prob.get("entry_point", ""))
    sol = samples[tid]
    full_sol = (preamble + sol) if preamble else sol
    rec = {"task_id": tid, "base": False, "plus": False}
    try:
        result = check_correctness(dataset, i, prob, full_sol, expected[tid],
                                   base_only=False, fast_check=False)
        b, p = result.get("base"), result.get("plus")
        if b and isinstance(b, tuple) and b[0] == "pass":
            base_pass += 1
            rec["base"] = True
        if p and isinstance(p, tuple) and p[0] == "pass":
            plus_pass += 1
            rec["plus"] = True
    except Exception as e:
        rec["error"] = str(e)[:200]
    per_task.append(rec)
    if (i + 1) % 50 == 0 or i == 0:
        print(f"  [{i + 1}/{len(valid)}] b={base_pass / (i + 1):.1%} "
              f"p={plus_pass / (i + 1):.1%}", flush=True)

elapsed = time.time() - t0
n = len(valid)

# Integrity guard: never silently inflate plus from base.
if plus_pass == 0 and base_pass > 0:
    print(f"WARNING: plus_pass=0 while base_pass={base_pass} > 0 — "
          f"plus scoring may have failed; NOT copying base into plus.", flush=True)
if plus_pass > base_pass:
    print(f"WARNING: plus_pass ({plus_pass}) > base_pass ({base_pass}) — unexpected.", flush=True)

print(f"\n=== {dataset}+ ({n} problems, {elapsed:.0f}s) [model={MODEL}] ===", flush=True)
print(f"Base pass@1:  {base_pass}/{n} = {base_pass / n:.1%}", flush=True)
print(f"Plus pass@1:  {plus_pass}/{n} = {plus_pass / n:.1%}", flush=True)
print(f"\nSCORE_FINAL: {dataset}+ model={MODEL} base={base_pass / n:.1%} plus={plus_pass / n:.1%}", flush=True)

out_path = f"/data/eval_final_{dataset}_{TAG}" + (f"_batch{BATCH_IDX}" if BATCH_IDX else "") + ".json"
summary = {
    "model": MODEL,
    "dataset": f"{dataset}+",
    "total": n,
    "base_passed": base_pass,
    "plus_passed": plus_pass,
    "base_pass1": round(base_pass / n, 4) if n else 0.0,
    "plus_pass1": round(plus_pass / n, 4) if n else 0.0,
    "preamble_prefix_applied": True,
    "samples_file": SAMPLES_FILE,
    "samples_sha256": SAMPLES_SHA,
    "samples_count": samples_lines,
    "elapsed_s": round(elapsed, 1),
    "timestamp": time.strftime("%Y-%m-%dT%H:%M:%S"),
    "per_task": per_task,
}
with open(out_path, "w", encoding="utf-8") as fh:
    json.dump(summary, fh, ensure_ascii=False, indent=2)
print(f"WROTE: {out_path}", flush=True)
