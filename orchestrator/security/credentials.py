"""Credential-shape redaction shared by persisted and exported artifacts."""

from __future__ import annotations

import re
from collections.abc import Iterable
from typing import Any

_BEARER_PATTERN = re.compile(r"(?i)(\bBearer\s+)[A-Za-z0-9][A-Za-z0-9._~+/=-]{5,}")
_SK_PATTERN = re.compile(r"(?i)\bsk-[A-Za-z0-9][A-Za-z0-9._-]{8,}")
_DSN_PATTERN = re.compile(r"(?i)(\b[a-z][a-z0-9+.-]*://[^/\s:@]+:)[^/\s@]+(@)")
_AWS_ACCESS_KEY_PATTERN = re.compile(r"\b(?:AKIA|ASIA)[0-9A-Z]{16}\b")
_ASSIGNMENT_PATTERN = re.compile(
    r"(?i)(\b(?:openai|anthropic|deepseek|azure)?[_-]?"
    r"(?:api[_-]?key|access[_-]?token|refresh[_-]?token|password|passwd|secret)"
    r"\s*[:=]\s*)(?:['\"][^'\"\r\n]+['\"]|[^\s,;]+)"
)
_AUTHORIZATION_PATTERN = re.compile(
    r"(?i)(\bauthorization\s*[:=]\s*)(?!(?:['\"])?Bearer\b)"
    r"(?:['\"][^'\"\r\n]+['\"]|[^\s,;]+)"
)
_PRIVATE_KEY_PATTERN = re.compile(
    r"-----BEGIN [^-]+-----.*?-----END [^-]+-----",
    re.DOTALL,
)
_ABSOLUTE_PATH_PATTERN = re.compile(
    r"(?i)(?<![A-Za-z0-9_.-])"
    r"(?:[A-Z]:[\\/][^\r\n'\";,]+|/(?:[^\s'\"]+[\\/])+[^\r\n'\";,]+)"
)


def redact_credential_text(text: str, secrets: Iterable[str] = ()) -> str:
    """Replace explicit secret values and common provider credential shapes."""
    protected = text
    for secret in secrets:
        if secret and not secret.isspace():
            protected = protected.replace(secret, "<redacted>")
    protected = _BEARER_PATTERN.sub(r"\1<redacted>", protected)
    protected = _SK_PATTERN.sub("<redacted>", protected)
    protected = _DSN_PATTERN.sub(r"\1<redacted>\2", protected)
    protected = _AWS_ACCESS_KEY_PATTERN.sub("<redacted>", protected)
    protected = _ASSIGNMENT_PATTERN.sub(r"\1<redacted>", protected)
    protected = _AUTHORIZATION_PATTERN.sub(r"\1<redacted>", protected)
    protected = _PRIVATE_KEY_PATTERN.sub("<redacted>", protected)
    return _ABSOLUTE_PATH_PATTERN.sub("<redacted-path>", protected)


def redact_credential_value(value: Any) -> Any:
    """Redact credential-shaped strings recursively in JSON-compatible data."""
    if isinstance(value, str):
        return redact_credential_text(value)
    if isinstance(value, dict):
        return {key: redact_credential_value(item) for key, item in value.items()}
    if isinstance(value, list):
        return [redact_credential_value(item) for item in value]
    if isinstance(value, tuple):
        return tuple(redact_credential_value(item) for item in value)
    return value


__all__ = ["redact_credential_text", "redact_credential_value"]
