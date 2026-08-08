"""Run SWE-bench official scoring with offline dataset."""
import os, sys

# Force offline mode — use cached dataset
os.environ["HF_DATASETS_OFFLINE"] = "1"
os.environ["HF_HOME"] = "/mnt/c/Users/ieeep/.cache/huggingface"

from swebench.harness.run_evaluation import main

predictions_path = "/mnt/d/vscode/localcode/eval/swebench_work/predictions_smoke.jsonl"
report_dir = "/mnt/d/vscode/localcode/eval/swebench_work/reports"
run_id = "smoke-test-1"

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
    print("✅ Scoring completed!")

    # Print results if any
    import glob
    report_files = glob.glob(f"{report_dir}/**/*.json", recursive=True)
    for rf in sorted(report_files):
        print(f"  Report: {rf}")
        with open(rf) as f:
            data = f.read()
            if len(data) < 2000:
                print(data)
            else:
                print(data[:1000] + "...")
except Exception as e:
    print(f"❌ Scoring failed: {e}")
    import traceback
    traceback.print_exc()
    sys.exit(1)
