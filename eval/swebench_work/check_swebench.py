"""Check swebench import + signature in WSL."""
from swebench.harness.run_evaluation import main
import inspect

print("main function found:", main)
sig = inspect.signature(main)
for name, param in sig.parameters.items():
    print(f"  {name}: {param.default!r}")
