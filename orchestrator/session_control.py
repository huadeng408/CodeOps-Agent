"""Versioned requests for Go-owned Session history controls."""

from __future__ import annotations

from dataclasses import dataclass


API_VERSION = "v1"


def _target_seq(value: int) -> int:
    if isinstance(value, bool) or not isinstance(value, int) or value < 0:
        raise ValueError("target_seq must be a non-negative integer")
    return value


def _session_id(value: str, field: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{field} must be a non-empty string")
    return value.strip()


@dataclass(frozen=True, slots=True)
class SessionForkRequest:
    target_session_id: str
    target_seq: int
    api_version: str = API_VERSION

    def __post_init__(self) -> None:
        if self.api_version != API_VERSION:
            raise ValueError(f"unsupported session control api_version: {self.api_version!r}")
        object.__setattr__(self, "target_session_id", _session_id(self.target_session_id, "target_session_id"))
        object.__setattr__(self, "target_seq", _target_seq(self.target_seq))

    def to_dict(self) -> dict[str, object]:
        return {
            "api_version": self.api_version,
            "operation": "fork",
            "target_session_id": self.target_session_id,
            "target_seq": self.target_seq,
        }


@dataclass(frozen=True, slots=True)
class SessionRewindRequest:
    target_seq: int
    api_version: str = API_VERSION

    def __post_init__(self) -> None:
        if self.api_version != API_VERSION:
            raise ValueError(f"unsupported session control api_version: {self.api_version!r}")
        object.__setattr__(self, "target_seq", _target_seq(self.target_seq))

    def to_dict(self) -> dict[str, object]:
        return {
            "api_version": self.api_version,
            "operation": "rewind",
            "target_seq": self.target_seq,
        }


class SessionControl:
    """Factory facade used by Python orchestration code and tool adapters."""

    api_version = API_VERSION

    @staticmethod
    def fork(target_session_id: str, target_seq: int) -> dict[str, object]:
        return SessionForkRequest(target_session_id, target_seq).to_dict()

    @staticmethod
    def rewind(target_seq: int) -> dict[str, object]:
        return SessionRewindRequest(target_seq).to_dict()
