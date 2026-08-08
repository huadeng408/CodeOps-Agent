#!/usr/bin/env python3
"""Run 1 SWE-bench instance (astropy__astropy-12907) through the HeadlessDriver agent.

Adapted from the orchestrator workflow script with environment fixes:
- model: deepseek-v4-pro (deepseek-chat no longer exists on the DeepSeek API)
- HF_HOME: Windows path (script originally used a WSL /mnt/c path)
- HTTP(S)_PROXY set so the agent's Bash tool can reach the network
"""
import os, sys, json, subprocess
from pathlib import Path

# --- DeepSeek Config ---
DEEPSEEK_API_KEY = "sk-ccdf276c22824536bd97a011dcd27102"
os.environ["LOCAL_LLM_BASE_URL"] = "https://api.deepseek.com/v1"
os.environ["LOCAL_LLM_MODEL"] = "deepseek-v4-pro"
os.environ["LOCAL_LLM_API_KEY"] = DEEPSEEK_API_KEY
os.environ["HF_DATASETS_OFFLINE"] = "1"
os.environ["HF_HOME"] = "C:/Users/ieeep/.cache/huggingface"
os.environ.setdefault("HTTP_PROXY", "http://127.0.0.1:7890")
os.environ.setdefault("HTTPS_PROXY", "http://127.0.0.1:7890")
os.environ.setdefault("http_proxy", "http://127.0.0.1:7890")
os.environ.setdefault("https_proxy", "http://127.0.0.1:7890")

sys.path.insert(0, "D:/vscode/localcode")

from eval.adapter import EvalInstance
from eval.driver_headless import HeadlessDriver
from datasets import load_dataset

INSTANCE_ID = "astropy__astropy-12907"
ROOT = Path("D:/vscode/localcode")
WORKDIR = ROOT / "eval/swebench_work/agent_run/astropy__astropy-12907"
WORKDIR.mkdir(parents=True, exist_ok=True)

print(f"=== Loading instance {INSTANCE_ID} ===", flush=True)
ds = load_dataset("princeton-nlp/SWE-bench_Verified", split="test")
instance_data = None
for inst in ds:
    if inst["instance_id"] == INSTANCE_ID:
        instance_data = inst
        break

if instance_data is None:
    print("ERROR: instance not found", file=sys.stderr)
    sys.exit(1)

task = (
    f"You are working in the {instance_data['repo']} repository. "
    f"Fix the following bug by making the MINIMAL code change.\n\n"
    f"# {INSTANCE_ID}\n\n"
    f"## Problem\n\n{instance_data['problem_statement']}\n\n"
    f"## Instructions\n\n"
    f"1. Read the relevant source file(s) to understand the code\n"
    f"2. Make the MINIMAL edit to fix the issue\n"
    f"3. Stop after making the fix -- do not refactor or add features\n"
)

inst = EvalInstance(
    instance_id=INSTANCE_ID,
    task_description=task,
    metadata={"repo": instance_data["repo"], "base_commit": instance_data["base_commit"]},
)

print(f"=== Running HeadlessDriver (deepseek-v4-pro, ConversationRunner) ===", flush=True)
driver = HeadlessDriver(use_runner=True, timeout_s=300)

# Ensure the repo exists in workdir (pre-provisioned shallow clone at base commit)
workdir_str = str(WORKDIR)
if not (WORKDIR / ".git").exists():
    print(f"  Cloning {instance_data['repo']}...", flush=True)
    subprocess.run(
        ["git", "clone", f"https://github.com/{instance_data['repo']}.git", workdir_str],
        timeout=600, capture_output=True,
    )
    subprocess.run(
        ["git", "checkout", instance_data["base_commit"]],
        cwd=workdir_str, timeout=60, capture_output=True,
    )

# sanity: clean tree at base commit
proc = subprocess.run(["git", "diff", "HEAD"], cwd=workdir_str, capture_output=True, timeout=15)
print(f"  Working dir: {workdir_str} (diff-lines={len(proc.stdout.splitlines())})", flush=True)

print(f"  Solving instance (this can take several minutes)...", flush=True)
result = driver.solve_instance(inst, workdir_str)

print(f"\n=== RESULT ===", flush=True)
print(f"  instance_id: {result.instance_id}")
print(f"  error: {result.error[:500] if result.error else 'NONE'}")
print(f"  wall_time_s: {result.wall_time_s}")
print(f"  tokens_in/out: {result.tokens_in}/{result.tokens_out}")
print(f"  model_patch length: {len(result.model_patch or '')}")

# Save prediction
pred_path = ROOT / "eval/swebench_work/predictions_agent.jsonl"
with open(pred_path, "w") as f:
    f.write(json.dumps({
        "instance_id": INSTANCE_ID,
        "model_name_or_path": "deepseek-v4-pro",
        "model_patch": result.model_patch or "",
    }, ensure_ascii=False) + "\n")
print(f"\n  Predictions saved to {pred_path}", flush=True)

if result.model_patch:
    print("\nSWEBENCH_AGENT_RESULT: PATCH_GENERATED", flush=True)
else:
    print(f"\nSWEBENCH_AGENT_RESULT: NO_PATCH error={result.error[:200] if result.error else 'unknown'}", flush=True)
