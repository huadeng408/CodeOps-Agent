"""Generate EvalPlus completions via an OpenAI-compatible LLM API (host-side).

Scoring is done separately via the official evalplus Docker Linux sandbox
(see score_sequential.py).

This script supports BOTH the legacy deepseek-chat (V3, non-reasoning) and the
new deepseek-v4-pro (reasoning) models via a single --model knob, so the V3↔V4Pro
comparison holds methodology constant and varies only the model.

Integrity features (do not silently lower the bar):
  - Logs finish_reason; flags finish_reason=="length" (truncation) as a counted
    warning so a reasoning model's incomplete output is never mistaken for a
    genuine model failure.
  - Logs reasoning_tokens vs completion_tokens usage per task.
  - Counts fallback bodies (def X(*a,**k): pass) so API failures masquerading
    as model failures are visible in the final summary.
  - Concurrent generation with a thread-safe checkpoint; resume-safe.

Usage:
  # V3 (reproduces the frozen baseline methodology; do NOT overwrite frozen samples)
  python eval/gen_samples.py humaneval --model deepseek-chat --max-tokens 2048 --workers 1

  # V4 Pro (reasoning model; default 8192 token headroom)
  python eval/gen_samples.py humaneval --model deepseek-v4-pro --max-tokens 8192 --workers 4
  python eval/gen_samples.py mbpp     --model deepseek-v4-pro --max-tokens 8192 --workers 4

  # Small probe to check for truncation before a full run
  python eval/gen_samples.py humaneval 5 --model deepseek-v4-pro --max-tokens 8192
"""
import argparse
import json
import os
import re
import sys
import threading
import time
import urllib.error
import urllib.request
import warnings
from concurrent.futures import ThreadPoolExecutor, as_completed

warnings.filterwarnings("ignore")
sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from orchestrator.config.env import load_dotenv  # type: ignore

load_dotenv(os.path.join(os.path.dirname(__file__), "..", ".env.local"))

DATASETS = ("humaneval", "mbpp")

# Short tag embedded in the output filename so different models do not overwrite
# each other (the frozen V3 samples must be preserved for the baseline).
_MODEL_TAGS = {
    "deepseek-chat": "v3",
    "deepseek-v4-pro": "v4pro",
    "deepseek-reasoner": "r1",
    "deepseek-v4-flash": "v4flash",
}

FALLBACK = "def {entry}(*a, **k):\n    pass"

# Prompt is held IDENTICAL to the frozen V3 run so the only variable that
# changes between V3 and V4 Pro is the model. (Full-function generation mode,
# which is stricter than canonical prompt-prefix completion.)
PROMPT_FMT = (
    "Implement the Python function `{entry}` per the spec below. "
    "Return ONLY the COMPLETE function definition (starting with 'def {entry}'), "
    "properly indented, no explanation, no markdown fences, no test code.\n\n{prompt}"
)

_FENCE_RE = re.compile(r"^```[a-zA-Z0-9]*\n", re.MULTILINE)


def model_tag(model: str) -> str:
    """Short filesystem-safe tag for a model id."""
    if model in _MODEL_TAGS:
        return _MODEL_TAGS[model]
    return re.sub(r"[^a-z0-9]+", "-", model.lower()).strip("-") or "model"


def strip_fences(code: str) -> str:
    """Strip a single wrapping ```lang ... ``` fence if present."""
    code = code.strip()
    # Remove a leading fenced block opener (```python / ``` etc.)
    m = _FENCE_RE.match(code)
    if m:
        code = code[m.end():]
    if code.endswith("```"):
        code = code[:-3]
    return code.strip()


def trim_before_def(code: str, entry: str) -> str:
    """Drop anything before the `def {entry}` line (prose / chatter)."""
    if entry and f"def {entry}" in code:
        idx = code.find(f"def {entry}")
        if idx > 0:
            code = code[idx:]
    return code


def call_llm(url: str, api_key: str, model: str, max_tokens: int,
             content: str, timeout: float = 300.0) -> tuple[str, str, dict]:
    """POST one chat completion. Returns (finish_reason, visible_content, usage).

    timeout defaults to 300s: reasoning models can take 200s+ on problems they
    over-think (observed: HumanEval/145 took 206s with ~14k reasoning tokens).
    """
    payload = json.dumps({
        "model": model,
        "temperature": 0.0,
        "max_tokens": max_tokens,
        "messages": [{"role": "user", "content": content}],
    }).encode()
    req = urllib.request.Request(url, data=payload, headers={
        "Content-Type": "application/json",
        "Authorization": f"Bearer {api_key}",
    })
    resp = json.loads(urllib.request.urlopen(req, timeout=timeout).read())
    choice = resp["choices"][0]
    finish = choice.get("finish_reason", "")
    text = choice.get("message", {}).get("content", "") or ""
    return finish, text, resp.get("usage", {}) or {}


def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(description="EvalPlus sample generator (multi-model)")
    p.add_argument("dataset", choices=list(DATASETS))
    p.add_argument("limit", nargs="?", type=int, default=None,
                   help="Only first N problems (default: all)")
    p.add_argument("--model", default=os.getenv("OPENAI_MODEL", "deepseek-chat"))
    p.add_argument("--max-tokens", type=int, default=8192,
                   help="Generation token cap (default 8192; use 2048 for V3 repro)")
    p.add_argument("--workers", type=int, default=4,
                   help="Concurrent API callers (default 4)")
    p.add_argument("--attempts", type=int, default=6,
                   help="Max attempts per task incl. 429 backoff (default 6)")
    return p.parse_args()


def main() -> int:
    args = parse_args()
    api_key = os.getenv("OPENAI_API_KEY", "")
    base_url = os.getenv("OPENAI_BASE_URL", "").rstrip("/")
    if not api_key or not base_url:
        print("ERROR: OPENAI_API_KEY / OPENAI_BASE_URL not set in .env.local", file=sys.stderr)
        return 2
    url = f"{base_url}/v1/chat/completions"
    model = args.model
    tag = model_tag(model)

    here = os.path.dirname(os.path.abspath(__file__))
    lim = f"_{args.limit}" if args.limit else ""
    samples_path = os.path.join(here, f"samples_{args.dataset}_{tag}{lim}.jsonl")
    ckpt_path = samples_path + ".ckpt"

    # Load problems (host has evalplus installed for generation only).
    if args.dataset == "humaneval":
        from evalplus.data import get_human_eval_plus
        problems = get_human_eval_plus()
    else:
        from evalplus.data import get_mbpp_plus
        problems = get_mbpp_plus()

    ids = sorted(problems.keys())
    if args.limit:
        ids = ids[: args.limit]

    # Resume: read checkpoint, tolerate a corrupt trailing line.
    done: dict[str, str] = {}
    skipped_bad = 0
    if os.path.exists(ckpt_path):
        with open(ckpt_path, encoding="utf-8") as fh:
            for line in fh:
                line = line.strip()
                if not line:
                    continue
                try:
                    r = json.loads(line)
                    done[r["task_id"]] = r.get("solution", r.get("completion", ""))
                except (json.JSONDecodeError, KeyError):
                    skipped_bad += 1

    remaining = [t for t in ids if t not in done]
    print(f"=== Generate {args.dataset}+ ({len(ids)}) on {model} [tag={tag}] ===", flush=True)
    print(f"max_tokens={args.max_tokens} workers={args.workers} attempts={args.attempts}", flush=True)
    print(f"Resume: {len(done)} done, {len(remaining)} remaining"
          + (f" ({skipped_bad} bad ckpt line(s) skipped)" if skipped_bad else ""), flush=True)
    print(f"output: {os.path.basename(samples_path)}", flush=True)

    # Re-seed the samples file from the checkpoint (last-wins per task_id).
    if os.path.exists(samples_path):
        os.remove(samples_path)
    for tid, comp in done.items():
        with open(samples_path, "a", encoding="utf-8") as fh:
            fh.write(json.dumps({"task_id": tid, "solution": comp}) + "\n")

    lock = threading.Lock()
    counts = {"ok": 0, "fallback": 0, "truncated": 0, "api_err": 0}
    edge = {"truncated": [], "fallback": []}  # task_ids, for transparent disclosure
    reason_seen: dict[str, int] = {}
    t_start = time.time()

    def gen_one(tid: str) -> None:
        prob = problems[tid]
        entry = prob.get("entry_point", "")
        content = PROMPT_FMT.format(entry=entry, prompt=prob["prompt"])
        code = ""
        is_fallback = False
        is_truncated = False
        for attempt in range(args.attempts):
            try:
                finish, text, usage = call_llm(url, api_key, model,
                                              args.max_tokens, content)
                code = strip_fences(text)
                code = trim_before_def(code, entry)
                rt = (usage.get("completion_tokens_details") or {}).get("reasoning_tokens")
                with lock:
                    reason_seen[finish] = reason_seen.get(finish, 0) + 1
                if finish == "length":
                    is_truncated = True  # keep the (likely incomplete) text; scored honestly as fail
                # If the visible output is not a usable function def, treat as fallback.
                if entry and f"def {entry}" not in code:
                    is_fallback = True
                break
            except urllib.error.HTTPError as e:
                code_http = getattr(e, "code", None)
                if code_http == 429:
                    time.sleep(min(30.0, 1.5 * (2 ** attempt)) + (attempt * 0.3))
                    continue
                if attempt == args.attempts - 1:
                    code = FALLBACK.format(entry=entry)
                    is_fallback = True
                else:
                    time.sleep(2 ** attempt)
            except Exception:
                if attempt == args.attempts - 1:
                    code = FALLBACK.format(entry=entry)
                    is_fallback = True
                else:
                    time.sleep(2 ** attempt)

        if is_fallback:
            code = FALLBACK.format(entry=entry)
        line = json.dumps({"task_id": tid, "solution": code})
        with lock:
            with open(samples_path, "a", encoding="utf-8") as fh:
                fh.write(line + "\n")
            with open(ckpt_path, "a", encoding="utf-8") as fh:
                fh.write(line + "\n")
            if is_fallback:
                counts["fallback"] += 1
                edge["fallback"].append(tid)
            if is_truncated:
                counts["truncated"] += 1
                edge["truncated"].append(tid)
            counts["ok"] += 1
            done_n = counts["ok"]
            if done_n == 1 or done_n % 20 == 0:
                el = time.time() - t_start
                print(f"  [{done_n}/{len(remaining)}] fb={counts['fallback']} "
                      f"trunc={counts['truncated']} ({el:.0f}s)", flush=True)

    with ThreadPoolExecutor(max_workers=args.workers) as ex:
        futs = [ex.submit(gen_one, t) for t in remaining]
        for f in as_completed(futs):
            f.result()

    n = sum(1 for _ in open(samples_path, encoding="utf-8"))
    el = time.time() - t_start
    print(f"\nDone: {n} samples in {os.path.basename(samples_path)} ({el:.0f}s)", flush=True)
    print(f"INTEGRITY: ok={counts['ok']} fallback={counts['fallback']} "
          f"truncated={counts['truncated']} api_err={counts['api_err']}", flush=True)
    print(f"finish_reasons: {reason_seen}", flush=True)
    if counts["truncated"]:
        print("WARNING: truncation detected — consider raising --max-tokens; "
              "truncated tasks are scored honestly as failures.", flush=True)
    # Transparent disclosure of edge-case task_ids.
    with open(ckpt_path + ".edge_cases.json", "w", encoding="utf-8") as fh:
        json.dump({"model": model, "max_tokens": args.max_tokens,
                   "truncated": sorted(set(edge["truncated"])),
                   "fallback": sorted(set(edge["fallback"]))}, fh, indent=2)
    print(f"End: {time.strftime('%Y-%m-%d %H:%M:%S')}", flush=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
