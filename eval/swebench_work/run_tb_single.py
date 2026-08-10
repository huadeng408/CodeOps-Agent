"""Terminal-Bench SINGLE task test — break-filter-js-from-html with base64 agent fix."""
import sys, os, json
from pathlib import Path

sys.path.insert(0, "D:/vscode/localcode")

# ---- Windows path patches ----
import docker.models.containers
_orig_put_archive = docker.models.containers.Container.put_archive
def _patched_put_archive(self, path, data):
    path = path.replace("\\", "/")
    return _orig_put_archive(self, path, data)
docker.models.containers.Container.put_archive = _patched_put_archive

_orig_exec_run = docker.models.containers.Container.exec_run
def _patched_exec_run(self, cmd, *args, **kwargs):
    if isinstance(cmd, str): cmd = cmd.replace("\\", "/")
    elif isinstance(cmd, (list, tuple)): cmd = [c.replace("\\", "/") if isinstance(c, str) else c for c in cmd]
    return _orig_exec_run(self, cmd, *args, **kwargs)
docker.models.containers.Container.exec_run = _patched_exec_run

from terminal_bench.terminal import tmux_session as _ts
_orig_send_keys = _ts.TmuxSession.send_keys
def _patched_send_keys(self, keys, block=False, min_timeout_sec=0.0, max_timeout_sec=180.0):
    if isinstance(keys, str): keys = keys.replace("\\", "/")
    elif isinstance(keys, (list, tuple)): keys = [k.replace("\\", "/") if isinstance(k, str) else k for k in keys]
    return _orig_send_keys(self, keys, block=block, min_timeout_sec=min_timeout_sec, max_timeout_sec=max_timeout_sec)
_ts.TmuxSession.send_keys = _patched_send_keys

from terminal_bench.terminal.docker_compose_manager import DockerComposeManager
DockerComposeManager.CONTAINER_TEST_DIR = Path("/tests")
DockerComposeManager.CONTAINER_SESSION_LOGS_PATH = "/logs"
DockerComposeManager.CONTAINER_AGENT_LOGS_PATH = "/agent-logs"
_orig_dcm_copy = DockerComposeManager.copy_to_container
@staticmethod
def _patched_dcm_copy(container, paths, container_dir=None, container_filename=None):
    if container_dir is not None: container_dir = container_dir.replace("\\", "/")
    if isinstance(paths, Path): paths = [paths]
    if isinstance(paths, (list, tuple)): paths = [Path(str(p).replace("\\", "/")) if isinstance(p, Path) else p for p in paths]
    return _orig_dcm_copy(container, paths, container_dir=container_dir, container_filename=container_filename)
DockerComposeManager.copy_to_container = _patched_dcm_copy
print("[patch] Windows Docker path fix applied")

# UTF-8 stdout
for stream in (sys.stdout, sys.stderr):
    try:
        enc = (stream.encoding or "").lower().replace("-", "")
        if stream is not None and enc not in ("", "utf8"):
            stream.reconfigure(encoding="utf-8", errors="replace")
    except (AttributeError, ValueError):
        pass

DATA_DIR = Path("D:/vscode/localcode/eval/benchmark_data/terminalbench/tasks")
OUTPUT_DIR = Path("D:/vscode/localcode/eval_results/terminalbench_honest")
OUTPUT_DIR.mkdir(parents=True, exist_ok=True)

# Clean stale dir
import shutil
stale = OUTPUT_DIR / "tb-honest-single"
if stale.exists():
    shutil.rmtree(stale)

TASK_ID = "break-filter-js-from-html"
print(f"=== Terminal-Bench Single Task: {TASK_ID} ===")

from eval.benchmarks.terminalbench import _materialize_task_dir
_materialize_task_dir(DATA_DIR / TASK_ID)

from terminal_bench.harness.harness import Harness

harness = Harness(
    output_path=OUTPUT_DIR,
    run_id="tb-honest-single",
    agent_import_path="eval.swebench_work.deepseek_tb_agent:DeepSeekTBAgent",
    dataset_path=DATA_DIR,
    task_ids=[TASK_ID],
    n_concurrent_trials=1,
    n_attempts=1,
    cleanup=False,
    agent_kwargs={
        "api_key": os.environ["DEEPSEEK_API_KEY"],  # env-only, no hardcoded key
        "model": "deepseek-chat",
    },
)

print(f"\n=== Running Harness ===")
try:
    results = harness.run()
    print(f"\n=== RESULTS ===")
    print(f"n_resolved: {results.n_resolved}")
    print(f"accuracy: {results.accuracy}")
    for trial in results.results:
        print(f"  task={trial.task_id} resolved={trial.is_resolved} mode={trial.failure_mode}")
except Exception as e:
    print(f"HARNESS FAILED: {e}")
    import traceback
    traceback.print_exc()

print("DONE")
