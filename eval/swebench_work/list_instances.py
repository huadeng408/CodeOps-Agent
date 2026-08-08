"""List all SWE-bench Verified instances for manual selection."""
import os
os.environ["HF_DATASETS_OFFLINE"] = "1"
os.environ["HF_HOME"] = "C:/Users/ieeep/.cache/huggingface"
from datasets import load_dataset
ds = load_dataset("princeton-nlp/SWE-bench_Verified", split="test")
for i, inst in enumerate(ds):
    rid = inst["repo"].split("/")[-1] if "/" in inst.get("repo","") else inst.get("repo","")
    print(f'{i:4d} | {inst["instance_id"]:50s} | {rid:25s} | v{inst.get("version","?")}')
