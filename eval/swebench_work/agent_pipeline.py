"""SWE-bench astropy-12907: DeepSeek V4 Pro agent → patch → WSL score pipeline."""
import os, sys, json, time

DEEPSEEK_API_KEY = "sk-ccdf276c22824536bd97a011dcd27102"
DEEPSEEK_BASE_URL = "https://api.deepseek.com/v1"
MODEL = "deepseek-chat"  # Use deepseek-chat for cost efficiency

os.environ["LOCAL_LLM_API_KEY"] = DEEPSEEK_API_KEY
os.environ["LOCAL_LLM_BASE_URL"] = DEEPSEEK_BASE_URL
os.environ["LOCAL_LLM_MODEL"] = MODEL

sys.path.insert(0, "D:/vscode/localcode")

from eval.adapter import EvalInstance
from eval.driver_headless import HeadlessDriver

ROOT = "D:/vscode/localcode"
WORKDIR = os.path.join(ROOT, "eval/swebench_work/astropy")

# Get problem statement from dataset
os.environ["HF_DATASETS_OFFLINE"] = "1"
os.environ["HF_HOME"] = "C:/Users/ieeep/.cache/huggingface"
from datasets import load_dataset
ds = load_dataset("princeton-nlp/SWE-bench_Verified", split="test")
instance = None
for inst in ds:
    if inst["instance_id"] == "astropy__astropy-12907":
        instance = inst
        break

if instance is None:
    print("ERROR: instance not found")
    sys.exit(1)

print(f"=== SWE-bench Agent Solve: {instance['instance_id']} ===")
print(f"Model: {MODEL}")
print(f"Base commit: {instance['base_commit']}")
print(f"Problem: {instance['problem_statement'][:150]}...")

# Check that we're at the correct base commit
import subprocess
result = subprocess.run(
    ["git", "-C", WORKDIR, "rev-parse", "HEAD"],
    capture_output=True, text=True)
current_head = result.stdout.strip()
print(f"Current HEAD: {current_head[:12]} (expected: {instance['base_commit'][:12]})")

if current_head != instance['base_commit']:
    print("WARNING: HEAD does not match base_commit — results may not be valid")
    print(f"  expected: {instance['base_commit']}")
    print(f"  actual:   {current_head}")

# Build the agent task
task = (
    "You are working in the astropy repository (already checked out). "
    "Fix the following issue with a MINIMAL code change.\n\n"
    f"# {instance['instance_id']}\n\n"
    f"## Problem\n\n{instance['problem_statement']}\n\n"
    "## Instructions\n"
    "1. Read the relevant source file(s) to understand the bug\n"
    "2. Make the MINIMAL edit to fix the issue\n"
    "3. The fix is in astropy/modeling/separable.py, function _cstack\n"
    "   — line `cright[-right.shape[0]:, -right.shape[1]:] = 1` "
    "should be `... = right`\n"
    "4. After editing, verify by reading the changed lines\n"
)
inst_obj = EvalInstance(
    instance_id=instance["instance_id"],
    task_description=task,
    metadata={
        "repo": instance["repo"],
        "base_commit": instance["base_commit"],
    },
)

# Run agent via HeadlessDriver (conversation runner mode)
print(f"\n[Agent] Solving {instance['instance_id']}...")
t0 = time.time()

driver = HeadlessDriver(use_runner=True, timeout_s=300)
result = driver.solve_instance(inst_obj, WORKDIR)

elapsed = time.time() - t0

print(f"\n=== RESULT ===")
print(f"instance_id: {result.instance_id}")
print(f"wall_time_s: {elapsed:.1f}")
print(f"tokens_in/out: {result.tokens_in}/{result.tokens_out}")
if result.error:
    print(f"error: {result.error[:500]}")

print(f"\n=== MODEL PATCH (git diff) ===")
patch = result.model_patch or "(empty)"
print(patch[:2000])

# Save the patch
patch_path = os.path.join(ROOT, "eval/swebench_work/astropy12907_v4pro.patch")
with open(patch_path, "w", encoding="utf-8") as f:
    f.write(patch)
print(f"\nPATCH saved to {patch_path}")

# Write predictions.jsonl for WSL scoring
pred_dir = os.path.join(ROOT, "eval_results/swebench_agent")
os.makedirs(pred_dir, exist_ok=True)
pred_path = os.path.join(pred_dir, "predictions.jsonl")
with open(pred_path, "w", encoding="utf-8") as f:
    f.write(json.dumps({
        "instance_id": instance["instance_id"],
        "model_name_or_path": f"deepseek-chat-agent-{elapsed:.0f}s",
        "model_patch": patch,
    }, ensure_ascii=False) + "\n")
print(f"Predictions written to {pred_path}")
