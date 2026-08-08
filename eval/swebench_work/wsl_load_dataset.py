import os, sys

# Use Windows HF cache in offline mode
os.environ["HF_DATASETS_OFFLINE"] = "1"
os.environ["HF_HOME"] = "/mnt/c/Users/ieeep/.cache/huggingface"

from datasets import load_dataset
ds = load_dataset("princeton-nlp/SWE-bench_Verified", split="test", trust_remote_code=True)
print("LOADED:", len(ds), "instances")
print("First:", ds[0]["instance_id"])
