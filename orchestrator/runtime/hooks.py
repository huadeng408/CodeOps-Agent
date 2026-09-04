"""Lifecycle hooks and command extensions for the Python orchestration loop."""

from __future__ import annotations

import re
from collections.abc import Callable, Mapping
from dataclasses import dataclass, field
from threading import RLock
from types import MappingProxyType
from typing import Any

from orchestrator.security import redact_credential_value

HOOK_SCHEMA_VERSION = "1"
HOOK_PHASES = frozenset(
    {
        "session_start",
        "pre_step",
        "post_model",
        "pre_tool",
        "post_tool",
        "turn_stopping",
        "session_end",
    }
)
HOOK_BLOCKING_PHASES = frozenset({"session_start", "pre_step", "pre_tool", "post_tool"})
_COMMAND_NAME = re.compile(r"[a-z][a-z0-9_-]*\Z")


def _freeze_value(value: Any) -> Any:
    if isinstance(value, Mapping):
        return MappingProxyType(
            {str(key): _freeze_value(item) for key, item in value.items()}
        )
    if isinstance(value, (list, tuple)):
        return tuple(_freeze_value(item) for item in value)
    if isinstance(value, (set, frozenset)):
        return frozenset(_freeze_value(item) for item in value)
    return value


def _freeze_mapping(value: Mapping[str, Any]) -> Mapping[str, Any]:
    return MappingProxyType(
        {str(key): _freeze_value(item) for key, item in value.items()}
    )


@dataclass(frozen=True, slots=True)
class HookEvent:
    """Stable input envelope for one lifecycle hook dispatch."""

    phase: str
    session_id: str
    turn: int
    tool_name: str = ""
    payload: Mapping[str, Any] = field(default_factory=dict)
    metadata: Mapping[str, Any] = field(default_factory=dict)
    schema_version: str = HOOK_SCHEMA_VERSION

    def __post_init__(self) -> None:
        if self.schema_version != HOOK_SCHEMA_VERSION:
            raise ValueError("unsupported hook schema_version")
        if self.phase not in HOOK_PHASES:
            raise ValueError("unsupported hook phase")
        if not isinstance(self.session_id, str):
            raise TypeError("session_id must be a string")
        if not isinstance(self.turn, int) or isinstance(self.turn, bool) or self.turn < 0:
            raise ValueError("turn must be a non-negative integer")
        object.__setattr__(self, "payload", _freeze_mapping(self.payload))
        object.__setattr__(self, "metadata", _freeze_mapping(self.metadata))


@dataclass(frozen=True, slots=True)
class HookResult:
    """One hook decision; context is appended to the next model request.

    Cancellation is honored only at phases in ``HOOK_BLOCKING_PHASES``.
    Post-model, stopping, and session-end hooks observe settled work and cannot
    retroactively retract streamed output.
    """

    cancel: bool = False
    message: str = ""
    context: str = ""


@dataclass(frozen=True, slots=True)
class HookDispatchResult:
    """Folded results from one ordered hook dispatch."""

    results: tuple[HookResult, ...] = ()
    context: tuple[str, ...] = ()
    messages: tuple[str, ...] = ()
    errors: tuple[dict[str, str], ...] = ()

    @property
    def blocked(self) -> bool:
        return any(result.cancel for result in self.results)


HookCallback = Callable[[HookEvent], HookResult | Mapping[str, Any] | None]


@dataclass(frozen=True, slots=True)
class _HookRegistration:
    name: str
    phase: str
    callback: HookCallback
    matcher: str


class HookRegistry:
    """Deterministic, serial lifecycle hook registry."""

    def __init__(self) -> None:
        self._hooks: list[_HookRegistration] = []
        self._lock = RLock()

    def register(
        self,
        name: str,
        phase: str,
        callback: HookCallback,
        *,
        matcher: str = "",
    ) -> None:
        hook_name = str(name).strip()
        if not hook_name:
            raise ValueError("hook name must not be empty")
        if phase not in HOOK_PHASES:
            raise ValueError("unsupported hook phase")
        if not callable(callback):
            raise TypeError("hook callback must be callable")
        with self._lock:
            if any(item.name == hook_name for item in self._hooks):
                raise ValueError("hook name is already registered")
            self._hooks.append(
                _HookRegistration(
                    name=hook_name,
                    phase=phase,
                    callback=callback,
                    matcher=str(matcher).strip(),
                )
            )

    def unregister(self, name: str) -> None:
        hook_name = str(name).strip()
        with self._lock:
            self._hooks = [item for item in self._hooks if item.name != hook_name]

    def names(self) -> tuple[str, ...]:
        with self._lock:
            return tuple(item.name for item in self._hooks)

    def dispatch(self, event: HookEvent) -> HookDispatchResult:
        results: list[HookResult] = []
        context: list[str] = []
        messages: list[str] = []
        errors: list[dict[str, str]] = []
        with self._lock:
            registrations = tuple(self._hooks)
        for registration in registrations:
            if registration.phase != event.phase:
                continue
            if registration.matcher and registration.matcher != event.tool_name:
                continue
            try:
                result = self._normalize_result(registration.callback(event))
                if result is None:
                    continue
                results.append(result)
                if result.context.strip():
                    context.append(result.context)
                if result.message.strip():
                    messages.append(result.message)
                if result.cancel:
                    break
            except Exception as exc:  # noqa: BLE001 - one hook cannot crash the loop
                errors.append(
                    {
                        "hook": "<redacted-hook>",
                        "error_type": type(exc).__name__,
                        "code": "hook_callback_failed",
                    }
                )
        return HookDispatchResult(
            results=tuple(results),
            context=tuple(context),
            messages=tuple(messages),
            errors=tuple(errors),
        )

    @staticmethod
    def _normalize_result(value: HookResult | Mapping[str, Any] | None) -> HookResult | None:
        if value is None:
            return None
        if isinstance(value, HookResult):
            return HookResult(
                cancel=bool(value.cancel),
                message=redact_credential_value(str(value.message or "")),
                context=redact_credential_value(str(value.context or "")),
            )
        if not isinstance(value, Mapping):
            raise TypeError("hook result must be a HookResult or mapping")
        context = value.get("context", value.get("additional_context", ""))
        message = value.get("message", "")
        return HookResult(
            cancel=bool(value.get("cancel", value.get("block", False))),
            message=redact_credential_value(str(message or "")),
            context=redact_credential_value(str(context or "")),
        )


@dataclass(frozen=True, slots=True)
class CommandResult:
    """Result returned by one named command extension."""

    handled: bool = True
    success: bool = True
    text: str = ""


CommandCallback = Callable[[str, Mapping[str, Any]], CommandResult | str | None]


class CommandRegistry:
    """Registry for injected slash-command handlers."""

    def __init__(self) -> None:
        self._commands: dict[str, CommandCallback] = {}
        self._lock = RLock()

    def register(self, name: str, callback: CommandCallback) -> None:
        command = self._normalize_name(name)
        if not callable(callback):
            raise TypeError("command callback must be callable")
        with self._lock:
            if command in self._commands:
                raise ValueError("command name is already registered")
            self._commands[command] = callback

    def unregister(self, name: str) -> None:
        with self._lock:
            self._commands.pop(self._normalize_name(name), None)

    def names(self) -> tuple[str, ...]:
        with self._lock:
            return tuple(self._commands)

    def dispatch(self, text: str, context: Mapping[str, Any] | None = None) -> CommandResult | None:
        raw = str(text or "").strip()
        if not raw.startswith("/"):
            return None
        parts = raw[1:].split(maxsplit=1)
        if not parts:
            return None
        try:
            command = self._normalize_name(parts[0])
        except ValueError:
            return None
        with self._lock:
            callback = self._commands.get(command)
        if callback is None:
            return None
        args = parts[1] if len(parts) == 2 else ""
        try:
            result = callback(args, _freeze_mapping(context or {}))
            if result is None:
                return CommandResult()
            if isinstance(result, CommandResult):
                return CommandResult(
                    handled=bool(result.handled),
                    success=bool(result.success),
                    text=redact_credential_value(str(result.text or "")),
                )
            if isinstance(result, str):
                return CommandResult(text=redact_credential_value(result))
            raise TypeError("command result must be a CommandResult, string, or None")
        except Exception:  # noqa: BLE001 - command failures are user-visible, not process-fatal
            return CommandResult(success=False, text="command failed")

    @staticmethod
    def _normalize_name(name: str) -> str:
        command = str(name).strip().lstrip("/")
        if _COMMAND_NAME.fullmatch(command) is None:
            raise ValueError("command name must match [a-z][a-z0-9_-]*")
        return command


__all__ = [
    "CommandRegistry",
    "CommandResult",
    "HOOK_BLOCKING_PHASES",
    "HOOK_PHASES",
    "HOOK_SCHEMA_VERSION",
    "HookDispatchResult",
    "HookEvent",
    "HookRegistry",
    "HookResult",
]
