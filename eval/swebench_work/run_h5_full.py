"""Full H5 1-instance smoke through the unified harness.

Unlike ``h5_rescore.py`` (which re-scored an existing patch), this drives the
whole lifecycle -- prepare -> solve -> score -> finalize -- so the artifact tree
gets manifest, prediction, official scorer raw output under ``scorer/``, and
``checksums.sha256`` covering all of it.  That is what the H5 gate asks for.

The credential must already be present in the current process environment. It
is never printed, logged, or written to any artifact.
"""

from __future__ import annotations

import os
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
if str(REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(REPO_ROOT))

from eval.swebench_work.credential_redaction import run_redacted_command

BASE_URL = "https://api.deepseek.com/v1"
MODEL = "deepseek-v4-pro"
_CREDENTIAL_ENV_NAMES = ("LOCAL_LLM_API_KEY", "DEEPSEEK_API_KEY")


def read_key() -> str | None:
    """Read a credential injected into this process, without file fallback."""
    for env_name in _CREDENTIAL_ENV_NAMES:
        value = os.environ.get(env_name, "").strip()
        if value:
            return value
    return None


def main() -> int:
    key = read_key()
    if not key:
        print("FAIL: no DeepSeek key found; refusing to run with a placeholder")
        return 2
    print("credential loaded: yes")

    env = dict(os.environ)
    env["LOCAL_LLM_API_KEY"] = key
    env["DEEPSEEK_API_KEY"] = key
    env["SWEBENCH_WSL_PROXY"] = "http://127.0.0.1:7890"
    env.setdefault("SWEBENCH_NAMESPACE", "swebench")
    env["PYTHONIOENCODING"] = "utf-8"

    out_dir = REPO_ROOT / "eval_results" / "h5-full-traces-20260810-join"
    cmd = [
        sys.executable, "-u", "-m", "eval.run",
        "--benchmark", "swebench",
        "--model", MODEL,
        "--base-url", BASE_URL,
        "--smoke",
        "--output-dir", str(out_dir),
    ]
    print("launching:", " ".join(cmd[3:]))
    returncode = run_redacted_command(
        cmd,
        cwd=REPO_ROOT,
        env=env,
        secrets=(key,),
    )
    print(f"eval.run exit code: {returncode}")
    return returncode


if __name__ == "__main__":
    sys.exit(main())
