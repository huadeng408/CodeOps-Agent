"""Honest SWE-bench agent — standalone path, NO HINTS. Fixed ConversationRunner issue bypassed.
Uses direct LLM without tools for robustness (conversation runner has DeepSeek tool-call protocol bugs).
"""
import os, sys, json, time, subprocess, shutil
from pathlib import Path

ROOT = Path("D:/vscode/localcode")
WORK_BASE = ROOT / "eval/swebench_work/repos"
PRED_DIR = ROOT / "eval_results/swebench_agent_honest"
PRED_DIR.mkdir(parents=True, exist_ok=True)

sys.path.insert(0, str(ROOT))

# Never inline the key: this file is tracked in a public repo.  Supply it via
# the environment (DEEPSEEK_API_KEY) at call time.
DEEPSEEK_API_KEY = os.environ.get("DEEPSEEK_API_KEY", "").strip()
if not DEEPSEEK_API_KEY:
    raise SystemExit(
        "DEEPSEEK_API_KEY is not set. Export it before running this script; "
        "it must never be hardcoded here."
    )
DEEPSEEK_BASE_URL = "https://api.deepseek.com/v1"
MODEL = "deepseek-chat"

INSTANCE_IDS = [
    "psf__requests-6028", "psf__requests-5414", "pallets__flask-5014",
    "mwaskom__seaborn-3069", "mwaskom__seaborn-3187",
    "pylint-dev__pylint-8898", "pylint-dev__pylint-7277",
    "pytest-dev__pytest-10081", "sphinx-doc__sphinx-10614", "sphinx-doc__sphinx-11510",
]

# Load dataset once
os.environ["HF_DATASETS_OFFLINE"] = "1"
os.environ["HF_HOME"] = "C:/Users/ieeep/.cache/huggingface"
from datasets import load_dataset
_DATASET = load_dataset("princeton-nlp/SWE-bench_Verified", split="test")
_INSTANCE_CACHE = {inst["instance_id"]: dict(inst) for inst in _DATASET}

from openai import OpenAI
_LLM = OpenAI(api_key=DEEPSEEK_API_KEY, base_url=DEEPSEEK_BASE_URL)


def solve_direct(instance_id: str) -> dict:
    """Solve ONE instance with NO ConversationRunner — direct LLM call."""
    print(f"\n=== {instance_id} ===")
    t0 = time.time()

    inst = _INSTANCE_CACHE[instance_id]
    repo = inst["repo"]
    base_commit = inst["base_commit"]
    repo_name = repo.split("/")[-1]
    workdir = WORK_BASE / repo_name / instance_id.replace("/", "_")

    # Clone
    if not workdir.exists():
        repo_url = f"https://github.com/{repo}.git"
        print(f"  Clone {repo_url}...")
        try:
            subprocess.run(["git", "clone", "--filter=blob:none", repo_url, str(workdir)], check=True, capture_output=True, timeout=300)
            subprocess.run(["git", "-C", str(workdir), "fetch", "origin", base_commit], check=True, capture_output=True, timeout=60)
            subprocess.run(["git", "-C", str(workdir), "checkout", base_commit], check=True, capture_output=True, timeout=30)
        except Exception as e:
            print(f"  Clone failed: {e}")
            return {"instance_id": instance_id, "model_patch": "", "error": str(e)[:200]}

    # Build prompt — HONEST: only problem statement
    task = (
        f"You are fixing a bug in the {repo_name} repository. "
        f"The repo is at {workdir}.\n\n"
        f"## Issue\n{inst['problem_statement']}\n\n"
        "## Instructions\n"
        "1. Read the relevant source files to understand the bug.\n"
        "2. Edit ONLY the minimal lines needed to fix the bug.\n"
        "3. Output the EXACT file path where the fix belongs, followed by the corrected file content.\n"
    )

    # First: let the model read the code
    print(f"  Explore: finding relevant files...")
    # Get the file tree for context
    try:
        files = subprocess.run(["git", "-C", str(workdir), "ls-files", "*.py"], capture_output=True, text=True, timeout=10)
        py_files = files.stdout.strip().split("\n")[:50]  # top 50 py files
    except:
        py_files = []

    # Read key source files
    source_context = ""
    key_dirs = set()
    for pf in py_files[:20]:
        p = Path(workdir) / pf
        if p.exists() and p.stat().st_size < 50000:
            try:
                content = p.read_text(encoding="utf-8", errors="replace")[:3000]
                source_context += f"\n### {pf}\n```python\n{content}\n```\n"
                key_dirs.add(str(p.parent))
            except:
                pass

    # Build the full prompt with source context
    full_prompt = (
        f"{task}\n\n"
        f"## Repository structure (key files)\n{source_context[:8000]}\n\n"
        "Based on the issue and the code shown above, identify the bug and "
        "output the fix. Format your response as:\n\n"
        "FILE: <relative/path/to/file.py>\n"
        "```diff\n"
        "--- a/path\n"
        "+++ b/path\n"
        "@@ ... @@\n"
        " your fix here\n"
        "```"
    )

    print(f"  Prompt: {len(full_prompt)} chars, model call...")

    try:
        response = _LLM.chat.completions.create(
            model=MODEL,
            messages=[
                {"role": "system", "content": "You are an expert Python debugger. Find the bug and output ONLY the minimal fix as a unified diff."},
                {"role": "user", "content": full_prompt},
            ],
            temperature=0.0,
            max_tokens=4096,
        )
    except Exception as e:
        print(f"  API error: {e}")
        return {"instance_id": instance_id, "model_patch": "", "error": str(e)[:200]}

    output = response.choices[0].message.content or ""
    tokens_in = response.usage.prompt_tokens if response.usage else 0
    tokens_out = response.usage.completion_tokens if response.usage else 0

    # Extract diff from output
    import re
    diff_match = re.search(r"```(?:diff)?\n(.*?)```", output, re.DOTALL)
    model_patch = diff_match.group(1).strip() if diff_match else output[:2000]

    elapsed = time.time() - t0
    print(f"  Done: {elapsed:.1f}s, {tokens_in}/{tokens_out} tokens, patch={len(model_patch)}B")

    return {
        "instance_id": instance_id,
        "model_name_or_path": f"deepseek-chat-honest-v2-{elapsed:.0f}s",
        "model_patch": model_patch,
        "wall_time_s": elapsed,
        "tokens_in": tokens_in,
        "tokens_out": tokens_out,
    }


def main():
    print("=" * 60)
    print("SWE-BENCH HONEST AGENT v2 — Direct LLM path (no ConversationRunner)")
    print("=" * 60)

    results = []
    for i, iid in enumerate(INSTANCE_IDS):
        print(f"\n[{i+1}/10]")
        r = solve_direct(iid)
        results.append(r)

    # Write predictions
    pred_path = PRED_DIR / "predictions.jsonl"
    with open(pred_path, "w", encoding="utf-8") as f:
        for r in results:
            f.write(json.dumps({
                "instance_id": r["instance_id"],
                "model_name_or_path": r.get("model_name_or_path", "deepseek-chat-honest-v2"),
                "model_patch": r.get("model_patch", ""),
            }, ensure_ascii=False) + "\n")

    # Summary
    patches = sum(1 for r in results if len(r.get("model_patch", "")) > 0)
    print(f"\n=== SUMMARY ===")
    print(f"Patches: {patches}/{len(results)}")
    for r in results:
        pl = len(r.get("model_patch", ""))
        print(f"  {'PATCH' if pl > 0 else 'NO_PATCH':8s} | {r['instance_id']:40s} | {pl}B | {r.get('tokens_in',0)}/{r.get('tokens_out',0)} tok")
    print(f"\nSaved to {pred_path}")


if __name__ == "__main__":
    main()
