"""Honest SWE-bench agent test: only problem statement, no hints, no answers.

The agent gets:
  - The problem statement from SWE-bench_Verified (exactly as in the dataset)
  - A git checkout at the correct base_commit
  - Tools: Read, Write, Edit, Bash, Glob, Grep

What it does NOT get:
  - No hint about which file to edit
  - No hint about what the fix is
  - No ground-truth patch

The agent's task is: read the code, understand the bug, fix it, and stop.
"""
import os, sys, json, time, subprocess
from pathlib import Path

# ---- Config ----
DEEPSEEK_API_KEY = "sk-ccdf276c22824536bd97a011dcd27102"
DEEPSEEK_BASE_URL = "https://api.deepseek.com/v1"
MODEL = "deepseek-chat"
INSTANCE_ID = "astropy__astropy-12907"

os.environ["LOCAL_LLM_API_KEY"] = DEEPSEEK_API_KEY
os.environ["LOCAL_LLM_BASE_URL"] = DEEPSEEK_BASE_URL
os.environ["LOCAL_LLM_MODEL"] = MODEL

ROOT = Path("D:/vscode/localcode")
WORKDIR = ROOT / "eval/swebench_work/astropy"

sys.path.insert(0, str(ROOT))

from eval.adapter import EvalInstance
from eval.driver_headless import HeadlessDriver

# ---- Step 1: Load the problem statement from the dataset ----
os.environ["HF_DATASETS_OFFLINE"] = "1"
os.environ["HF_HOME"] = "C:/Users/ieeep/.cache/huggingface"
from datasets import load_dataset

ds = load_dataset("princeton-nlp/SWE-bench_Verified", split="test")
instance_data = None
for inst in ds:
    if inst["instance_id"] == INSTANCE_ID:
        instance_data = inst
        break

if instance_data is None:
    print(f"FATAL: instance {INSTANCE_ID} not found in dataset")
    sys.exit(1)

print(f"=== SWE-bench Honest Agent Test ===")
print(f"Instance: {INSTANCE_ID}")
print(f"Model: {MODEL}")
print(f"Base commit: {instance_data['base_commit'][:12]}")

# ---- Step 2: Verify correct checkout ----
result = subprocess.run(
    ["git", "-C", str(WORKDIR), "rev-parse", "HEAD"],
    capture_output=True, text=True)
current_head = result.stdout.strip()
if current_head != instance_data["base_commit"]:
    print(f"WARNING: HEAD mismatch (current={current_head[:12]}, expected={instance_data['base_commit'][:12]})")
else:
    print(f"HEAD confirmed: {current_head[:12]}")

# Verify no uncommitted changes
result = subprocess.run(
    ["git", "-C", str(WORKDIR), "status", "--porcelain"],
    capture_output=True, text=True)
if result.stdout.strip():
    print(f"WARNING: working tree has uncommitted changes! Resetting...")
    subprocess.run(["git", "-C", str(WORKDIR), "checkout", "--", "."], check=True)

# ---- Step 3: Build the task description (PROBLEM ONLY, NO HINTS) ----
# This is exactly what the dataset says — no more, no less.
task = (
    "You are a software engineer fixing a bug in the astropy repository.\n"
    "The repository is already checked out at the correct version.\n\n"
    f"## Issue\n\n{instance_data['problem_statement']}\n\n"
    "## Instructions\n\n"
    "1. Read the relevant source files to understand the code and the bug.\n"
    "2. Find the root cause and make the MINIMAL code change to fix it.\n"
    "3. Do NOT generate tests, docs, or any other changes — just the fix.\n"
    "4. When you're done, state clearly what you changed and why.\n"
    "5. Do NOT add debug prints or comments unrelated to the fix.\n"
)

print(f"\n## Task description (exactly as given to agent):")
print("-" * 60)
print(task[:500])
print(f"... ({len(task)} chars total)")
print("-" * 60)

inst_obj = EvalInstance(
    instance_id=INSTANCE_ID,
    task_description=task,
    metadata={
        "repo": instance_data["repo"],
        "base_commit": instance_data["base_commit"],
    },
)

# ---- Step 4: Run the agent ----
print(f"\n[Agent] Starting honest solve (NO HINTS, tools only)...")
t0 = time.time()

driver = HeadlessDriver(use_runner=True, timeout_s=600)
result = driver.solve_instance(inst_obj, str(WORKDIR))

elapsed = time.time() - t0

# ---- Step 5: Check what the agent actually changed ----
print(f"\n=== AGENT RESULT ===")
print(f"instance_id: {result.instance_id}")
print(f"wall_time_s: {elapsed:.1f}")
print(f"tokens_in/out: {result.tokens_in}/{result.tokens_out}")
if result.error:
    print(f"error: {result.error[:800]}")

# Get the actual git diff
diff_result = subprocess.run(
    ["git", "-C", str(WORKDIR), "diff", "HEAD"],
    capture_output=True, text=True)
actual_diff = diff_result.stdout

print(f"\n=== ACTUAL GIT DIFF (what the agent really did) ===")
if actual_diff.strip():
    print(actual_diff[:3000])
else:
    print("(NO CHANGES — agent did not modify any files)")

# ---- Step 6: Check against ground truth ----
# The ground truth for astropy__astropy-12907:
# In _cstack() in astropy/modeling/separable.py:
#   cright[-right.shape[0]:, -right.shape[1]:] = 1
# should be:
#   cright[-right.shape[0]:, -right.shape[1]:] = right
GT_FILE = "astropy/modeling/separable.py"
GT_BUG = "cright[-right.shape[0]:, -right.shape[1]:] = 1"
GT_FIX = "cright[-right.shape[0]:, -right.shape[1]:] = right"

print(f"\n=== HONESTY CHECK ===")
if actual_diff.strip():
    # Check if the diff contains the file we expect
    if GT_FILE in actual_diff:
        print(f"✅ Edited the correct file: {GT_FILE}")
    else:
        print(f"❌ Did NOT edit the expected file ({GT_FILE})")
        print(f"   Files changed: {[l for l in actual_diff.splitlines() if l.startswith('---') or l.startswith('+++')]}")

    # Check if the fix replaces = 1 with = right
    if GT_BUG in actual_diff and GT_FIX not in actual_diff:
        # It's removing the buggy line but not fixing it correctly
        print(f"⚠️  Removed buggy line but may not have correct replacement")
    elif GT_BUG not in actual_diff and GT_FIX in actual_diff:
        # Bug is already gone, fix is present — agent applied correct fix
        print(f"✅ Exact match: fix applied correctly")
    elif GT_BUG in actual_diff and GT_FIX in actual_diff:
        print(f"✅ Correct fix: replaced '= 1' with '= right'")
    else:
        print(f"⚠️  Diff does not look like the expected fix — check manually")
else:
    print(f"❌ Agent made NO changes to the code")

# Save predictions for WSL scoring
pred_dir = ROOT / "eval_results/swebench_agent_honest"
pred_dir.mkdir(parents=True, exist_ok=True)
pred_path = pred_dir / "predictions.jsonl"
with open(pred_path, "w", encoding="utf-8") as f:
    f.write(json.dumps({
        "instance_id": INSTANCE_ID,
        "model_name_or_path": f"deepseek-chat-honest-{elapsed:.0f}s",
        "model_patch": actual_diff,
    }, ensure_ascii=False) + "\n")

# Also save full agent output for inspection
log_path = pred_dir / "agent_log.json"
with open(log_path, "w", encoding="utf-8") as f:
    json.dump({
        "instance_id": INSTANCE_ID,
        "model": MODEL,
        "wall_time_s": elapsed,
        "tokens_in": result.tokens_in,
        "tokens_out": result.tokens_out,
        "error": result.error,
        "model_patch": actual_diff,
        "task_description": task,
    }, f, indent=2, ensure_ascii=False)

print(f"\nPredictions saved to: {pred_path}")
print(f"Full log saved to: {log_path}")
print(f"\nRun: wsl -d Ubuntu-24.04 -- bash -c 'cd /d/vscode/localcode && python3 eval/swebench_work/score_honest.py'")
