from __future__ import annotations

import threading
import time

import pytest

from orchestrator.runtime.session_ops import (
    SessionOperationBusy,
    SessionOperationCancelled,
    SessionOperationCoordinator,
)


def test_compact_is_rejected_while_converse_lease_is_active():
    coordinator = SessionOperationCoordinator()
    lease = coordinator.acquire_converse("session-1")
    try:
        with pytest.raises(SessionOperationBusy):
            coordinator.try_acquire_compact("session-1")
    finally:
        lease.release()


def test_converse_waits_for_active_compaction_then_acquires():
    coordinator = SessionOperationCoordinator(wait_poll_seconds=0.005)
    compact_lease = coordinator.try_acquire_compact("session-1")
    acquired = threading.Event()
    release = threading.Event()

    def run_converse():
        lease = coordinator.acquire_converse("session-1")
        acquired.set()
        release.wait(timeout=2)
        lease.release()

    thread = threading.Thread(target=run_converse)
    thread.start()
    try:
        assert not acquired.wait(timeout=0.05)
        compact_lease.release()
        assert acquired.wait(timeout=2)
    finally:
        release.set()
        thread.join(timeout=2)
        assert not thread.is_alive()


def test_canceled_converse_waiter_is_removed():
    coordinator = SessionOperationCoordinator(wait_poll_seconds=0.005)
    compact_lease = coordinator.try_acquire_compact("session-1")
    cancel_event = threading.Event()
    finished = threading.Event()
    outcome: list[object] = []

    def run_converse():
        try:
            coordinator.acquire_converse("session-1", cancel_event)
        except SessionOperationCancelled:
            outcome.append("canceled")
        finally:
            finished.set()

    thread = threading.Thread(target=run_converse)
    thread.start()
    try:
        time.sleep(0.03)
        cancel_event.set()
        assert finished.wait(timeout=2)
        assert outcome == ["canceled"]
        compact_lease.release()
        next_lease = coordinator.acquire_converse("session-1")
        next_lease.release()
    finally:
        compact_lease.release()
        thread.join(timeout=2)
        assert not thread.is_alive()


def test_compact_is_rejected_when_prompts_are_already_queued():
    coordinator = SessionOperationCoordinator(wait_poll_seconds=0.005)
    compact_lease = coordinator.try_acquire_compact("session-1")
    waiter_started = threading.Event()
    waiter_cancel = threading.Event()

    def wait_for_converse():
        waiter_started.set()
        with pytest.raises(SessionOperationCancelled):
            coordinator.acquire_converse("session-1", waiter_cancel)

    thread = threading.Thread(target=wait_for_converse)
    thread.start()
    try:
        assert waiter_started.wait(timeout=1)
        time.sleep(0.03)
        with pytest.raises(SessionOperationBusy):
            coordinator.try_acquire_compact("session-1")
        waiter_cancel.set()
        thread.join(timeout=2)
        assert not thread.is_alive()
    finally:
        compact_lease.release()
        waiter_cancel.set()
        thread.join(timeout=2)
