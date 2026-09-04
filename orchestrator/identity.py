from __future__ import annotations

import threading
from collections.abc import Mapping
from dataclasses import dataclass
from typing import Any

SCHEMA_VERSION = 1


class ActorIdentityError(ValueError):
    """Raised when an actor cannot be trusted for a session operation."""


@dataclass(frozen=True, slots=True)
class ActorIdentity:
    schema_version: int
    actor_id: str
    subject: str
    tenant_id: str
    roles: tuple[str, ...]
    session_id: str

    @classmethod
    def from_wire(cls, value: Mapping[str, Any] | None) -> ActorIdentity:
        if not isinstance(value, Mapping):
            raise ActorIdentityError("actor context is required")
        roles = value.get("roles", ())
        if isinstance(roles, str) or not isinstance(roles, (list, tuple)):
            raise ActorIdentityError("actor roles must be a list")
        return cls(
            schema_version=int(value.get("schema_version", 0)),
            actor_id=str(value.get("actor_id", "")),
            subject=str(value.get("subject", "")),
            tenant_id=str(value.get("tenant_id", "")),
            roles=tuple(str(item) for item in roles),
            session_id=str(value.get("session_id", "")),
        )

    @classmethod
    def from_proto(cls, value: Any | None) -> ActorIdentity:
        if value is None:
            raise ActorIdentityError("actor context is required")
        return cls(
            schema_version=int(value.schema_version),
            actor_id=str(value.actor_id),
            subject=str(value.subject),
            tenant_id=str(value.tenant_id),
            roles=tuple(str(item) for item in value.roles),
            session_id=str(value.session_id),
        )

    def validate(self) -> None:
        if self.schema_version != SCHEMA_VERSION:
            raise ActorIdentityError("unsupported actor schema version")
        for name, value in (
            ("actor_id", self.actor_id),
            ("subject", self.subject),
            ("tenant_id", self.tenant_id),
            ("session_id", self.session_id),
        ):
            _validate_field(name, value)
        if not self.roles:
            raise ActorIdentityError("actor roles are required")
        for role in self.roles:
            _validate_field("role", role)

    def validate_session(self, session_id: str) -> None:
        self.validate()
        if self.session_id != str(session_id).strip():
            raise ActorIdentityError("actor session binding mismatch")

    def to_wire(self) -> dict[str, Any]:
        self.validate()
        return {
            "schema_version": self.schema_version,
            "actor_id": self.actor_id,
            "subject": self.subject,
            "tenant_id": self.tenant_id,
            "roles": list(self.roles),
            "session_id": self.session_id,
        }

    @property
    def scope_key(self) -> str:
        roles = ",".join(sorted({role.strip().upper() for role in self.roles if role.strip()}))
        return f"{self.actor_id}\x00{self.subject}\x00{self.tenant_id}\x00{roles}\x00{self.session_id}"


def _validate_field(name: str, value: str) -> None:
    clean = str(value).strip()
    if not clean:
        raise ActorIdentityError(f"{name} is required")
    if len(clean) > 256 or any(ord(char) < 32 for char in clean):
        raise ActorIdentityError(f"{name} is invalid")


class ActorSessionRegistry:
    """Process-local session ownership fence for the orchestrator runtime."""

    def __init__(self) -> None:
        self._lock = threading.RLock()
        self._owners: dict[str, str] = {}

    def authorize(self, session_id: str, actor: ActorIdentity) -> None:
        session_id = str(session_id).strip()
        actor.validate_session(session_id)
        with self._lock:
            owner = self._owners.get(session_id)
            if owner is None:
                self._owners[session_id] = actor.scope_key
                return
            if owner != actor.scope_key:
                raise ActorIdentityError("session actor binding mismatch")

    def clear(self, session_id: str, actor: ActorIdentity) -> None:
        session_id = str(session_id).strip()
        actor.validate_session(session_id)
        with self._lock:
            if self._owners.get(session_id) == actor.scope_key:
                self._owners.pop(session_id, None)
