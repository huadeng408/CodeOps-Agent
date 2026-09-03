from __future__ import annotations

import pytest

from orchestrator.session_control import (
    API_VERSION,
    SessionControl,
    SessionForkRequest,
    SessionRewindRequest,
)


def test_session_control_requests_are_versioned_and_serializable() -> None:
    assert SessionControl.fork("child", 7) == {
        "api_version": API_VERSION,
        "operation": "fork",
        "target_session_id": "child",
        "target_seq": 7,
    }
    assert SessionControl.rewind(3) == {
        "api_version": "v1",
        "operation": "rewind",
        "target_seq": 3,
    }


@pytest.mark.parametrize(
    "factory",
    [
        lambda: SessionForkRequest("", 0),
        lambda: SessionForkRequest("child", -1),
        lambda: SessionForkRequest("child", 1, "v0"),
        lambda: SessionRewindRequest(-1),
        lambda: SessionRewindRequest(1, "v0"),
    ],
)
def test_session_control_requests_fail_closed(factory) -> None:
    with pytest.raises(ValueError):
        factory()
