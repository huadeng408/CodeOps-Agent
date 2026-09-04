from __future__ import annotations

import threading
from collections import deque
from dataclasses import dataclass, field
from typing import Deque


class SessionOperationBusy(RuntimeError):
    """Raised when an idle-only operation cannot start for a session."""


class SessionOperationCancelled(RuntimeError):
    """Raised when a queued session operation is canceled before it starts."""


@dataclass(slots=True)
class _SessionState:
    active_kind: str | None = None
    active_token: object | None = None
    converse_waiters: Deque[object] = field(default_factory=deque)


class SessionOperationLease:
    """Idempotent lease returned by the session operation coordinator."""

    def __init__(
        self,
        coordinator: SessionOperationCoordinator,
        session_key: str | None,
        token: object | None,
    ) -> None:
        self._coordinator = coordinator
        self._session_key = session_key
        self._token = token
        self._released = False

    def release(self) -> None:
        if self._released:
            return
        self._released = True
        self._coordinator._release(self._session_key, self._token)

    def __enter__(self) -> SessionOperationLease:
        return self

    def __exit__(self, exc_type, exc, traceback) -> None:
        self.release()


class SessionOperationCoordinator:
    """Serialize mutating RPCs for one durable session.

    Conversations wait FIFO. Manual compaction is intentionally fail-fast: it
    may only start while the session has neither an active operation nor a
    queued prompt.
    """

    def __init__(self, *, wait_poll_seconds: float = 0.05) -> None:
        self._condition = threading.Condition()
        self._states: dict[str, _SessionState] = {}
        self._wait_poll_seconds = max(0.001, float(wait_poll_seconds))

    @staticmethod
    def _session_key(session_id: str) -> str | None:
        value = str(session_id or "").strip()
        return value or None

    def acquire_converse(
        self,
        session_id: str,
        cancel_event: threading.Event | None = None,
    ) -> SessionOperationLease:
        key = self._session_key(session_id)
        if cancel_event is not None and cancel_event.is_set():
            raise SessionOperationCancelled("conversation canceled before acquiring session")
        if key is None:
            return SessionOperationLease(self, None, None)

        token = object()
        with self._condition:
            state = self._states.setdefault(key, _SessionState())
            state.converse_waiters.append(token)
            try:
                while state.active_kind is not None or state.converse_waiters[0] is not token:
                    if cancel_event is not None and cancel_event.is_set():
                        state.converse_waiters.remove(token)
                        self._cleanup_state(key, state)
                        self._condition.notify_all()
                        raise SessionOperationCancelled(
                            "conversation canceled while waiting for session"
                        )
                    self._condition.wait(timeout=self._wait_poll_seconds)
                state.converse_waiters.popleft()
                state.active_kind = "converse"
                state.active_token = token
                return SessionOperationLease(self, key, token)
            except Exception:
                if token in state.converse_waiters:
                    state.converse_waiters.remove(token)
                    self._cleanup_state(key, state)
                    self._condition.notify_all()
                raise

    def try_acquire_compact(self, session_id: str) -> SessionOperationLease:
        key = self._session_key(session_id)
        if key is None:
            return SessionOperationLease(self, None, None)
        with self._condition:
            state = self._states.setdefault(key, _SessionState())
            if state.active_kind is not None or state.converse_waiters:
                raise SessionOperationBusy("session is busy")
            token = object()
            state.active_kind = "compact"
            state.active_token = token
            return SessionOperationLease(self, key, token)

    def _release(self, session_key: str | None, token: object | None) -> None:
        if session_key is None or token is None:
            return
        with self._condition:
            state = self._states.get(session_key)
            if state is None or state.active_token is not token:
                return
            state.active_kind = None
            state.active_token = None
            self._cleanup_state(session_key, state)
            self._condition.notify_all()

    def _cleanup_state(self, key: str, state: _SessionState) -> None:
        if state.active_kind is None and not state.converse_waiters:
            self._states.pop(key, None)
