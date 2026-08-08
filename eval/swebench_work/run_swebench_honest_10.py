"""Honest SWE-bench agent for 10 instances — NO HINTS, NO ANSWERS, NO CHEATING.

The agent gets:
  - The problem statement from SWE-bench_Verified (exactly as in the dataset)
  - A git checkout at the correct base_commit
  - Tools: Read, Write, Edit, Bash, Glob, Grep

What it does NOT get:
  - No hint about which file to edit
  - No hint about what the fix is
  - No ground-truth patch
  - No file location hints

Each instance is solved independently. Results saved as predictions.jsonl for official scoring.
"""
import os, sys, json, time, subprocess, shutil
from pathlib import Path
from concurrent.futures import ThreadPoolExecutor, as_completed

# ---- Config ----
DEEPSEEK_API_KEY = "sk-ccdf276c22824536bd97a011dcd27102"
DEEPSEEK_BASE_URL = "https://api.deepseek.com/v1"
MODEL = "deepseek-chat"

os.environ["LOCAL_LLM_API_KEY"] = DEEPSEEK_API_KEY
os.environ["LOCAL_LLM_BASE_URL"] = DEEPSEEK_BASE_URL
os.environ["LOCAL_LLM_MODEL"] = MODEL

ROOT = Path("D:/vscode/localcode")
WORK_BASE = ROOT / "eval/swebench_work/repos"
PRED_DIR = ROOT / "eval_results/swebench_agent_honest"
PRED_DIR.mkdir(parents=True, exist_ok=True)

sys.path.insert(0, str(ROOT))

from eval.adapter import EvalInstance
from eval.driver_headless import HeadlessDriver

# ---------------------------------------------------------------------------
# 10-instance selection — 5 repos × 2 instances each, NO astropy/django/sklearn/matplotlib
# ---------------------------------------------------------------------------
INSTANCE_IDS = [
    "psf__requests-6028",     # requests v2.27 (tiny repo ~5MB)
    "psf__requests-5414",     # requests v2.26
    "pallets__flask-5014",    # flask v2.3 (small repo ~15MB)
    "mwaskom__seaborn-3069",  # seaborn v0.12 (medium ~30MB)
    "mwaskom__seaborn-3187",  # seaborn v0.12
    "pylint-dev__pylint-8898",  # pylint v3.0 (medium ~40MB)
    "pylint-dev__pylint-7277",  # pylint v2.15
    "pytest-dev__pytest-10081", # pytest v7.2 (medium ~30MB)
    "sphinx-doc__sphinx-10614", # sphinx v7.2 (medium ~30MB)
    "sphinx-doc__sphinx-11510", # sphinx v7.2
]


# ---------------------------------------------------------------------------
# Pre-load the dataset ONCE (not 10 times)
# ---------------------------------------------------------------------------
os.environ["HF_DATASETS_OFFLINE"] = "1"
os.environ["HF_HOME"] = "C:/Users/ieeep/.cache/huggingface"
print("Loading SWE-bench_Verified dataset (once)...")
from datasets import load_dataset
_DATASET = load_dataset("princeton-nlp/SWE-bench_Verified", split="test")
_INSTANCE_CACHE = {inst["instance_id"]: dict(inst) for inst in _DATASET}
print(f"  Cached {len(_INSTANCE_CACHE)} instances")


def load_instance_data(instance_id: str) -> dict:
    """Load a single instance from the cached dataset."""
    if instance_id not in _INSTANCE_CACHE:
        raise ValueError(f"Instance {instance_id} not found in dataset")
    return _INSTANCE_CACHE[instance_id]


def clone_repo(instance_data: dict, workdir: Path) -> bool:
    """Clone the repo at the correct base_commit. Returns True on success."""
    repo = instance_data["repo"]
    base_commit = instance_data["base_commit"]
    repo_url = f"https://github.com/{repo}.git"

    if workdir.exists():
        shutil.rmtree(workdir, ignore_errors=True)

    print(f"  Cloning {repo_url} ...")
    try:
        subprocess.run(
            ["git", "clone", "--filter=blob:none", "--single-branch", repo_url, str(workdir)],
            check=True, capture_output=True, timeout=300,
        )
        # Fetch the specific commit
        subprocess.run(
            ["git", "-C", str(workdir), "fetch", "origin", base_commit],
            check=True, capture_output=True, timeout=60,
        )
        subprocess.run(
            ["git", "-C", str(workdir), "checkout", base_commit],
            check=True, capture_output=True, timeout=30,
        )
        return True
    except subprocess.CalledProcessError as e:
        # Try full clone as fallback
        print(f"  Blobless clone failed, trying full clone: {e.stderr[:200] if e.stderr else str(e)}")
        if workdir.exists():
            shutil.rmtree(workdir, ignore_errors=True)
        try:
            subprocess.run(
                ["git", "clone", repo_url, str(workdir)],
                check=True, capture_output=True, timeout=600,
            )
            subprocess.run(
                ["git", "-C", str(workdir), "checkout", base_commit],
                check=True, capture_output=True, timeout=30,
            )
            return True
        except subprocess.CalledProcessError as e2:
            print(f"  FULL CLONE FAILED: {e2.stderr[:300] if e2.stderr else str(e2)}")
            return False


def build_task_description(instance_data: dict) -> str:
    """Build the agent task description — ONLY the problem statement, no hints."""
    return (
        "You are a software engineer fixing a bug.\n"
        "The repository is already checked out at the correct version with the bug present.\n\n"
        f"## Issue\n\n{instance_data['problem_statement']}\n\n"
        "## Instructions\n\n"
        "1. Read the relevant source files to understand the code and the bug.\n"
        "2. Find the root cause and make the MINIMAL code change to fix it.\n"
        "3. Do NOT generate tests, docs, or any other changes — just the fix.\n"
        "4. When you're done, state clearly what you changed and why.\n"
    )


def solve_one_instance(instance_id: str) -> dict:
    """Solve a single SWE-bench instance honestly. Returns result dict."""
    print(f"\n{'='*60}")
    print(f"=== SOLVING: {instance_id} ===")
    t0 = time.time()

    try:
        instance_data = load_instance_data(instance_id)
    except Exception as e:
        return {"instance_id": instance_id, "error": f"LOAD_FAILED: {e}", "model_patch": ""}

    repo_name = instance_data["repo"].split("/")[-1]
    workdir = WORK_BASE / repo_name / instance_id.replace("/", "_")

    # Clone
    if not clone_repo(instance_data, workdir):
        return {"instance_id": instance_id, "error": "CLONE_FAILED", "model_patch": ""}

    # Build task (HONEST — no hints)
    task = build_task_description(instance_data)
    print(f"  Task: {len(task)} chars")

    inst_obj = EvalInstance(
        instance_id=instance_id,
        task_description=task,
        metadata={
            "repo": instance_data["repo"],
            "base_commit": instance_data["base_commit"],
        },
    )

    # Solve with HeadlessDriver (GIVE AGENT ONLY THE PROBLEM STATEMENT)
    try:
        driver = HeadlessDriver(use_runner=True, timeout_s=600)
        result = driver.solve_instance(inst_obj, str(workdir))
    except Exception as e:
        elapsed = time.time() - t0
        return {
            "instance_id": instance_id,
            "error": f"AGENT_ERROR: {e}",
            "model_patch": "",
            "wall_time_s": elapsed,
            "tokens_in": 0,
            "tokens_out": 0,
        }

    elapsed = time.time() - t0

    # Get the actual git diff
    diff_result = subprocess.run(
        ["git", "-C", str(workdir), "diff", "HEAD"],
        capture_output=True, text=True)
    actual_diff = diff_result.stdout

    print(f"  Wall time: {elapsed:.1f}s")
    print(f"  Tokens in/out: {result.tokens_in}/{result.tokens_out}")
    print(f"  Patch size: {len(actual_diff)} chars")
    if result.error:
        print(f"  Agent error: {result.error[:200]}")

    return {
        "instance_id": instance_id,
        "model_name_or_path": f"deepseek-chat-honest-{elapsed:.0f}s",
        "model_patch": actual_diff,
        "wall_time_s": elapsed,
        "tokens_in": result.tokens_in,
        "tokens_out": result.tokens_out,
        "agent_error": result.error or "",
    }


def main():
    print("=" * 60)
    print("SWE-BENCH HONEST AGENT — 10 INSTANCES")
    print("=" * 60)
    print(f"Instances: {INSTANCE_IDS}")
    print(f"Model: {MODEL}")
    print(f"Strategy: NO HINTS. Agent gets ONLY the problem statement.")
    print()

    results = []
    for i, iid in enumerate(INSTANCE_IDS):
        print(f"\n[{i+1}/{len(INSTANCE_IDS)}] {iid}")
        try:
            r = solve_one_instance(iid)
            results.append(r)
        except Exception as e:
            print(f"  FATAL: {e}")
            results.append({"instance_id": iid, "error": f"FATAL: {e}", "model_patch": ""})

    # ---- Write predictions.jsonl ----
    pred_path = PRED_DIR / "predictions.jsonl"
    with open(pred_path, "w", encoding="utf-8") as f:
        for r in results:
            entry = {
                "instance_id": r["instance_id"],
                "model_name_or_path": r.get("model_name_or_path", "deepseek-chat-honest"),
                "model_patch": r.get("model_patch", ""),
            }
            f.write(json.dumps(entry, ensure_ascii=False) + "\n")

    # ---- Write full log ----
    log_path = PRED_DIR / "agent_log.json"
    with open(log_path, "w", encoding="utf-8") as f:
        json.dump({
            "model": MODEL,
            "strategy": "HONEST — no hints, no file paths, no fix directions",
            "instances": INSTANCE_IDS,
            "results": results,
        }, f, indent=2, ensure_ascii=False)

    # ---- Summary ----
    resolved = sum(1 for r in results if r.get("model_patch") and not r.get("error"))
    failed = sum(1 for r in results if r.get("error"))
    no_patch = sum(1 for r in results if not r.get("model_patch") and not r.get("error"))

    print(f"\n{'='*60}")
    print(f"=== SUMMARY ===")
    print(f"Total: {len(results)}")
    print(f"With patch: {resolved}")
    print(f"No patch: {no_patch}")
    print(f"Errors: {failed}")
    for r in results:
        status = "PATCH" if r.get("model_patch") else ("ERROR" if r.get("error") else "NO_PATCH")
        print(f"  {status:8s} | {r['instance_id']:40s} | {r.get('wall_time_s', 0):6.1f}s | {r.get('tokens_in',0)}/{r.get('tokens_out',0)} tok")
    print(f"\nPredictions: {pred_path}")
    print(f"Full log: {log_path}")
    print(f"\nNext: wsl -d Ubuntu-24.04 -- bash -c 'cd /d/vscode/localcode && python3 eval/swebench_work/score_agent.py'")


if __name__ == "__main__":
    main()
