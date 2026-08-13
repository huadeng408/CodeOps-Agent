"""Ephemeral verifier-network support for official Terminal-Bench receipts.

The public task snapshot stays immutable.  When explicitly requested, this
module adds a generated Docker Compose overlay only for one Harness invocation.
It never changes Docker Desktop, the task compose file, or the parent process.
"""

from __future__ import annotations

import json
import os
import hashlib
from contextlib import contextmanager
from pathlib import Path
from typing import Iterator
from urllib.parse import urlparse


_PROXY_ENV_KEYS = ("TERMINALBENCH_VERIFIER_PROXY", "TERMINALBENCH_VERIFIER_NO_PROXY")
_DEFAULT_NO_PROXY = "localhost,127.0.0.1,::1"


def internal_harness_run_id(receipt_run_id: str) -> str:
    """Return a stable short run id so Windows paths remain below MAX_PATH."""
    return "tb-" + hashlib.sha256(receipt_run_id.encode("utf-8")).hexdigest()[:12]


@contextmanager
def scoped_verifier_proxy(proxy: str | None, receipt_root: Path) -> Iterator[None]:
    """Temporarily add a proxy-only Compose overlay to official Harness calls."""
    normalized = _validate_proxy(proxy) if proxy else None
    no_proxy = _DEFAULT_NO_PROXY if normalized else None
    _write_manifest(receipt_root, enabled=normalized is not None, proxy=normalized, no_proxy=no_proxy)
    if normalized is None:
        yield
        return

    overlay = receipt_root / "terminalbench-verifier-proxy.compose.yaml"
    overlay.write_text(_compose_overlay(), encoding="utf-8")
    previous_env = {key: os.environ.get(key) for key in _PROXY_ENV_KEYS}
    os.environ["TERMINALBENCH_VERIFIER_PROXY"] = normalized
    os.environ["TERMINALBENCH_VERIFIER_NO_PROXY"] = no_proxy

    from terminal_bench.terminal.docker_compose_manager import DockerComposeManager

    original = DockerComposeManager.get_docker_compose_command

    def with_overlay(manager: DockerComposeManager, command: list[str]) -> list[str]:
        generated = original(manager, command)
        insertion_index = len(generated) - len(command)
        return [
            *generated[:insertion_index],
            "-f",
            str(overlay),
            *generated[insertion_index:],
        ]

    DockerComposeManager.get_docker_compose_command = with_overlay
    try:
        yield
    finally:
        DockerComposeManager.get_docker_compose_command = original
        for key, value in previous_env.items():
            if value is None:
                os.environ.pop(key, None)
            else:
                os.environ[key] = value


def _validate_proxy(value: str) -> str:
    parsed = urlparse(value)
    if parsed.scheme not in {"http", "https"} or not parsed.hostname:
        raise ValueError("verifier proxy must be an absolute http(s) URL")
    if parsed.username or parsed.password:
        raise ValueError("verifier proxy must not contain credentials")
    if parsed.path not in {"", "/"} or parsed.query or parsed.fragment:
        raise ValueError("verifier proxy must not include a path, query, or fragment")
    return value.rstrip("/")


def _write_manifest(receipt_root: Path, *, enabled: bool, proxy: str | None, no_proxy: str | None) -> None:
    receipt_root.mkdir(parents=True, exist_ok=True)
    manifest = {
        "enabled": enabled,
        "proxy": proxy,
        "no_proxy": no_proxy,
        "scope": "official Terminal-Bench verifier container only",
    }
    (receipt_root / "verifier-proxy-manifest.json").write_text(
        json.dumps(manifest, indent=2, ensure_ascii=True) + "\n", encoding="utf-8"
    )


def _compose_overlay() -> str:
    return """services:
  client:
    environment:
      HTTP_PROXY: ${TERMINALBENCH_VERIFIER_PROXY}
      HTTPS_PROXY: ${TERMINALBENCH_VERIFIER_PROXY}
      http_proxy: ${TERMINALBENCH_VERIFIER_PROXY}
      https_proxy: ${TERMINALBENCH_VERIFIER_PROXY}
      NO_PROXY: ${TERMINALBENCH_VERIFIER_NO_PROXY}
      no_proxy: ${TERMINALBENCH_VERIFIER_NO_PROXY}
"""
