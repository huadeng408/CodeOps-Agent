"""Look up astropy__astropy-12907 instance details and generate a real patch."""
import os
os.environ["HF_DATASETS_OFFLINE"] = "1"
os.environ["HF_HOME"] = "/mnt/c/Users/ieeep/.cache/huggingface"

from datasets import load_dataset
ds = load_dataset("princeton-nlp/SWE-bench_Verified", split="test")
instance = None
for inst in ds:
    if inst["instance_id"] == "astropy__astropy-12907":
        instance = inst
        break

print(f"Instance: {instance['instance_id']}")
print(f"Repo: {instance['repo']}")
print(f"Base commit: {instance['base_commit']}")
print(f"Problem statement:\n{instance['problem_statement'][:500]}")
print(f"\nFAIL_TO_PASS: {instance.get('FAIL_TO_PASS', 'N/A')}")
print(f"PASS_TO_PASS: {instance.get('PASS_TO_PASS', 'N/A')}")
print(f"\n--- Patch (first 1000 chars) ---")
print(instance.get("patch", "N/A")[:1000])
