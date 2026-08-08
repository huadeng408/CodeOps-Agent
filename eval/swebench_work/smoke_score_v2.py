"""Run SWE-bench official scoring with pre-built base image."""
import os, sys

os.environ["HF_DATASETS_OFFLINE"] = "1"
os.environ["HF_HOME"] = "/mnt/c/Users/ieeep/.cache/huggingface"

# Proxy configuration for Clash on Windows (mirrored networking makes 127.0.0.1 work)
os.environ["HTTP_PROXY"] = "http://127.0.0.1:7890"
os.environ["HTTPS_PROXY"] = "http://127.0.0.1:7890"
os.environ["http_proxy"] = "http://127.0.0.1:7890"
os.environ["https_proxy"] = "http://127.0.0.1:7890"
os.environ["NO_PROXY"] = "localhost,127.0.0.1,.internal,docker.internal"
os.environ["no_proxy"] = "localhost,127.0.0.1,.internal,docker.internal"

from swebench.harness.run_evaluation import main

predictions_path = "/mnt/d/vscode/localcode/eval/swebench_work/predictions_smoke.jsonl"
report_dir = "/mnt/d/vscode/localcode/eval/swebench_work/reports"
run_id = "smoke-test-2"

print(f"Starting SWE-bench scoring...")
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
