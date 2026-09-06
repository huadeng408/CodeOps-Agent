from __future__ import annotations

import base64
import json
import logging
import random
import re
import socket
import threading
import time
import urllib.error
import urllib.request
from abc import ABC, abstractmethod
from dataclasses import dataclass, field
from typing import Any

_logger = logging.getLogger(__name__)

# Matches a standalone image data-URI run inside message content, terminated by
# whitespace or end-of-string. The base64 alphabet is A-Za-z0-9+/ with = padding.
_IMAGE_DATA_URI_RE = re.compile(
    r"data:image/[A-Za-z0-9.+-]+;base64,[A-Za-z0-9+/=]*"
)


def split_data_uri(text: str) -> tuple[str, str] | None:
    """If *text* is a single ``data:<mime>;base64,<payload>`` URI, return
    ``(media_type, base64_payload)``; otherwise return ``None``.

    Used by provider adapters to decide whether a message content string is an
    image payload that should be turned into a native multimodal block.
    """
    if not isinstance(text, str) or not text.startswith("data:"):
        return None
    rest = text[len("data:"):]
    marker = ";base64,"
    idx = rest.find(marker)
    if idx < 0:
        return None
    media_type = rest[:idx]
    payload = rest[idx + len(marker):]
    if not media_type or not payload:
        return None
    return media_type, payload


def split_image_segments(text: str) -> list[tuple[str, str]]:
    """Split *text* into ordered ``(kind, value)`` segments.

    ``kind`` is ``"text"`` (value is a literal string) or ``"image"`` (value is
    the full matched ``data:image/...;base64,...`` URI). Adjacent text between
    image URIs is preserved as ``"text"`` segments so providers can rebuild a
    mixed text+image content array. Returns an empty list for empty input.
    """
    if not isinstance(text, str) or not text:
        return []
    segments: list[tuple[str, str]] = []
    pos = 0
    for match in _IMAGE_DATA_URI_RE.finditer(text):
        if match.start() > pos:
            segments.append(("text", text[pos:match.start()]))
        segments.append(("image", match.group(0)))
        pos = match.end()
    if pos < len(text):
        segments.append(("text", text[pos:]))
    return segments


MessageContent = str | list[dict[str, Any]]


def split_content_segments(content: MessageContent) -> list[tuple[str, str]]:
    """Normalize text or structured content into ordered text/image segments."""
    if isinstance(content, str):
        return split_image_segments(content)

    segments: list[tuple[str, str]] = []
    for block in content:
        block_type = str(block.get("type", "")).strip().lower()
        if block_type == "text":
            text = str(block.get("text", ""))
            if text:
                segments.append(("text", text))
            continue
        if block_type != "image":
            continue
        mime = str(block.get("mime", block.get("mime_type", ""))).strip()
        blob = block.get("data", block.get("image_blob", b""))
        if isinstance(blob, str):
            payload = blob
        elif isinstance(blob, (bytes, bytearray, memoryview)):
            payload = base64.b64encode(bytes(blob)).decode("ascii")
        else:
            payload = ""
        if mime.startswith("image/") and payload:
            segments.append(("image", f"data:{mime};base64,{payload}"))
    return segments


def message_content_text(content: MessageContent) -> str:
    """Return a log/recovery-safe textual view without embedding image bytes."""
    if isinstance(content, str):
        return content
    parts: list[str] = []
    for block in content:
        block_type = str(block.get("type", "")).strip().lower()
        if block_type == "text":
            text = str(block.get("text", ""))
            if text:
                parts.append(text)
        elif block_type == "image":
            mime = str(block.get("mime", block.get("mime_type", "image/*")))
            parts.append(f"[image content: {mime}]")
    return "\n".join(parts)


# Polling interval (seconds) for the abortable HTTP path. While an in-flight
# request is running in a worker thread, the caller polls the cancel event this
# often so that a user interrupt is noticed within ~CANCEL_POLL_INTERVAL_S.
CANCEL_POLL_INTERVAL_S: float = 0.25

# Retry configuration for LLM provider HTTP clients.
_RETRYABLE_HTTP_CODES: frozenset[int] = frozenset({429, 500, 502, 503, 504})
_NON_RETRYABLE_HTTP_CODES: frozenset[int] = frozenset({400, 401, 402, 403, 404})
_MAX_RETRY_DELAY_S: float = 60.0
_BASE_DELAY_S: float = 1.0
_MAX_JITTER_FACTOR: float = 0.25


@dataclass(slots=True)
class ComplexityScore:
    """Floating-point complexity rating for a user request.

    0.0 = simplest (simple lookup), 1.0 = most complex (multi-step refactor).
    The fast model is recommended when score <= COMPLEXITY_FAST_THRESHOLD.
    """

    score: float
    reason: str


COMPLEXITY_FAST_THRESHOLD: float = 0.40

# Simple query patterns (weak signals that suggest fast-model suitability).
_COMPLEXITY_SIMPLE_PATTERNS: list[tuple[str, float]] = [
    (r"\b(?:what|where|which)\s+(?:is|are|file|function|directory|package|module)\b", 0.05),
    (r"\b(?:show|display|print|list|reveal)\b", 0.05),
    (r"\b(?:find|search|locate|grep)\b", 0.05),
    (r"\b(?:read|open|view|cat|check|inspect)\b", 0.05),
    (r"\b(?:look\s+(?:at|up|into))\b", 0.05),
    (r"\b(?:tell\s+me|explain|describe|summarize)\b", 0.05),
    (r"\b(?:how\s+(?:many|much))\b", 0.05),
]

# Complex task patterns (strong signals that demand the main model).
_COMPLEXITY_COMPLEX_PATTERNS: list[tuple[str, float]] = [
    (r"\[plan\s*mode\]", 0.50),
    (r"\b(?:refactor|rewrite|restructure|redesign|rearchitect)\b", 0.40),
    (r"\b(?:multi[-\s]?file|several\s+files|multiple\s+files|across\s+files)\b", 0.30),
    (r"\b(?:implement|build|create)\s+(?:a\s+)?(?:new\s+)?(?:feature|module|system|service|api|endpoint)\b", 0.25),
    (r"\b(?:agent|spawn|sub[-\s]?agent|parallel\s+agent)\b", 0.30),
    (r"\b(?:fix|debug|resolve)\s+(?:this|an?\s+)?(?:error|bug|issue|crash)\b", 0.20),
    (r"\b(?:plan|design|architecture|workflow|pipeline)\b", 0.15),
    (r"\b(?:migrate|upgrade|downgrade|deploy|release)\b", 0.25),
    (r"\b(?:test|coverage|benchmark|profile)\b", 0.10),
    (r"\b(?:review|audit|security\s+(?:review|audit|scan))\b", 0.15),
]


def assess_complexity(user_text: str) -> ComplexityScore:
    """Assess task complexity from the user message alone.

    Returns a ComplexityScore whose *score* ranges from 0.0 (simplest) to
    1.0 (most complex).  Scores at or below COMPLEXITY_FAST_THRESHOLD
    recommend routing to the fast model.
    """
    text_lower = user_text.lower().strip()

    score = 0.0
    reasons: list[str] = []

    for pattern, weight in _COMPLEXITY_SIMPLE_PATTERNS:
        if re.search(pattern, text_lower):
            score -= weight * 0.6
            reasons.append(f"simple:{pattern}")

    for pattern, weight in _COMPLEXITY_COMPLEX_PATTERNS:
        if re.search(pattern, text_lower):
            score += weight
            reasons.append(f"complex:{pattern}")

    # Length-based adjustment.
    word_count = len(text_lower.split())
    if word_count < 8:
        score -= 0.08
        reasons.append("short")
    elif word_count > 120:
        score += 0.12
        reasons.append("long")

    # Simple tools mentioned explicitly tilt toward fast model.
    simple_tool_count = 0
    for pattern in (r"\bRead\b", r"\bGlob\b", r"\bGrep\b", r"\bBash\b"):
        if re.search(pattern, user_text):
            simple_tool_count += 1
    if simple_tool_count >= 2:
        score -= 0.10
        reasons.append("simple_tools")

    score = max(0.0, min(1.0, round(score, 3)))
    if not reasons:
        reasons.append("default")

    return ComplexityScore(score=score, reason="; ".join(reasons))


@dataclass(slots=True)
class ChatMessage:
    role: str
    content: MessageContent
    name: str | None = None
    tool_call_id: str | None = None
    tool_calls: list[ToolCall] = field(default_factory=list)
    thinking_blocks: list[dict[str, Any]] = field(default_factory=list)
    is_error: bool = False
    cache_control: str = ""  # "ephemeral" triggers provider-specific cache marking


@dataclass(slots=True)
class ToolCall:
    name: str
    arguments: dict[str, Any] = field(default_factory=dict)
    id: str = ""
    arguments_json: str = "{}"


@dataclass(slots=True)
class Usage:
    input_tokens: int = 0
    output_tokens: int = 0
    cached_input_tokens: int = 0


@dataclass(slots=True)
class ChatRequest:
    model: str
    messages: list[ChatMessage]
    tools: list[dict[str, Any]] = field(default_factory=list)
    temperature: float = 0.2
    thinking_enabled: bool = False
    thinking_budget: int = 10000
    reasoning_effort: str = ""  # "low" | "medium" | "high" for OpenAI reasoning models
    # ``purpose`` distinguishes auxiliary calls such as context compaction
    # from normal agent turns without exposing credentials or raw history.
    purpose: str = ""
    allow_tools: bool = True
    # Optional cancellation handle. When set, providers forward it to
    # http_call_with_retry so an in-flight request can be aborted by the
    # harness when the user interrupts the turn (design 22.8).
    cancel_event: threading.Event | None = None
    # Provider-neutral tool routing hints. OpenAI-compatible providers accept
    # the structured ``tool_choice`` form to force one named function; a
    # ``None`` value preserves the provider's default automatic selection.
    # These fields are appended after the original fields so existing
    # positional callers keep their pre-routing meaning.
    tool_choice: str | dict[str, Any] | None = None
    parallel_tool_calls: bool | None = None


@dataclass(slots=True)
class ChatResponse:
    text: str = ""
    tool_calls: list[ToolCall] = field(default_factory=list)
    thinking_blocks: list[dict[str, Any]] = field(default_factory=list)
    usage: Usage = field(default_factory=Usage)
    # Provider-reported model identity is read from the response body. A
    # *requested* model name is an input, not evidence;
    # only the provider's own answer to "what served this request?" can testify
    # to model identity.  Keys (all optional, blank when the provider is
    # silent): ``requested_model``, ``reported_model``, ``response_id``,
    # ``system_fingerprint``, ``created``, ``identity_verified``.
    # Never contains credentials — it is written verbatim into artifacts.
    model_identity: dict[str, Any] = field(default_factory=dict)


@dataclass(slots=True)
class StreamDelta:
    """One chunk emitted by :meth:`LLMClient.stream` (design 22.6).

    Minimal, documented discriminator contract consumed by the streaming
    conversation path. ``kind`` selects which field is meaningful for a given
    chunk:

    - ``"text"``: a partial assistant-text fragment (``text``).
    - ``"thinking"``: one or more assembled thinking blocks
      (``thinking_blocks``). Optional in v1; consumers may simply accumulate.
    - ``"tool_calls"``: the finalized list of tool calls for the turn
      (``tool_calls``). Emitted at most once, after all text deltas.
    - ``"usage"``: the token :class:`Usage` for the turn (``usage``).
    - ``"done"``: terminal marker; the generator ends right after this delta.
      May carry finalized ``thinking_blocks``.
    """

    kind: str
    text: str = ""
    tool_calls: list[ToolCall] = field(default_factory=list)
    usage: Usage | None = None
    thinking_blocks: list[dict[str, Any]] = field(default_factory=list)


class RequestInterrupted(RuntimeError):
    """The in-flight HTTP request was cancelled via a cancel event.

    Raised by :func:`http_call_with_retry` when the caller-provided
    ``cancel_event`` is set.  Callers that handle this exception should
    stop the current operation cooperatively (do NOT retry).
    """


def is_context_window_exceeded(error: BaseException) -> bool:
    """Recognize provider context-overflow failures without provider coupling."""
    current: BaseException | None = error
    seen: set[int] = set()
    while current is not None and id(current) not in seen:
        seen.add(id(current))
        code = str(getattr(current, "code", "") or "").upper()
        text = str(current).upper()
        normalized = text.replace("_", " ").replace("-", " ")
        if "CONTEXT WINDOW EXCEEDED" in code.replace("_", " ") or "CONTEXT WINDOW EXCEEDED" in normalized:
            return True
        if (
            "CONTEXT LENGTH" in normalized
            or "MAXIMUM CONTEXT" in normalized
            or "TOKEN LIMIT" in normalized
            or "TOO MANY TOKENS" in normalized
            or "PROMPT IS TOO LONG" in normalized
        ):
            return True
        current = current.__cause__ or current.__context__
    return False


class LLMClient(ABC):
    max_retries: int = 3

    @abstractmethod
    async def chat(self, request: ChatRequest) -> ChatResponse:
        raise NotImplementedError

    async def stream(self, request: ChatRequest):
        """Async generator yielding :class:`StreamDelta` chunks for *request*.

        This is the streaming contract backing design 22.6 (incremental text).
        The default implementation wraps :meth:`chat` and emits at most one
        ``"text"`` delta (the whole text), one ``"tool_calls"`` delta, one
        ``"usage"`` delta, then ``"done"`` -- so every client that does not
        override this still gets a correct, single-chunk stream for free.

        Providers that support real SSE override this to yield many small
        ``"text"`` deltas. Overrides MUST keep honouring
        ``request.cancel_event`` cooperatively: check it between chunks and
        raise :class:`RequestInterrupted` when set, so the abortable-stream
        contract from design 22.8 is preserved on the streaming path.
        """
        response = await self.chat(request)
        if response.text:
            yield StreamDelta(kind="text", text=response.text)
        if response.tool_calls:
            yield StreamDelta(
                kind="tool_calls", tool_calls=list(response.tool_calls)
            )
        yield StreamDelta(kind="usage", usage=response.usage)
        yield StreamDelta(
            kind="done", thinking_blocks=list(response.thinking_blocks)
        )


def _parse_retry_after(headers: Any, body: dict[str, Any]) -> float | None:
    """Extract a retry delay in seconds from HTTP response headers or JSON body.

    Checks the ``Retry-After`` header (value in seconds) and the
    ``retry_after_ms`` body field (milliseconds).  Returns *None* when
    neither source provides a usable value.
    """
    # Retry-After header -- plain integer/float seconds.
    raw = None
    if hasattr(headers, "get"):
        raw = headers.get("Retry-After") or headers.get("retry-after")
    if raw:
        if isinstance(raw, str):
            raw = raw.strip()
        try:
            return float(raw)
        except (TypeError, ValueError):
            pass

    # retry_after_ms field in the JSON body (milliseconds to seconds).
    ms = body.get("retry_after_ms")
    if ms is not None:
        try:
            return float(ms) / 1000.0
        except (TypeError, ValueError):
            pass

    return None


def _backoff_delay(attempt: int, retry_after: float | None = None) -> float:
    """Compute exponential-backoff delay with 0-25 % random jitter.

    *attempt* is 1-indexed (first retry is attempt 1).
    When *retry_after* is provided, the delay is clamped by it.
    The total delay is capped at ``_MAX_RETRY_DELAY_S`` (60 s).
    """
    if retry_after is not None and retry_after > 0:
        base = min(retry_after, _MAX_RETRY_DELAY_S)
    else:
        base = min(_BASE_DELAY_S * (2 ** (attempt - 1)), _MAX_RETRY_DELAY_S)
    jitter = base * random.uniform(0, _MAX_JITTER_FACTOR)
    return base + jitter


def _interruptible_sleep(delay: float, cancel_event: threading.Event | None) -> None:
    """Sleep for *delay* seconds, returning early if *cancel_event* is set.

    When *cancel_event* is set during the wait, raises :class:`RequestInterrupted`
    so the caller stops retrying immediately instead of completing the backoff.
    """
    if delay <= 0:
        if cancel_event is not None and cancel_event.is_set():
            raise RequestInterrupted("cancelled during backoff")
        return
    if cancel_event is None:
        time.sleep(delay)
        return
    # Event.wait returns True iff the event was set during the wait.
    if cancel_event.wait(delay):
        raise RequestInterrupted("cancelled during backoff")


def _urlopen_abortable(
    http_request: urllib.request.Request,
    timeout: float,
    cancel_event: threading.Event,
    provider_name: str,
) -> bytes:
    """Run a single ``urlopen`` attempt in a daemon thread, racing it against
    *cancel_event*.

    ``urllib`` does not expose a way to interrupt a blocking ``urlopen`` from
    the outside, and Python threads cannot be force-killed.  To make a user
    interrupt actually stop the *caller* (instead of waiting up to ``timeout``
    seconds), each attempt is executed on a daemon worker thread while the
    calling thread polls *cancel_event*.  When the event fires, the caller
    raises :class:`RequestInterrupted` and returns; the orphaned worker keeps
    running until the socket responds or times out, but its result is discarded
    (it is a daemon, so it never blocks process exit).

    Exceptions raised inside the worker are captured and re-raised on the
    calling thread so the existing retry/backoff logic in
    :func:`http_call_with_retry` applies unchanged.
    """
    holder: dict[str, Any] = {}

    def _worker() -> None:
        try:
            with urllib.request.urlopen(http_request, timeout=timeout) as resp:
                holder["body"] = resp.read()
        except BaseException as exc:  # propagate to caller thread
            holder["error"] = exc

    worker = threading.Thread(
        target=_worker,
        name=f"{provider_name}-http",
        daemon=True,
    )
    worker.start()
    while worker.is_alive():
        if cancel_event.is_set():
            raise RequestInterrupted(
                f"{provider_name} request cancelled mid-flight"
            )
        worker.join(timeout=CANCEL_POLL_INTERVAL_S)
    if cancel_event.is_set():
        raise RequestInterrupted(f"{provider_name} request cancelled")
    if "error" in holder:
        raise holder["error"]
    return holder.get("body", b"")


def http_call_with_retry(
    http_request: urllib.request.Request,
    timeout: float,
    max_retries: int,
    provider_name: str,
    cancel_event: threading.Event | None = None,
) -> bytes:
    """Execute an HTTP request with exponential backoff and retry-after awareness.

    Parameters
    ----------
    http_request:
        A fully-constructed :class:`urllib.request.Request` whose *data* is
        bytes (not a one-shot iterable), so it is safe to resend.
    timeout:
        Per-attempt timeout in seconds.
    max_retries:
        Maximum number of retries (the call is made at most ``max_retries + 1``
        times).
    provider_name:
        Human-readable label used in log messages and error text.
    cancel_event:
        Optional :class:`threading.Event`. When set, the in-flight request is
        aborted (see :func:`_urlopen_abortable`) and no further retries are
        attempted; :class:`RequestInterrupted` is raised instead.  ``None``
        preserves the original blocking behavior.

    Returns
    -------
    bytes
        The raw response body on success.

    Raises
    ------
    RequestInterrupted
        When *cancel_event* is set before or during a request.
    RuntimeError
        On any non-retryable HTTP error (400-404), or when retries are
        exhausted for retryable errors (429, 5xx, network failures).
    """
    last_error: Exception | None = None
    attempts_total = max(0, int(max_retries)) + 1  # initial attempt + retries

    for attempt in range(1, attempts_total + 1):
        # Cooperative cancellation: never start a new attempt after interrupt.
        if cancel_event is not None and cancel_event.is_set():
            raise RequestInterrupted(
                f"{provider_name} request cancelled before attempt {attempt}"
            )
        try:
            if cancel_event is None:
                with urllib.request.urlopen(http_request, timeout=timeout) as resp:
                    return resp.read()
            return _urlopen_abortable(
                http_request, timeout, cancel_event, provider_name
            )
        except urllib.error.HTTPError as exc:
            detail = exc.read().decode("utf-8", errors="replace")
            if exc.code in _NON_RETRYABLE_HTTP_CODES:
                raise RuntimeError(
                    f"{provider_name} API error {exc.code}: {detail}"
                ) from exc
            if exc.code in _RETRYABLE_HTTP_CODES:
                last_error = exc
                if attempt >= attempts_total:
                    raise RuntimeError(
                        f"{provider_name} API error {exc.code} after "
                        f"{attempt} attempt(s): {detail}"
                    ) from exc
                # Try to extract a server-suggested retry delay.
                try:
                    body_json: dict[str, Any] = json.loads(detail)
                except (json.JSONDecodeError, TypeError):
                    body_json = {}
                retry_after_parsed = _parse_retry_after(exc.headers, body_json)
                delay = _backoff_delay(attempt, retry_after_parsed)
                _logger.warning(
                    "%s API returned %d (attempt %d/%d), retrying in %.1f s",
                    provider_name,
                    exc.code,
                    attempt,
                    attempts_total,
                    delay,
                )
                _interruptible_sleep(delay, cancel_event)
                continue
            # Unknown status code -> treat as non-retryable.
            raise RuntimeError(
                f"{provider_name} API error {exc.code}: {detail}"
            ) from exc
        except (urllib.error.URLError, TimeoutError, socket.timeout) as exc:
            last_error = exc
            if attempt >= attempts_total:
                raise RuntimeError(
                    f"{provider_name} API request failed after "
                    f"{attempt} attempt(s): {exc}"
                ) from exc
            delay = _backoff_delay(attempt)
            _logger.warning(
                "%s API network error (attempt %d/%d), retrying in %.1f s: %s",
                provider_name,
                attempt,
                attempts_total,
                delay,
                exc,
            )
            _interruptible_sleep(delay, cancel_event)

    # Defensive -- should never reach here.
    raise RuntimeError(f"{provider_name} API request failed: {last_error}")
