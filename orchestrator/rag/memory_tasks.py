from __future__ import annotations

import json
import re
import time

from langchain_core.messages import HumanMessage, SystemMessage
from langchain_openai import ChatOpenAI

from .config import ModelSettings, Settings
from .models import (
    ChatMessagePayload,
    MemorySummaryRequestPayload,
    MemorySummaryResponsePayload,
    MemoryWriteRequestPayload,
    MemoryWriteResponsePayload,
    ProfileUpdatePayload,
)
from .trace import elapsed_ms, log_request

JSON_OBJECT_PATTERN = re.compile(r"\{[\s\S]*\}")


class MemoryTaskService:
    def __init__(self, settings: Settings) -> None:
        self._settings = settings
        self._model = _build_model(settings.planner, request_timeout=settings.request_timeout_seconds)

    async def summarize(self, payload: MemorySummaryRequestPayload) -> MemorySummaryResponsePayload:
        start = time.perf_counter()
        history = payload.history[-max(1, payload.workingHistoryMessages) :]
        raw = await self._invoke(
            system_prompt=(
                "You summarize conversation memory for a RAG assistant. "
                "Return strict JSON with keys: summary, facts, entities, profile_updates. "
                "summary must be one concise paragraph. "
                "facts must be an array of short factual bullets. "
                "entities must be an array of important names, projects, systems, files, or technologies. "
                "profile_updates must be an array of objects with slot_key, slot_value, confidence. "
                "Only extract stable user traits or durable context into profile_updates."
            ),
            user_prompt=_render_conversation(history),
        )
        parsed = _parse_json_response(raw, MemorySummaryResponsePayload)
        parsed.facts = _normalize_string_list(parsed.facts)[: max(1, payload.workingMaxFacts)]
        parsed.entities = _normalize_string_list(parsed.entities)
        parsed.profile_updates = _normalize_profile_updates(parsed.profile_updates)
        log_request(
            "memory_summarize",
            latency_ms=elapsed_ms(start),
            history=len(history),
            facts=len(parsed.facts),
        )
        return parsed

    async def extract(self, payload: MemoryWriteRequestPayload) -> MemoryWriteResponsePayload:
        start = time.perf_counter()
        prompt_parts = [
            "User question:\n" + payload.question.strip(),
            "Assistant answer:\n" + payload.answer.strip(),
        ]
        if payload.workingSummary.strip():
            prompt_parts.append("Current working memory:\n" + payload.workingSummary.strip())

        raw = await self._invoke(
            system_prompt=(
                "You decide whether a dialogue turn should be stored as long-term memory. "
                "Return strict JSON with keys: should_store, memory_type, summary, content, entities, importance, profile_updates. "
                "Store only durable preferences, project context, constraints, decisions, architecture facts, or reusable references. "
                "importance must be a float between 0 and 1. "
                "profile_updates follows the same schema: slot_key, slot_value, confidence."
            ),
            user_prompt="\n\n".join(prompt_parts),
        )
        parsed = _parse_json_response(raw, MemoryWriteResponsePayload)
        parsed.memory_type = _normalize_memory_type(parsed.memory_type)
        parsed.entities = _normalize_string_list(parsed.entities)
        parsed.importance = max(0.0, min(1.0, parsed.importance))
        parsed.profile_updates = _normalize_profile_updates(parsed.profile_updates)
        log_request(
            "memory_extract",
            latency_ms=elapsed_ms(start),
            should_store=parsed.should_store,
            importance=parsed.importance,
        )
        return parsed

    async def _invoke(self, *, system_prompt: str, user_prompt: str) -> str:
        result = await self._model.ainvoke(
            [
                SystemMessage(content=system_prompt),
                HumanMessage(content=user_prompt),
            ]
        )
        content = result.content
        if isinstance(content, str):
            return content
        if isinstance(content, list):
            parts: list[str] = []
            for item in content:
                if isinstance(item, str):
                    parts.append(item)
                elif isinstance(item, dict):
                    text = item.get("text")
                    if isinstance(text, str):
                        parts.append(text)
            return "".join(parts)
        return str(content)


def _build_model(model_settings: ModelSettings, request_timeout: int) -> ChatOpenAI:
    return ChatOpenAI(
        model=model_settings.model,
        api_key=model_settings.api_key or "not-needed",
        base_url=model_settings.base_url,
        temperature=model_settings.temperature,
        top_p=model_settings.top_p if model_settings.top_p > 0 else None,
        max_tokens=model_settings.max_tokens,
        timeout=request_timeout,
        max_retries=2,
    )


def _render_conversation(history: list[ChatMessagePayload]) -> str:
    if not history:
        return "(empty)"
    return "\n".join(f"{item.role.title()}: {item.content.strip()}" for item in history)


def _parse_json_response(raw: str, model_cls):
    trimmed = (raw or "").strip()
    if not trimmed:
        raise ValueError("empty model response")
    try:
        return model_cls.model_validate_json(trimmed)
    except Exception:
        match = JSON_OBJECT_PATTERN.search(trimmed)
        if not match:
            raise
        payload = json.loads(match.group(0))
        if isinstance(payload, dict):
            payload = _sanitize_null_fields(payload)
        return model_cls.model_validate(payload)


def _sanitize_null_fields(payload: dict[str, object]) -> dict[str, object]:
    normalized = dict(payload)
    defaults: dict[str, object] = {
        "summary": "",
        "facts": [],
        "entities": [],
        "profile_updates": [],
        "memory_type": "fact",
        "content": "",
        "importance": 0.0,
        "should_store": False,
    }
    for key, default in defaults.items():
        if normalized.get(key) is None:
            normalized[key] = default
    return normalized


def _normalize_string_list(items: list[str]) -> list[str]:
    result: list[str] = []
    seen: set[str] = set()
    for item in items:
        value = item.strip()
        if not value or value in seen:
            continue
        seen.add(value)
        result.append(value)
    return result


def _normalize_profile_updates(items: list[ProfileUpdatePayload]) -> list[ProfileUpdatePayload]:
    result: list[ProfileUpdatePayload] = []
    for item in items:
        key = _normalize_profile_slot_key(item.slot_key)
        value = item.slot_value.strip()
        if not key or not value:
            continue
        confidence = max(0.1, min(0.99, item.confidence))
        result.append(ProfileUpdatePayload(slot_key=key, slot_value=value[:255], confidence=confidence))
    return result


def _normalize_profile_slot_key(key: str) -> str:
    value = key.strip().lower()
    mapping = {
        "language": "primary_language",
        "tech_stack": "primary_language",
        "coding_language": "primary_language",
        "preferred_language": "primary_language",
        "primary_language": "primary_language",
        "answer_style": "preferred_answer_style",
        "response_style": "preferred_answer_style",
        "preferred_answer_style": "preferred_answer_style",
        "project": "current_project",
        "current_project": "current_project",
        "role": "role",
        "deployment_env": "deployment_env",
        "environment": "deployment_env",
    }
    return mapping.get(value, "")


def _normalize_memory_type(memory_type: str) -> str:
    value = memory_type.strip().lower()
    if value in {"preference", "project", "decision", "constraint", "fact"}:
        return value
    return "fact"
