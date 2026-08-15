from __future__ import annotations

import asyncio
from dataclasses import dataclass
import re
import time
from urllib.parse import urlparse

import httpx

_LOOPBACK_HOSTS = {"127.0.0.1", "::1", "localhost"}
_PROJECT_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$")
_OBSERVATION_ID_RE = re.compile(r"(?<![A-Za-z0-9_])#([1-9][0-9]*)\b")
_SENSITIVE_PATTERNS = (
    re.compile(r"(?i)(?:api[_-]?key|access[_-]?key|secret|password|passwd|token)\s*[:=]"),
    re.compile(r"(?i)\bauthorization\s*:\s*bearer\s+\S+"),
    re.compile(r"(?i)\bbearer\s+[A-Za-z0-9._~-]{16,}"),
    re.compile(r"-----BEGIN [A-Z ]*PRIVATE KEY-----"),
    re.compile(r"(?i)(?:[A-Z]:\\Users\\|\\\\[^\\]+\\(?:Users|Profiles)\\|/(?:home|Users)/)"),
    re.compile(r"(?i)\b(?:postgres|mysql|redis|mongodb(?:\+srv)?)://[^\s/@:]+(?::[^\s/@]+)?@"),
)


@dataclass(frozen=True)
class MemoryReadOutcome:
    """Allowlisted L3 metadata suitable for a caller-owned Phoenix span."""

    backend: str
    latency_ms: int
    citation_ids: tuple[int, ...]
    failure_category: str | None = None

    def telemetry_attributes(self) -> dict[str, str | int]:
        result: dict[str, str | int] = {
            "memory.backend": self.backend,
            "memory.latency_ms": self.latency_ms,
            "memory.result_count": len(self.citation_ids),
            "memory.citation_ids": ",".join(map(str, self.citation_ids)),
        }
        if self.failure_category:
            result["memory.failure_category"] = self.failure_category
        return result


class ClaudeMemClient:
    """Boundary adapter for Claude-Mem; benchmark modes never read L3."""

    def __init__(
        self,
        worker_url: str,
        *,
        transport: httpx.AsyncBaseTransport | None = None,
        timeout_seconds: float = 1.0,
    ) -> None:
        self.worker_url = worker_url.rstrip("/")
        self._transport = transport
        self._timeout_seconds = timeout_seconds
        self.last_outcome = MemoryReadOutcome("claude_mem", 0, (), "disabled")

    async def context(
        self,
        project: str,
        *,
        query: str = "*",
        memory_mode: str = "disabled",
    ) -> str:
        started = time.monotonic()
        if memory_mode != "enabled":
            return self._finish("", started, failure_category="disabled")
        if not _is_loopback_worker_url(self.worker_url) or not _valid_request(
            project, query
        ):
            return self._finish("", started, failure_category="invalid_input")

        try:
            async with asyncio.timeout(self._timeout_seconds):
                async with httpx.AsyncClient(
                    base_url=self.worker_url,
                    transport=self._transport,
                    timeout=httpx.Timeout(self._timeout_seconds),
                    trust_env=False,
                ) as http:
                    search = await http.get(
                        "/api/search",
                        params={"query": query.strip(), "project": project, "limit": 3},
                    )
                    search.raise_for_status()
                    observation_ids = _observation_ids(search.json())
                    if not observation_ids:
                        return self._finish("", started)

                    remaining = self._timeout_seconds - (time.monotonic() - started)
                    if remaining <= 0:
                        return self._finish("", started, failure_category="timeout")

                    batch = await http.post(
                        "/api/observations/batch",
                        json={"ids": observation_ids, "project": project},
                        timeout=httpx.Timeout(remaining),
                    )
                    batch.raise_for_status()
                    context, citation_ids, failure_category = _render_context(
                        batch.json(), project, observation_ids
                    )
                    return self._finish(
                        context,
                        started,
                        citation_ids=citation_ids,
                        failure_category=failure_category,
                    )
        except TimeoutError:
            return self._finish("", started, failure_category="timeout")
        except httpx.TimeoutException:
            return self._finish("", started, failure_category="timeout")
        except httpx.HTTPError:
            return self._finish("", started, failure_category="http_error")
        except (TypeError, ValueError):
            return self._finish("", started, failure_category="malformed_response")

    def _finish(
        self,
        context: str,
        started: float,
        *,
        citation_ids: tuple[int, ...] = (),
        failure_category: str | None = None,
    ) -> str:
        self.last_outcome = MemoryReadOutcome(
            "claude_mem",
            int((time.monotonic() - started) * 1000),
            citation_ids,
            failure_category,
        )
        return context


def _is_loopback_worker_url(value: str) -> bool:
    parsed = urlparse(value)
    return (
        parsed.scheme == "http"
        and parsed.hostname in _LOOPBACK_HOSTS
        and not parsed.username
        and not parsed.password
    )


def _valid_request(project: str, query: str) -> bool:
    return bool(_PROJECT_RE.fullmatch(project)) and bool(query.strip()) and len(query) <= 256


def _observation_ids(payload: object) -> list[int]:
    if not isinstance(payload, dict):
        return []

    content = payload.get("content")
    if not isinstance(content, list):
        return []

    result: list[int] = []
    for item in content:
        if not isinstance(item, dict) or item.get("type") != "text":
            continue
        text = item.get("text")
        if not isinstance(text, str):
            continue
        for match in _OBSERVATION_ID_RE.finditer(text):
            observation_id = int(match.group(1))
            if observation_id not in result:
                result.append(observation_id)
            if len(result) == 3:
                return result
    return result


def _render_context(
    payload: object,
    project: str,
    requested_ids: list[int],
) -> tuple[str, tuple[int, ...], str | None]:
    records: list[object]
    if isinstance(payload, list):
        records = payload
    elif isinstance(payload, dict) and isinstance(payload.get("id"), int):
        records = [payload]
    elif isinstance(payload, dict) and isinstance(payload.get("observations"), list):
        records = payload["observations"]
    else:
        return "", (), "malformed_response"

    rendered: list[str] = []
    citation_ids: list[int] = []
    for record in records:
        if not isinstance(record, dict):
            return "", (), "malformed_response"
        observation_id = record.get("id")
        title = record.get("title")
        if (
            not isinstance(observation_id, int)
            or observation_id not in requested_ids
            or record.get("project") != project
            or not isinstance(title, str)
        ):
            return "", (), "malformed_response"
        rendered.append(f"[claude-mem:{observation_id}] {title}")
        citation_ids.append(observation_id)

    result = "\n".join(rendered)
    if _contains_sensitive_content(result):
        return "", (), "privacy_rejected"
    return _truncate_utf8(result), tuple(citation_ids), None


def _contains_sensitive_content(value: str) -> bool:
    return any(pattern.search(value) for pattern in _SENSITIVE_PATTERNS)


def _truncate_utf8(value: str, maximum_bytes: int = 4096) -> str:
    encoded = value.encode("utf-8")[:maximum_bytes]
    while encoded:
        try:
            return encoded.decode("utf-8")
        except UnicodeDecodeError:
            encoded = encoded[:-1]
    return ""
