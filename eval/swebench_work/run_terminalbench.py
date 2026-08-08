"""Run 1 Terminal-Bench task through official Harness — smoke test.
With Windows path fixes for Docker container commands and paths.
"""
import sys, json
sys.path.insert(0, "D:/vscode/localcode")

from pathlib import Path
import docker.models.containers

# ---------------------------------------------------------------------------
# Monkey-patch 1: fix Container.put_archive path backslash issue
# ---------------------------------------------------------------------------
_orig_put_archive = docker.models.containers.Container.put_archive

def _patched_put_archive(self, path, data):
    path = path.replace("\\", "/")
    return _orig_put_archive(self, path, data)

docker.models.containers.Container.put_archive = _patched_put_archive

# ---------------------------------------------------------------------------
# Monkey-patch 2: fix Container.exec_run path backslash
# ---------------------------------------------------------------------------
_orig_exec_run = docker.models.containers.Container.exec_run

def _patched_exec_run(self, cmd, *args, **kwargs):
    if isinstance(cmd, str):
        cmd = cmd.replace("\\", "/")
    elif isinstance(cmd, (list, tuple)):
        cmd = [c.replace("\\", "/") if isinstance(c, str) else c for c in cmd]
    return _orig_exec_run(self, cmd, *args, **kwargs)

docker.models.containers.Container.exec_run = _patched_exec_run

# ---------------------------------------------------------------------------
# Monkey-patch 3: fix TmuxSession.send_keys backslash in commands
# ---------------------------------------------------------------------------
from terminal_bench.terminal import tmux_session as _ts
_orig_send_keys = _ts.TmuxSession.send_keys

def _patched_send_keys(self, keys, block=False, min_timeout_sec=0.0, max_timeout_sec=180.0):
    if isinstance(keys, str):
        keys = keys.replace("\\", "/")
    elif isinstance(keys, (list, tuple)):
        keys = [k.replace("\\", "/") if isinstance(k, str) else k for k in keys]
    return _orig_send_keys(self, keys, block=block,
                           min_timeout_sec=min_timeout_sec, max_timeout_sec=max_timeout_sec)

_ts.TmuxSession.send_keys = _patched_send_keys

# ---------------------------------------------------------------------------
# Monkey-patch 4: fix DockerComposeManager static paths
# ---------------------------------------------------------------------------
from terminal_bench.terminal.docker_compose_manager import DockerComposeManager
DockerComposeManager.CONTAINER_SESSION_LOGS_PATH = "/logs"
DockerComposeManager.CONTAINER_AGENT_LOGS_PATH = "/agent-logs"
DockerComposeManager.CONTAINER_TEST_DIR = Path("/tests")

# Also patch TmuxSession static paths
_ts.TmuxSession._GET_ASCIINEMA_TIMESTAMP_SCRIPT_CONTAINER_PATH = Path("/tmp/get-asciinema-timestamp.sh")

# ---------------------------------------------------------------------------
# Monkey-patch 5: fix copy_to_container (uses str(Path("/tests")) → "\tests")
# ---------------------------------------------------------------------------
_orig_dcm_copy = DockerComposeManager.copy_to_container

@staticmethod
def _patched_dcm_copy(container, paths, container_dir=None, container_filename=None):
    if container_dir is not None:
        container_dir = container_dir.replace("\\", "/")
    if isinstance(paths, Path):
        paths = [paths]
    if isinstance(paths, (list, tuple)):
        paths = [Path(str(p).replace("\\", "/")) if isinstance(p, Path) else p for p in paths]
    return _orig_dcm_copy(container, paths,
                          container_dir=container_dir,
                          container_filename=container_filename)

DockerComposeManager.copy_to_container = _patched_dcm_copy

print("[patch] Windows Docker path fix applied (5 patches)")

# ---------------------------------------------------------------------------
# Now run the harness
# ---------------------------------------------------------------------------
from terminal_bench.agents.agent_name import AgentName
from terminal_bench.harness.harness import Harness

data_dir = Path("D:/vscode/localcode/eval/benchmark_data/terminalbench/tasks")
task_ids = ["adaptive-rejection-sampler"]
output_dir = Path("D:/vscode/localcode/eval_results/terminalbench_smoke")
output_dir.mkdir(parents=True, exist_ok=True)

print(f"=== Terminal-Bench Harness: {task_ids} ===")
print(f"Dataset path: {data_dir}")

harness = Harness(
    output_path=output_dir,
    run_id="tb-smoke-04",
    agent_name=AgentName.NOP,
    dataset_path=data_dir,
    task_ids=task_ids,
    n_concurrent_trials=1,
    n_attempts=1,
    cleanup=False,
)

print("Harness built, calling run()...")
results = harness.run()

print(f"\n=== RESULTS ===")
print(f"n_resolved: {results.n_resolved}")
print(f"n_unresolved: {results.n_unresolved}")
print(f"accuracy: {results.accuracy}")
print(f"pass_at_k: {results.pass_at_k}")
for trial in results.results:
    print(f"  task_id={trial.task_id} is_resolved={trial.is_resolved} failure_mode={trial.failure_mode}")
    print(f"  tokens_in={trial.total_input_tokens} tokens_out={trial.total_output_tokens}")

# Write summary
summary = {
    "benchmark": "terminal-bench",
    "agent": "NOP",
}
if hasattr(results, "model_dump"):
    summary.update(results.model_dump(mode="json"))
with open(output_dir / "tb_smoke_summary.json", "w") as f:
    json.dump(summary, f, indent=2, default=str)

print("\nTERMINALBENCH_RESULT: OK")
