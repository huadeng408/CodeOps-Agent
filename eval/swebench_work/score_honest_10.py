"""Score honest SWE-bench predictions in WSL2 — adapted from score_honest.py for 10-instance batch."""
import os, sys, subprocess

os.environ["HF_DATASETS_OFFLINE"] = "1"
os.environ["HF_HOME"] = "/mnt/c/Users/ieeep/.cache/huggingface"

PROXY = "http://127.0.0.1:7890"

# ---- Fake Response (curl through Clash proxy for SSL compat) ----
class FakeResponse:
    def __init__(self):
        self.status_code = 404
        self._content = b""
        self.encoding = "utf-8"
        self.text = ""

    @classmethod
    def curl_get(cls, url: str, timeout: int = 30, **kwargs) -> "FakeResponse":
        resp = cls()
        headers = kwargs.get("headers", {})
        cmd = ["curl", "-s", "--connect-timeout", "10", "--max-time", str(timeout),
               "--proxy", PROXY]
        if isinstance(headers, dict):
            for k, v in headers.items():
                cmd.extend(["-H", f"{k}: {v}"])

        import tempfile
        tmp = tempfile.NamedTemporaryFile(delete=False, suffix=".http_resp")
        tmp.close()
        try:
            result = subprocess.run(
                cmd + ["-o", tmp.name, "-w", "%{http_code}", url],
                capture_output=True, text=True, timeout=timeout + 10
            )
            code_str = result.stdout.strip()
            resp.status_code = int(code_str) if code_str.isdigit() else 500
            if resp.status_code == 200:
                with open(tmp.name, "rb") as f:
                    resp._content = f.read()
                resp.text = resp._content.decode(resp.encoding, errors="replace")
        except Exception:
            pass
        finally:
            try:
                os.unlink(tmp.name)
            except OSError:
                pass
        return resp

import swebench.harness.test_spec.python as py_module
py_module.requests.get = FakeResponse.curl_get

from swebench.harness.run_evaluation import main

predictions_path = "/mnt/d/vscode/localcode/eval_results/swebench_agent_honest/predictions.jsonl"
report_dir = "/mnt/d/vscode/localcode/eval_results/swebench_agent_honest/report"
run_id = "honest-10-v1"

print(f"Starting SWE-bench official scoring (HONEST 10-instance test)...")
print(f"  predictions: {predictions_path}")
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
    print("\nScoring completed!")

    import glob
    report_files = sorted(glob.glob(f"{report_dir}/**/*.json", recursive=True))
    print(f"\nReport files ({len(report_files)}):")
    for rf in report_files[-5:]:
        print(f"  {rf}")
        with open(rf) as f:
            import json
            d = json.loads(f.read())
            print(f"  total: {d.get('total_instances')} submitted: {d.get('submitted_instances')}")
            print(f"  completed: {d.get('completed_instances')} resolved: {d.get('resolved_instances')}")
            print(f"  unresolved: {d.get('unresolved_instances')} errors: {d.get('error_instances')}")
            print(f"  resolved_ids: {d.get('resolved_ids')}")
except Exception as e:
    print(f"Scoring failed: {type(e).__name__}: {e}")
    import traceback
    traceback.print_exc()
    sys.exit(1)
