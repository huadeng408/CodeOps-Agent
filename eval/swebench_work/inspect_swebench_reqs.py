"""Find SWE_BENCH_URL_RAW constant"""
from swebench.harness.test_spec import python as m
import inspect

# Look for SWE_BENCH_URL_RAW
print("=== SWE_BENCH_URL_RAW ===")
print(m.SWE_BENCH_URL_RAW)

# Look for MAP_REPO_TO_REQS_PATHS
print("\n=== MAP_REPO_TO_REQS_PATHS (first 5 entries) ===")
for i, (repo, paths) in enumerate(m.MAP_REPO_TO_REQS_PATHS.items()):
    if i >= 5:
        break
    print(f"  {repo}: {paths}")
print(f"  ... ({len(m.MAP_REPO_TO_REQS_PATHS)} total)")
