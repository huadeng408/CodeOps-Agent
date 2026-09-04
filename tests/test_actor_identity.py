from __future__ import annotations

import pytest

from orchestrator.identity import (
    ActorIdentity,
    ActorIdentityError,
    ActorSessionRegistry,
)


def test_actor_identity_requires_versioned_trust_fields() -> None:
    actor = ActorIdentity.from_wire(
        {
            "schema_version": 1,
            "actor_id": "user:42",
            "subject": "alice",
            "tenant_id": "org:7",
            "roles": ["USER"],
            "session_id": "session-1",
        }
    )
    actor.validate()
    assert actor.session_id == "session-1"

    with pytest.raises(ActorIdentityError):
        ActorIdentity.from_wire({"actor_id": "user:42", "subject": "alice"}).validate()


def test_actor_session_registry_rejects_cross_actor_reuse() -> None:
    registry = ActorSessionRegistry()
    first = ActorIdentity(
        schema_version=1,
        actor_id="user:42",
        subject="alice",
        tenant_id="org:7",
        roles=("USER",),
        session_id="session-1",
    )
    second = ActorIdentity(
        schema_version=1,
        actor_id="user:99",
        subject="bob",
        tenant_id="org:7",
        roles=("USER",),
        session_id="session-1",
    )

    registry.authorize("session-1", first)
    registry.authorize("session-1", first)
    with pytest.raises(ActorIdentityError, match="session actor binding mismatch"):
        registry.authorize("session-1", second)


def test_actor_session_registry_requires_session_binding() -> None:
    registry = ActorSessionRegistry()
    actor = ActorIdentity(
        schema_version=1,
        actor_id="user:42",
        subject="alice",
        tenant_id="org:7",
        roles=("USER",),
        session_id="",
    )
    with pytest.raises(ActorIdentityError):
        registry.authorize("session-1", actor)


def test_actor_scope_includes_subject_tenant_and_roles() -> None:
    base = ActorIdentity(1, "user:42", "alice", "org:7", ("USER", "REVIEWER"), "session-1")
    assert base.scope_key != ActorIdentity(1, "user:42", "mallory", "org:7", base.roles, "session-1").scope_key
    assert base.scope_key != ActorIdentity(1, "user:42", "alice", "org:8", base.roles, "session-1").scope_key
    assert base.scope_key != ActorIdentity(1, "user:42", "alice", "org:7", ("USER",), "session-1").scope_key
    assert base.scope_key == ActorIdentity(1, "user:42", "alice", "org:7", ("reviewer", "user"), "session-1").scope_key


def test_chat_request_carries_actor_context_separately_from_user_profile() -> None:
    from orchestrator.rag.models import ActorContext, ChatStreamRequest

    payload = ChatStreamRequest.model_validate(
        {
            "query": "private query",
            "user": {"id": 42, "username": "alice", "role": "USER"},
            "actor": {
                "schema_version": 1,
                "actor_id": "user:42",
                "subject": "alice",
                "tenant_id": "tenant:default",
                "roles": ["USER"],
                "session_id": "user:42",
            },
        }
    )
    assert isinstance(payload.actor, ActorContext)
    assert payload.actor.actor_id == "user:42"
