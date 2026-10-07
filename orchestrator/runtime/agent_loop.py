"""Versioned extension points for the agent loop.

Observation plugins remain compatible.  Strategy extensions may add bounded
context or block/adjust a pending action; they never execute tools or write the
durable Session ledger.
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
        "prepare_context",
        "before_model",
        "after_model",
        "before_tool",
        "after_tool",
        "finish_turn",
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
    payload: Mapping[str, Any] = field(default_factory=dict)

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
        if not isinstance(self.payload, Mapping):
            raise TypeError("payload must be a mapping")
        object.__setattr__(self, "metadata", MappingProxyType(dict(self.metadata)))
        object.__setattr__(self, "payload", MappingProxyType(dict(self.payload)))


@dataclass(frozen=True, slots=True)
class LoopDispatchResult:
    """Metadata and sanitized plugin failures returned by one dispatch."""

    metadata: dict[str, Any] = field(default_factory=dict)
    errors: list[dict[str, str]] = field(default_factory=list)
    context: tuple[str, ...] = ()
    tool_arguments: dict[str, Any] = field(default_factory=dict)
    model: str = ""
    blocked: bool = False
    message: str = ""


@dataclass(frozen=True, slots=True)
class _ExtensionRegistration:
    name: str
    phase: str
    callback: PluginCallback


class AgentLoopPluginRegistry:
    """Deterministic registry for observation-only loop plugins."""

    def __init__(self) -> None:
        self._plugins: dict[str, PluginCallback] = {}
        self._extensions: list[_ExtensionRegistration] = []

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
        normalized = str(name).strip()
        self._plugins.pop(normalized, None)
        self._extensions = [item for item in self._extensions if item.name != normalized]

    def names(self) -> tuple[str, ...]:
        return tuple(self._plugins)

    def register_extension(self, name: str, phase: str, callback: PluginCallback) -> None:
        """Register one ordered strategy callback at a mutable loop seam."""

        extension_name = str(name).strip()
        if not extension_name:
            raise ValueError("extension name must not be empty")
        if phase not in {
            "prepare_context",
            "before_model",
            "after_model",
            "before_tool",
            "after_tool",
            "finish_turn",
        }:
            raise ValueError(f"unsupported extension phase: {phase!r}")
        if not callable(callback):
            raise TypeError("extension callback must be callable")
        if any(item.name == extension_name for item in self._extensions):
            raise ValueError(f"agent loop extension already registered: {extension_name}")
        self._extensions.append(_ExtensionRegistration(extension_name, phase, callback))

    def extension_names(self) -> tuple[str, ...]:
        return tuple(item.name for item in self._extensions)

    def dispatch_extension(self, phase: str, event: LoopEvent) -> LoopDispatchResult:
        """Fold bounded strategy decisions while isolating plugin failures."""

        if phase not in {
            "prepare_context",
            "before_model",
            "after_model",
            "before_tool",
            "after_tool",
            "finish_turn",
        }:
            raise ValueError(f"unsupported extension phase: {phase!r}")
        metadata: dict[str, Any] = {}
        errors: list[dict[str, str]] = []
        contexts: list[str] = []
        arguments: dict[str, Any] = {}
        model = ""
        blocked = False
        message = ""
        for registration in tuple(self._extensions):
            if registration.phase != phase:
                continue
            try:
                raw = registration.callback(event)
                if raw is None:
                    continue
                if not isinstance(raw, Mapping):
                    raise TypeError("extension result must be a mapping")
                if raw.get("context"):
                    value = raw["context"]
                    values = value if isinstance(value, (list, tuple)) else [value]
                    contexts.extend(redact_credential_value(str(item)) for item in values if str(item).strip())
                if isinstance(raw.get("tool_arguments"), Mapping):
                    arguments.update({str(key): redact_credential_value(value) for key, value in raw["tool_arguments"].items()})
                if raw.get("model"):
                    model = redact_credential_value(str(raw["model"]))
                if raw.get("message"):
                    message = redact_credential_value(str(raw["message"]))
                if raw.get("metadata") and isinstance(raw["metadata"], Mapping):
                    metadata.update({str(key): redact_credential_value(value) for key, value in raw["metadata"].items()})
                blocked = blocked or bool(raw.get("blocked", raw.get("cancel", False)))
                if blocked:
                    break
            except Exception as exc:  # noqa: BLE001 - one extension cannot stop the loop
                errors.append(
                    {
                        "plugin": "<redacted-plugin>",
                        "error_type": type(exc).__name__,
                        "code": "plugin_extension_failed",
                    }
                )
        return LoopDispatchResult(
            metadata=metadata,
            errors=errors,
            context=tuple(contexts),
            tool_arguments=arguments,
            model=model,
            blocked=blocked,
            message=message,
        )

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
