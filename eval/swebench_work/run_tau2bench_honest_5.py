"""tau2-bench 5-instance honest test — airline domain via official tau_bench.run.run()."""
import os, sys, json, time, io
from pathlib import Path

# Fix GBK console UnicodeEncodeError for rich/emoji output
sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')
sys.stderr = io.TextIOWrapper(sys.stderr.buffer, encoding='utf-8', errors='replace')

os.environ["DEEPSEEK_API_KEY"] = os.environ.get("DEEPSEEK_API_KEY", "sk-ccdf276c22824536bd97a011dcd27102")
os.environ["OPENAI_API_KEY"] = os.environ["DEEPSEEK_API_KEY"]  # litellm needs both

sys.path.insert(0, "D:/vscode/tau-bench")

from tau_bench.run import run as tau_run
from tau_bench.types import RunConfig, EnvRunResult

OUTPUT_DIR = Path("D:/vscode/localcode/eval_results/tau2bench_honest")
OUTPUT_DIR.mkdir(parents=True, exist_ok=True)

TASK_IDS = list(range(5))  # tasks 0-4, airline domain
print(f"=== tau2-bench Honest Agent — {len(TASK_IDS)} AIRLINE INSTANCES ===")

run_config = RunConfig(
    model_provider="deepseek",
    user_model_provider="deepseek",
    model="deepseek-chat",
    user_model="deepseek-chat",
    num_trials=1,
    env="airline",
    task_split="test",
    task_ids=TASK_IDS,
    log_dir=str(OUTPUT_DIR),
    max_concurrency=1,
)

t0 = time.time()
env_results: list[EnvRunResult] = tau_run(run_config)
elapsed = time.time() - t0

print(f"\n=== RESULTS ({elapsed:.1f}s) ===")
rewards = []
for er in env_results:
    rewards.append(er.reward)
    print(f"  task_id={er.task_id} reward={er.reward:.2f}")

summary = {
    "benchmark": "tau2-bench",
    "domain": "airline",
    "task_ids": TASK_IDS,
    "model": "deepseek-chat",
    "agent": "tool-calling (honest — no hints)",
    "rewards": {str(er.task_id): er.reward for er in env_results},
    "avg_reward": sum(rewards) / len(rewards) if rewards else 0,
    "wall_time_s": elapsed,
}
with open(OUTPUT_DIR / "tau2bench_honest_5_summary.json", "w") as f:
    json.dump(summary, f, indent=2, default=str)

print(f"\nAvg reward: {summary['avg_reward']:.3f}")
print(f"Saved to {OUTPUT_DIR}")
