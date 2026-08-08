"""Monkey-patch swebench to use curl subprocess for HTTPS requests through Clash proxy.

Python's requests library has SSL compatibility issues through Clash HTTP CONNECT tunnel,
but curl works fine. This monkey-patches get_requirements_by_commit to use curl.
"""
import os, sys, subprocess, json
from functools import lru_cache

os.environ["HF_DATASETS_OFFLINE"] = "1"
os.environ["HF_HOME"] = "/mnt/c/Users/ieeep/.cache/huggingface"

# --- Monkey-patch: use curl for HTTP requests through proxy ---
PROXY = "http://127.0.0.1:7890"

def _curl_get(url: str, timeout: int = 30, **kwargs) -> str:
    """Use curl subprocess to fetch URLs — works through Clash proxy where Python SSL doesn't."""
    import tempfile
    # Build curl command with headers
    headers = kwargs.get('headers', {})
    cmd = ["curl", "-s", "-f", "--max-time", str(timeout), "--proxy", PROXY]
    if isinstance(headers, dict):
        for k, v in headers.items():
            cmd.extend(["-H", f"{k}: {v}"])
    cmd.append(url)

    tmp = tempfile.NamedTemporaryFile(delete=False, suffix='.http_resp')
    tmp.close()
    try:
        result = subprocess.run(
            cmd + ["-o", tmp.name, "-w", "%{http_code}"],
            capture_output=True, text=True, timeout=timeout + 5
        )
        http_code = result.stdout.strip()
        if result.returncode != 0 or http_code != "200":
            return f"HTTP {http_code}"
        with open(tmp.name, 'r') as f:
            return f.read()
    finally:
        try:
            os.unlink(tmp.name)
        except OSError:
            pass

# Apply monkey-patch to swebench
import swebench.harness.test_spec.python as py_module
py_module.requests.get = _curl_get

# --- Now run scoring ---
from swebench.harness.run_evaluation import main

predictions_path = "/mnt/d/vscode/localcode/eval/swebench_work/predictions_smoke.jsonl"
report_dir = "/mnt/d/vscode/localcode/eval/swebench_work/reports"
run_id = "smoke-test-3"

print(f"Starting SWE-bench scoring (curl-backed HTTP)...")
print(f"  predictions: {predictions_path}")
print(f"  report_dir: {report_dir}")
print(f"  run_id: {run_id}")

try:
    main(
        dataset_name="princeton-nlp/SWE-bench_Verified",
        split="test",
        instance_ids=[],
        predictions_path=predictions_path,
        max_workers=1,
        force_rebuild=False,
        cache_level="env",
        clean=False,
        open_file_limit=4096,
        run_id=run_id,
        timeout=3600,
        namespace=None,
        rewrite_reports=False,
        modal=False,
        instance_image_tag="latest",
        env_image_tag="latest",
        report_dir=report_dir,
    )
    print("Scoring completed!")

    # Check report
    import glob
    report_files = sorted(glob.glob(f"{report_dir}/**/*.json", recursive=True))
    print(f"\nReport files ({len(report_files)}):")
    for rf in report_files:
        print(f"  {rf}")
        with open(rf) as f:
            data = f.read()
            print(data[:3000] if len(data) > 3000 else data)
except Exception as e:
    print(f"Scoring failed: {e}")
    import traceback
    traceback.print_exc()
    sys.exit(1)
