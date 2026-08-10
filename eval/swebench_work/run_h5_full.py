"""Full H5 1-instance smoke through the unified harness.

Unlike ``h5_rescore.py`` (which re-scored an existing patch), this drives the
whole lifecycle -- prepare -> solve -> score -> finalize -- so the artifact tree
gets manifest, prediction, official scorer raw output under ``scorer/``, and
``checksums.sha256`` covering all of it.  That is what the H5 gate asks for.

The key is read from the operator's local notes into the process environment
only.  It is never printed, logged, or written to any artifact.
"""

from __future__ import annotations

import os
import re
import subprocess
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
KEY_NOTES = Path(r"D:\Obsidian\code-autogrowth\项目进展\api-key.md")
KEY_PATTERN = re.compile(r"sk-[0-9a-f]{32}")
BASE_URL = "https://api.deepseek.com/v1"
MODEL = "deepseek-v4-pro"


def read_key() -> str | None:
    """Extract the DeepSeek key, anchored on its label to avoid grabbing a
    neighbouring provider's key (an unanchored regex previously returned the
    OpenRouter key and produced a 401)."""
    if not KEY_NOTES.exists():
        return None
    lines = KEY_NOTES.read_text(encoding="utf-8", errors="replace").splitlines()
    for i, line in enumerate(lines):
        if "deepseek" in line.lower():
            for candidate in lines[i : i + 4]:
                match = KEY_PATTERN.search(candidate)
                if match:
                    return match.group(0)
    return None


def main() -> int:
    key = read_key()
    if not key:
        print("FAIL: no DeepSeek key found; refusing to run with a placeholder")
        return 2
    print(f"key loaded: yes (length {len(key)})")

    env = dict(os.environ)
    env["LOCAL_LLM_API_KEY"] = key
    env["SWEBENCH_WSL_PROXY"] = "http://127.0.0.1:7890"
    env.setdefault("SWEBENCH_NAMESPACE", "swebench")
    env["PYTHONIOENCODING"] = "utf-8"

    out_dir = REPO_ROOT / "eval_results" / "h5-full-traces-20260810"
    cmd = [
        sys.executable, "-u", "-m", "eval.run",
        "--benchmark", "swebench",
        "--model", MODEL,
        "--base-url", BASE_URL,
        "--smoke",
        "--output-dir", str(out_dir),
    ]
    print("launching:", " ".join(cmd[3:]))
    result = subprocess.run(
        cmd, cwd=str(REPO_ROOT), env=env,
        encoding="utf-8", errors="replace",
    )
    print(f"eval.run exit code: {result.returncode}")
    return result.returncode


if __name__ == "__main__":
    sys.exit(main())
