"""Run one arm of the paired harness-uplift experiment.

Both arms go through the same entry point so the only difference between them is
the harness configuration under test, not the way they were launched.  A
before/after number is only worth anything if the "before" and "after" were
measured the same way.

Usage:
    python eval/swebench_work/run_arm.py baseline
    python eval/swebench_work/run_arm.py optimized

The instance list comes from the pinned subset file (sha256-verified by
``eval/run.py``), never from a limit, so both arms provably run the same 20
instances.
"""

from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
if str(REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(REPO_ROOT))

MODEL = "deepseek-v4-pro"
BASE_URL = "https://api.deepseek.com/v1"
SUBSET = REPO_ROOT / "data" / "eval" / "swebench" / "subset-astropy-20.json"

ARMS = {
    # arm: (output dir suffix, extra env)
    "baseline": ("arm-a-baseline", {}),
    "optimized": ("arm-b-optimized", {"SWEBENCH_HARNESS_UPLIFT": "1"}),
}


def read_key() -> str:
    """Read the DeepSeek key, delegating to the one implementation that works.

    An earlier version of this function searched the notes file for any
    ``sk-``-prefixed token and returned the first one, which picked up a
    neighbouring provider's key and produced ``HTTP 401`` on the first instance.
    ``run_h5_full.read_key`` already solved this by anchoring the search on the
    ``deepseek`` label — the comment there records the same 401. Reimplementing
    it reintroduced the bug it documents, so this defers to it instead.

    Never inlined, never echoed, never written to an artifact.
    """
    env_key = os.environ.get("DEEPSEEK_API_KEY", "").strip()
    if env_key:
        return env_key
    from eval.swebench_work.run_h5_full import read_key as anchored_read_key

    return anchored_read_key() or ""


def preflight_auth(key: str) -> tuple[bool, str]:
    """Send one minimal completion to confirm the credential works.

    Returns ``(ok, human-readable detail)``. The key is never echoed; only the
    HTTP status and, on failure, the provider's message with the key redacted.
    """
    import json as _json
    import urllib.error
    import urllib.request

    request = urllib.request.Request(
        f"{BASE_URL}/chat/completions",
        data=_json.dumps(
            {
                "model": MODEL,
                "messages": [{"role": "user", "content": "ok"}],
                "max_tokens": 1,
            }
        ).encode(),
        headers={"Authorization": f"Bearer {key}", "Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(request, timeout=60) as response:
            body = _json.loads(response.read())
            return True, f"HTTP {response.status}, model={body.get('model')}"
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode("utf-8", "replace")[:200].replace(key, "<redacted>")
        return False, f"HTTP {exc.code}: {detail}"
    except Exception as exc:
        return False, f"{type(exc).__name__}: {str(exc)[:200].replace(key, '<redacted>')}"


def main() -> int:
    if len(sys.argv) < 2 or sys.argv[1] not in ARMS:
        print(f"usage: {Path(__file__).name} {{{'|'.join(ARMS)}}}")
        return 2
    arm = sys.argv[1]
    suffix, extra_env = ARMS[arm]

    key = read_key()
    if not key:
        print("FAIL: no DeepSeek key found; refusing to run with a placeholder")
        return 2
    print(f"key loaded: yes (length {len(key)})")

    if not SUBSET.is_file():
        print(f"FAIL: pinned subset missing: {SUBSET}")
        return 2

    ok, detail = preflight_auth(key)
    print(f"auth preflight: {detail}")
    if not ok:
        # The first attempt at this experiment ran all 20 instances with a key
        # that returned HTTP 401 on every call. Each instance still cloned
        # astropy, still built an image, still invoked the official scorer, and
        # still recorded resolved=False — twelve minutes of work producing a
        # number that described the credential, not the agent. One request up
        # front is the whole cost of never doing that again.
        return 2

    env = dict(os.environ)
    env["LOCAL_LLM_API_KEY"] = key
    env["DEEPSEEK_API_KEY"] = key
    env["SWEBENCH_WSL_PROXY"] = "http://127.0.0.1:7890"
    env.setdefault("SWEBENCH_NAMESPACE", "swebench")
    env["PYTHONIOENCODING"] = "utf-8"
    env.update(extra_env)

    out_dir = REPO_ROOT / "eval_results" / "harness-uplift-20260810" / suffix
    cmd = [
        sys.executable, "-u", "-m", "eval.run",
        "--benchmark", "swebench",
        "--model", MODEL,
        "--base-url", BASE_URL,
        "--subset", str(SUBSET),
        "--output-dir", str(out_dir),
    ]
    print(f"[arm={arm}] launching:", " ".join(cmd[3:]))
    result = subprocess.run(
        cmd, cwd=str(REPO_ROOT), env=env, encoding="utf-8", errors="replace"
    )
    print(f"[arm={arm}] eval.run exit code: {result.returncode}")
    return result.returncode


if __name__ == "__main__":
    sys.exit(main())
