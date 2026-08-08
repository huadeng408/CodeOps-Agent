#!/usr/bin/env python3
"""Generate a predictions.jsonl with the REAL ground-truth patch for astropy__astropy-12907."""
import os, sys, json

os.environ["HF_DATASETS_OFFLINE"] = "1"
os.environ["HF_HOME"] = "/mnt/c/Users/ieeep/.cache/huggingface"

# Real fix: cright[...] = 1 → cright[...] = right
# This is the actual SWE-bench ground truth patch
real_patch = """diff --git a/astropy/modeling/separable.py b/astropy/modeling/separable.py
--- a/astropy/modeling/separable.py
+++ b/astropy/modeling/separable.py
@@ -242,7 +242,7 @@ def _cstack(left, right):
         cright = _coord_matrix(right, 'right', noutp)
     else:
         cright = np.zeros((noutp, right.shape[1]))
-        cright[-right.shape[0]:, -right.shape[1]:] = 1
+        cright[-right.shape[0]:, -right.shape[1]:] = right

     return np.hstack([cleft, cright])"""

predictions = [{
    "instance_id": "astropy__astropy-12907",
    "model_name_or_path": "ground-truth-test",
    "model_patch": real_patch,
}]

out_path = "/mnt/d/vscode/localcode/eval/swebench_work/predictions_smoke.jsonl"
with open(out_path, "w") as f:
    for p in predictions:
        f.write(json.dumps(p, ensure_ascii=False) + "\n")

print(f"Wrote {len(predictions)} ground-truth predictions to {out_path}")
