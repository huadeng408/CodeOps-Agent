"""Lightweight V4 Pro + tool-use SWE-bench solve for flask-5014.

Reads src/flask/blueprints.py, sends the issue + file to V4 Pro, applies the
fix, captures git diff. RAM-light (no orchestrator stack — just API calls).
"""
import json
import subprocess
import sys
import time
import urllib.request

ROOT = "D:/vscode/localcode"
WORKDIR = f"{ROOT}/eval/swebench_work/flask"
BP_PATH = "src/flask/blueprints.py"

API_KEY = "sk-ccdf276c22824536bd97a011dcd27102"

# 1. Read the current file.
content = open(f"{WORKDIR}/{BP_PATH}", encoding="utf-8").read()

# 2. Send file + issue to V4 Pro.
issue = (
    "Require a non-empty name for Blueprints.\n"
    "A Blueprint with an empty name causes problems.\n"
    "Add a guard in Blueprint.__init__ to raise ValueError when name is empty.\n"
    "This should be right alongside the existing dot-in-name check."
)

prompt = (
    "Fix this bug in Flask's Blueprint class. "
    "You are given the file content and must return ONLY the corrected file content "
    "(the ENTIRE file, with your fix applied). "
    "Do NOT explain, just return the complete corrected file.\n\n"
    f"BUG: {issue}\n\n"
    f"FILE ({BP_PATH}):\n{content}"
)

print(f"Sending {BP_PATH} ({len(content)} chars) + issue to V4 Pro...", flush=True)
payload = json.dumps({
    "model": "deepseek-v4-pro",
    "temperature": 0.0,
    "max_tokens": 16384,
    "messages": [{"role": "user", "content": prompt}],
}).encode()
req = urllib.request.Request(
    "https://api.deepseek.com/v1/chat/completions", data=payload,
    headers={"Content-Type": "application/json", "Authorization": f"Bearer {API_KEY}"},
)
t0 = time.time()
resp = json.loads(urllib.request.urlopen(req, timeout=300).read())
elapsed = time.time() - t0

choice = resp["choices"][0]
fixed = (choice.get("message", {}).get("content") or "").strip()
usage = resp.get("usage", {})
rt = (usage.get("completion_tokens_details") or {}).get("reasoning_tokens")

print(f"V4 Pro response: finish={choice.get('finish_reason')} "
      f"reasoning_tokens={rt} completion_tokens={usage.get('completion_tokens')} "
      f"({elapsed:.1f}s)", flush=True)

# Strip markdown fences if present.
if fixed.startswith("```"):
    lines = fixed.splitlines()
    end = len(lines)
    for i, l in enumerate(lines):
        if i > 0 and l.startswith("```"):
            end = i
            break
    fixed = "\n".join(lines[1:end])
fixed = fixed.strip()

# Verify it contains the Blueprint class.
if "class Blueprint" not in fixed:
    print("ERROR: V4 Pro response does not contain class Blueprint!", flush=True)
    print(f"First 500 chars: {fixed[:500]}", flush=True)
    sys.exit(1)

# 3. Apply the fix.
with open(f"{WORKDIR}/{BP_PATH}", "w", encoding="utf-8") as fh:
    fh.write(fixed)
print(f"Wrote fixed {BP_PATH} ({len(fixed)} chars)", flush=True)

# Verify the fix is present: should have "if not name: raise ValueError"
if "raise ValueError" in fixed and "not name" in fixed:
    print("Fix verification: guard clause PRESENT in fixed file", flush=True)
else:
    print("WARNING: guard clause not found in fixed file!", flush=True)

# 4. Capture git diff.
patch = subprocess.check_output(
    ["git", "-C", WORKDIR, "diff", "--no-color", "HEAD"],
    text=True,
)
patch_path = f"{ROOT}/eval/swebench_work/flask5014_v4pro.patch"
with open(patch_path, "w", encoding="utf-8") as fh:
    fh.write(patch)

print(f"=== GIT DIFF ({len(patch)} chars) ===\n{patch}", flush=True)
print(f"PATCH saved to {patch_path}", flush=True)
