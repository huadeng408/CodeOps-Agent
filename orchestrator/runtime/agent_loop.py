"""Versioned, observation-only extension points for the agent loop.

Plugins run in the Python orchestration layer.  They receive bounded metadata
about a lifecycle phase and cannot replace model responses, execute tools, or
write the durable Session ledger.
"""

from __future__ import annotations

from collections.abc import Callable, Mapping
from dataclasses import dataclass, field
from types import MappingProxyType
from typing import Any

from orchestrator.security import redact_credential_value

LOOP_SCHEMA_VERSION = "1"
LOOP_PHASES = frozenset(
    {
        "loop_start",
        "model_before",
        "model_after",
        "tool_before",
        "tool_after",
        "loop_end",
    }
)

PluginCallback = Callable[["LoopEvent"], Mapping[str, Any] | None]


@dataclass(frozen=True, slots=True)
class LoopEvent:
    """Stable lifecycle envelope supplied to an Agent Loop plugin."""

    schema_version: str
    session_id: str
    turn: int
    phase: str
    metadata: Mapping[str, Any] = field(default_factory=dict)

    def __post_init__(self) -> None:
        if self.schema_version != LOOP_SCHEMA_VERSION:
            raise ValueError(f"unsupported loop schema_version: {self.schema_version!r}")
        if not isinstance(self.session_id, str):
            raise TypeError("session_id must be a string")
        if not isinstance(self.turn, int) or isinstance(self.turn, bool) or self.turn < 0:
            raise ValueError("turn must be a non-negative integer")
        if self.phase not in LOOP_PHASES:
            raise ValueError(f"unsupported loop phase: {self.phase!r}")
        if not isinstance(self.metadata, Mapping):
            raise TypeError("metadata must be a mapping")
        object.__setattr__(self, "metadata", MappingProxyType(dict(self.metadata)))


@dataclass(frozen=True, slots=True)
class LoopDispatchResult:
    """Metadata and sanitized plugin failures returned by one dispatch."""

    metadata: dict[str, Any] = field(default_factory=dict)
    errors: list[dict[str, str]] = field(default_factory=list)


class AgentLoopPluginRegistry:
    """Deterministic registry for observation-only loop plugins."""

    def __init__(self) -> None:
        self._plugins: dict[str, PluginCallback] = {}

    def register(self, name: str, callback: PluginCallback) -> None:
        plugin_name = str(name).strip()
        if not plugin_name:
            raise ValueError("plugin name must not be empty")
        if plugin_name in self._plugins:
            raise ValueError(f"agent loop plugin already registered: {plugin_name}")
        if not callable(callback):
            raise TypeError("plugin callback must be callable")
        self._plugins[plugin_name] = callback

    def unregister(self, name: str) -> None:
        self._plugins.pop(str(name).strip(), None)

    def names(self) -> tuple[str, ...]:
        return tuple(self._plugins)

    def emit(self, event: LoopEvent) -> LoopDispatchResult:
        metadata: dict[str, Any] = {}
        errors: list[dict[str, str]] = []
        for name, callback in tuple(self._plugins.items()):
            try:
                update = callback(event)
                if update is None:
                    continue
                if not isinstance(update, Mapping):
                    raise TypeError("plugin metadata must be a mapping")
                for key, value in update.items():
                    metadata[str(key)] = redact_credential_value(value)
            except Exception as exc:  # noqa: BLE001 - one plugin cannot stop the loop
                errors.append(
                    {
                        "plugin": "<redacted-plugin>",
                        "error_type": type(exc).__name__,
                        # Do not expose exception text: arbitrary plugin data
                        # cannot be reliably recognized as a credential.
                        "code": "plugin_callback_failed",
                    }
                )
        return LoopDispatchResult(metadata=metadata, errors=errors)


__all__ = [
    "AgentLoopPluginRegistry",
    "LOOP_PHASES",
    "LOOP_SCHEMA_VERSION",
    "LoopDispatchResult",
    "LoopEvent",
]
