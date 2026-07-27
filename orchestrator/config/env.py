from __future__ import annotations

import os
from pathlib import Path


def load_dotenv(path: str | Path = ".env.local") -> None:
    env_path = Path(path)
    if not env_path.exists():
        return

    for raw_line in env_path.read_text(encoding="utf-8-sig").splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        key = key.strip()
        value = value.strip().strip('"').strip("'")
        if key and key not in os.environ:
            os.environ[key] = value


def read_env(name: str, default: str = "") -> str:
    value = os.environ.get(name, default)
    return value.strip() if isinstance(value, str) else default


def is_thinking_enabled() -> bool:
    """Return whether Extended Thinking / 深度思考模式 is enabled.

    Controlled by the THINKING_ENABLED environment variable (default: true).
    """
    return read_env("THINKING_ENABLED", "true").lower() == "true"


MODEL_FAST_DEFAULT: str = "gpt-4o-mini"


def get_model_fast() -> str:
    """Return the fast/cheap model name for simple tasks.

    Controlled by the MODEL_FAST environment variable, which the Go harness
    sets from its config.ModelFast before launching the orchestrator subprocess.
    Defaults to "gpt-4o-mini".
    Returns empty string when explicitly set to empty (disabled).
    """
    return read_env("MODEL_FAST", MODEL_FAST_DEFAULT)
