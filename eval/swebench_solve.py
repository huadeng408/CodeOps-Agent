"""SWE-bench flask-5014 agent solve via HeadlessDriver (ConversationRunner) on V4 Pro.

Runs the real localcode orchestrator (ConversationRunner + local tool executor)
against the cloned flask repo with the issue as the task, then captures git diff.
"""
import json
import os
import sys

os.environ["LOCAL_LLM_BASE_URL"] = os.environ.get("LOCAL_LLM_BASE_URL", "https://api.deepseek.com/v1")
os.environ["LOCAL_LLM_MODEL"] = os.environ.get("LOCAL_LLM_MODEL", "deepseek-v4-pro")
# Never hardcode keys in source. Read from env (e.g. .env.local loaded by the
# orchestrator) and fail loudly if absent instead of shipping a real key.
_api_key = os.environ.get("LOCAL_LLM_API_KEY")
if not _api_key:
    raise SystemExit("LOCAL_LLM_API_KEY is not set; put it in .env.local or export it before running.")
os.environ["LOCAL_LLM_API_KEY"] = _api_key

sys.path.insert(0, "D:/vscode/localcode")

from eval.adapter import EvalInstance
from eval.driver_headless import HeadlessDriver

ROOT = "D:/vscode/localcode"
d = json.load(open(os.path.join(ROOT, "eval/swebench_flask5014.json"), encoding="utf-8"))

# Agent-friendly task framing.
task = (
    "You are working in the Flask repository (already checked out). Fix the following issue.\n\n"
    f"# {d['instance_id']}\n\n## Problem\n\n{d['problem_statement']}\n\n"
    "## Task\nRead the relevant source file(s), make the MINIMAL edit to fix the issue, "
    "and stop. The fix should raise a ValueError when a Blueprint is created with an empty name."
)
inst = EvalInstance(
    instance_id=d["instance_id"],
    task_description=task,
    metadata={"repo": d["repo"], "base_commit": d["base_commit"]},
)

driver = HeadlessDriver(use_runner=True, timeout_s=180)
workdir = os.path.join(ROOT, "eval/swebench_work/flask")

print(f"=== Solving {d['instance_id']} with deepseek-v4-pro (ConversationRunner) ===", flush=True)
print(f"workdir: {workdir}", flush=True)
result = driver.solve_instance(inst, workdir)

print(f"\n=== RESULT ===", flush=True)
print(f"instance_id: {result.instance_id}", flush=True)
print(f"error: {(result.error[:1000] if result.error else 'none')}", flush=True)
print(f"wall_time_s: {result.wall_time_s}", flush=True)
print(f"tokens_in/out: {result.tokens_in}/{result.tokens_out}", flush=True)
print(f"\n=== MODEL PATCH (git diff) ===\n{result.model_patch or '(empty)'}", flush=True)

# Save the patch.
with open(os.path.join(ROOT, "eval/swebench_work/flask5014_v4pro.patch"), "w", encoding="utf-8") as fh:
    fh.write(result.model_patch or "")
print("\nPATCH saved to eval/swebench_work/flask5014_v4pro.patch", flush=True)
