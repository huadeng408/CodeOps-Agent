"""Run 1 tau2-bench airline task through official tau_bench.run.run() — smoke test."""
import sys, os, json
sys.path.insert(0, "D:/vscode/localcode")
sys.path.insert(0, "D:/vscode/tau-bench")

# DeepSeek API config for tau2-bench (uses OPENAI_API_KEY convention)
os.environ["OPENAI_API_KEY"] = "sk-ccdf276c22824536bd97a011dcd27102"
os.environ["OPENAI_BASE_URL"] = "https://api.deepseek.com/v1"

from pathlib import Path
from tau_bench.run import run as tau_run
from tau_bench.types import RunConfig

output_dir = Path("D:/vscode/localcode/eval_results/tau2bench_smoke")
output_dir.mkdir(parents=True, exist_ok=True)

print(f"=== tau2-bench: airline task 0 ===")

run_config = RunConfig(
    model_provider="deepseek",
    user_model_provider="deepseek",
    model="deepseek-chat",
    user_model="deepseek-chat",
    num_trials=1,
    env="airline",
    agent_strategy="tool-calling",
    temperature=0.0,
    task_split="test",
    task_ids=[0],
    log_dir=str(output_dir),
    max_concurrency=1,
)

print(f"Config: {run_config.model_dump_json(indent=2) if hasattr(run_config, 'model_dump_json') else run_config}")
print(f"\nRunning tau_run()...")

try:
    env_results = tau_run(run_config)
    print(f"\n=== RESULTS ===")
    for er in env_results:
        print(f"  task_id={er.task_id} reward={er.reward:.4f}")
        if er.info:
            print(f"  info={json.dumps(er.info, default=str)[:500]}")
    print("\nTAU2BENCH_RESULT: OK")
except Exception as e:
    print(f"\nTAU2BENCH_RESULT: FAILED")
    print(f"  {type(e).__name__}: {e}")
    import traceback
    traceback.print_exc()
    sys.exit(1)
