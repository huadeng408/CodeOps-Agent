from __future__ import annotations

import asyncio
import json
import re
import time
from typing import Any

from langchain_core.documents import Document
from langchain_core.messages import AIMessage, BaseMessage, HumanMessage, SystemMessage
from langchain_core.prompts import ChatPromptTemplate, MessagesPlaceholder
from langchain_openai import ChatOpenAI
from langgraph.config import get_stream_writer
from langgraph.graph import END, START, StateGraph
from typing_extensions import TypedDict

from .backend import GoBackendClient
from .config import ModelSettings, Settings
from .models import (
    ChatMessagePayload,
    PersistRequestPayload,
    PlannerDecision,
    PromptContextRequestPayload,
    RetrievePlanPayload,
    RerankContextRequestPayload,
    StreamEvent,
    UserPayload,
)
from .retrievers import (
    GoKnowledgeRetriever,
    GoMemoryRetriever,
    HybridKnowledgeRetriever,
    context_snippet_to_document,
    document_to_context_snippet,
    rrf_fuse_documents,
)
from .trace import elapsed_ms, log_request

PLANNER_PROMPT = """你是企业知识库问答系统的查询规划器。
请基于当前问题和最近历史，输出一个 JSON 对象，字段包括：
- intent: single_hop | follow_up | comparison | troubleshooting | chitchat
- rewritten_query: 更适合检索的查询语句
- sub_queries: 字符串数组，可为空
- retrieval_mode: hybrid | bm25 | vector
- enable_rerank: true | false | null
- need_history: true | false
- skip_retrieval: true | false
- reason: 简短说明

只输出 JSON，不要输出解释。"""

JSON_OBJECT_PATTERN = re.compile(r"\{[\s\S]*\}")


class RAGState(TypedDict, total=False):
    query: str
    user: dict[str, Any]
    stream_callback: Any
    conversation_id: str
    history: list[dict[str, Any]]
    intent: str
    rewritten_query: str
    sub_queries: list[str]
    retrieval_mode: str
    enable_rerank: bool | None
    need_history: bool
    skip_retrieval: bool
    reason: str
    sensory_history: list[dict[str, Any]]
    memory_prelude: str
    prompt_rules: str
    ref_start: str
    ref_end: str
    no_result_text: str
    knowledge_top_k: int
    context_top_k: int
    rrf_k: int
    knowledge_docs: list[Document]
    memory_docs: list[Document]
    fused_docs: list[Document]
    reranked_docs: list[Document]
    context_items: list[dict[str, Any]]
    context_text: str
    system_message: str
    prompt_messages: list[BaseMessage]
    answer: str
    citations: list[str]


def build_graph(settings: Settings, backend: GoBackendClient):
    planner_llm = _build_model(settings.planner, request_timeout=settings.request_timeout_seconds)
    answer_llm = _build_model(settings.llm, request_timeout=settings.request_timeout_seconds)

    def get_optional_stream_writer():
        try:
            return get_stream_writer()
        except RuntimeError:
            return None

    def emit_trace(step: str, **metadata: Any) -> None:
        log_request("graph_step", step=step, **metadata)
        if settings.debug_traces:
            writer = get_optional_stream_writer()
            if writer is not None:
                writer(StreamEvent(type="trace", trace=step, metadata=metadata).model_dump(mode="json"))

    async def load_history(state: RAGState) -> RAGState:
        start = time.perf_counter()
        session = await backend.load_session(int(state["user"]["id"]))
        emit_trace("load_history", latency_ms=elapsed_ms(start), history=len(session.history))
        return {
            "conversation_id": session.conversationId,
            "history": [item.model_dump(mode="json") for item in session.history],
        }

    async def classify_intent(state: RAGState) -> RAGState:
        start = time.perf_counter()
        query = state["query"]
        history = [ChatMessagePayload.model_validate(item) for item in state.get("history", [])]
        fallback = _heuristic_decision(query, history)
        if _should_use_heuristic_planner(query, history):
            emit_trace("classify_intent", latency_ms=elapsed_ms(start), intent=fallback.intent, mode="heuristic")
            return fallback.model_dump(exclude_none=False)

        prompt = f"最近历史：\n{_format_history_lines(history)}\n\n当前问题：\n{query}\n"
        try:
            result = await planner_llm.ainvoke(
                [
                    SystemMessage(content=PLANNER_PROMPT),
                    HumanMessage(content=prompt),
                ]
            )
            decision = _parse_planner_decision(result.content, query, history, fallback)
        except Exception:
            decision = fallback
        emit_trace("classify_intent", latency_ms=elapsed_ms(start), intent=decision.intent)

        return decision.model_dump(exclude_none=False)

    async def rewrite_query(state: RAGState) -> RAGState:
        start = time.perf_counter()
        rewritten = _sanitize_query(state.get("rewritten_query") or state["query"])
        sub_queries = [item.strip() for item in state.get("sub_queries", []) if item and item.strip()]
        emit_trace("rewrite_query", latency_ms=elapsed_ms(start), sub_queries=len(sub_queries))
        return {
            "rewritten_query": rewritten or state["query"],
            "sub_queries": sub_queries[:2],
        }

    async def prepare_prompt_context(state: RAGState) -> RAGState:
        start = time.perf_counter()
        response = await backend.prepare_prompt_context(
            PromptContextRequestPayload(
                user=UserPayload.model_validate(state["user"]),
                conversationId=state.get("conversation_id", ""),
                history=[ChatMessagePayload.model_validate(item) for item in state.get("history", [])],
                plan=RetrievePlanPayload(
                    intent=state.get("intent", "single_hop"),
                    rewritten_query=state.get("rewritten_query", state["query"]),
                    sub_queries=state.get("sub_queries", []),
                    retrieval_mode=state.get("retrieval_mode", "hybrid"),
                    enable_rerank=state.get("enable_rerank"),
                    need_history=state.get("need_history", False),
                    skip_retrieval=state.get("skip_retrieval", False),
                    reason=state.get("reason", ""),
                ),
            )
        )
        emit_trace("prepare_prompt_context", latency_ms=elapsed_ms(start), history=len(response.history))
        return {
            "conversation_id": response.conversationId,
            "history": [item.model_dump(mode="json") for item in response.history],
            "sensory_history": [item.model_dump(mode="json") for item in response.sensoryHistory],
            "memory_prelude": response.memoryPrelude,
            "prompt_rules": response.promptRules,
            "ref_start": response.refStart,
            "ref_end": response.refEnd,
            "no_result_text": response.noResultText,
            "knowledge_top_k": response.knowledgeTopK or settings.knowledge_top_k,
            "context_top_k": response.contextTopK or settings.context_top_k,
            "rrf_k": response.rrfK or settings.rrf_k,
        }

    async def retrieve_knowledge(state: RAGState) -> RAGState:
        start = time.perf_counter()
        if state.get("skip_retrieval"):
            emit_trace("retrieve_knowledge", latency_ms=elapsed_ms(start), skipped=True)
            return {"knowledge_docs": []}

        user = UserPayload.model_validate(state["user"])
        top_k = max(1, int(state.get("knowledge_top_k", settings.knowledge_top_k)))
        queries = [state.get("rewritten_query", state["query"]).strip() or state["query"]]
        queries.extend(item for item in state.get("sub_queries", []) if item.strip())

        mode = state.get("retrieval_mode", "hybrid")
        if mode == "hybrid":
            retriever = HybridKnowledgeRetriever(
                backend=backend,
                user=user,
                top_k=top_k,
                rrf_k=max(1, int(state.get("rrf_k", settings.rrf_k))),
            )
        else:
            retriever = GoKnowledgeRetriever(
                backend=backend,
                user=user,
                mode=mode,
                top_k=top_k,
                disable_rerank=True,
            )

        results = await asyncio.gather(*(retriever.ainvoke(item) for item in queries), return_exceptions=True)
        doc_groups: list[list[Document]] = []
        for item in results:
            if isinstance(item, Exception):
                continue
            doc_groups.append(item)

        merged = _merge_document_groups(doc_groups, top_k)
        emit_trace("retrieve_knowledge", latency_ms=elapsed_ms(start), docs=len(merged), mode=mode)
        return {"knowledge_docs": merged}

    async def retrieve_memory(state: RAGState) -> RAGState:
        start = time.perf_counter()
        if state.get("skip_retrieval"):
            emit_trace("retrieve_memory", latency_ms=elapsed_ms(start), skipped=True)
            return {"memory_docs": []}

        retriever = GoMemoryRetriever(
            backend=backend,
            user=UserPayload.model_validate(state["user"]),
            history=[ChatMessagePayload.model_validate(item) for item in state.get("history", [])],
            plan=RetrievePlanPayload(
                intent=state.get("intent", "single_hop"),
                rewritten_query=state.get("rewritten_query", state["query"]),
                sub_queries=state.get("sub_queries", []),
                retrieval_mode=state.get("retrieval_mode", "hybrid"),
                enable_rerank=state.get("enable_rerank"),
                need_history=state.get("need_history", False),
                skip_retrieval=state.get("skip_retrieval", False),
                reason=state.get("reason", ""),
            ),
        )
        try:
            docs = await retriever.ainvoke(state["query"])
        except Exception:
            docs = []
        emit_trace("retrieve_memory", latency_ms=elapsed_ms(start), docs=len(docs))
        return {"memory_docs": docs}

    async def fuse_context(state: RAGState) -> RAGState:
        start = time.perf_counter()
        knowledge_docs = list(state.get("knowledge_docs", []))
        memory_docs = list(state.get("memory_docs", []))
        top_k = max(1, int(state.get("context_top_k", settings.context_top_k))) * 2

        if not knowledge_docs and not memory_docs:
            emit_trace("fuse_context", latency_ms=elapsed_ms(start), docs=0)
            return {
                "fused_docs": [],
                "context_items": [],
                "citations": [],
            }

        fused_docs = rrf_fuse_documents(
            [knowledge_docs, memory_docs],
            max(1, int(state.get("rrf_k", settings.rrf_k))),
            top_k,
        )
        emit_trace("fuse_context", latency_ms=elapsed_ms(start), docs=len(fused_docs))
        return {
            "fused_docs": fused_docs,
            "context_items": [document_to_context_snippet(doc).model_dump(mode="json") for doc in fused_docs],
            "citations": _build_citations(fused_docs),
        }

    async def rerank_context(state: RAGState) -> RAGState:
        start = time.perf_counter()
        fused_docs = list(state.get("fused_docs", []))
        if not fused_docs:
            emit_trace("rerank_context", latency_ms=elapsed_ms(start), docs=0)
            return {
                "reranked_docs": [],
                "context_items": [],
                "context_text": "",
                "citations": [],
            }

        response = await backend.rerank_context(
            RerankContextRequestPayload(
                query=state["query"],
                topK=max(1, int(state.get("context_top_k", settings.context_top_k))),
                items=[document_to_context_snippet(doc) for doc in fused_docs],
            )
        )
        reranked_docs = [context_snippet_to_document(item) for item in response.items]
        context_text = _build_context_text(reranked_docs)
        emit_trace("rerank_context", latency_ms=elapsed_ms(start), docs=len(reranked_docs))
        return {
            "reranked_docs": reranked_docs,
            "context_items": [item.model_dump(mode="json") for item in response.items],
            "context_text": context_text,
            "citations": _build_citations(reranked_docs),
        }

    async def build_messages(state: RAGState) -> RAGState:
        start = time.perf_counter()
        system_message = _build_system_message(
            rules=state.get("prompt_rules", ""),
            memory_prelude=state.get("memory_prelude", ""),
            context_text=state.get("context_text", ""),
            ref_start=state.get("ref_start", "<<REF>>"),
            ref_end=state.get("ref_end", "<<END>>"),
            no_result_text=state.get("no_result_text", "(no retrieval result in this turn)"),
        )

        prompt = ChatPromptTemplate.from_messages(
            [
                ("system", "{system_message}"),
                MessagesPlaceholder("history"),
                ("human", "{query}"),
            ]
        )
        history_messages = [
            _to_langchain_message(ChatMessagePayload.model_validate(item))
            for item in state.get("sensory_history", [])
        ]
        prompt_messages = prompt.format_messages(
            system_message=system_message,
            history=history_messages,
            query=state["query"],
        )
        emit_trace("build_messages", latency_ms=elapsed_ms(start), history=len(history_messages))
        return {
            "system_message": system_message,
            "prompt_messages": prompt_messages,
        }

    async def generate_answer(state: RAGState) -> RAGState:
        start = time.perf_counter()
        writer = get_optional_stream_writer()
        stream_callback = state.get("stream_callback")
        parts: list[str] = []
        async for chunk in answer_llm.astream(state["prompt_messages"]):
            text = _extract_chunk_text(chunk)
            if not text:
                continue
            parts.append(text)
            if writer is not None:
                writer(StreamEvent(type="chunk", chunk=text).model_dump(mode="json"))
            if callable(stream_callback):
                try:
                    stream_callback(text)
                except Exception:
                    pass
        answer = "".join(parts).strip()
        emit_trace("generate_answer", latency_ms=elapsed_ms(start), chars=len(answer))
        return {"answer": answer}

    async def persist_memory(state: RAGState) -> RAGState:
        start = time.perf_counter()
        if not state.get("answer"):
            return {}
        try:
            await backend.persist_turn(
                PersistRequestPayload(
                    user=UserPayload.model_validate(state["user"]),
                    conversationId=state.get("conversation_id", ""),
                    history=[ChatMessagePayload.model_validate(item) for item in state.get("history", [])],
                    query=state["query"],
                    answer=state["answer"],
                )
            )
        except Exception:
            return {}
        emit_trace("persist_memory", latency_ms=elapsed_ms(start))
        return {}

    workflow = StateGraph(RAGState)
    workflow.add_node("load_history", load_history)
    workflow.add_node("classify_intent", classify_intent)
    workflow.add_node("rewrite_query", rewrite_query)
    workflow.add_node("prepare_prompt_context", prepare_prompt_context)
    workflow.add_node("retrieve_knowledge", retrieve_knowledge)
    workflow.add_node("retrieve_memory", retrieve_memory)
    workflow.add_node("fuse_context", fuse_context)
    workflow.add_node("rerank_context", rerank_context)
    workflow.add_node("build_messages", build_messages)
    workflow.add_node("generate_answer", generate_answer)
    workflow.add_node("persist_memory", persist_memory)

    workflow.add_edge(START, "load_history")
    workflow.add_edge("load_history", "classify_intent")
    workflow.add_edge("classify_intent", "rewrite_query")
    workflow.add_edge("rewrite_query", "prepare_prompt_context")
    workflow.add_edge("prepare_prompt_context", "retrieve_knowledge")
    workflow.add_edge("retrieve_knowledge", "retrieve_memory")
    workflow.add_edge("retrieve_memory", "fuse_context")
    workflow.add_edge("fuse_context", "rerank_context")
    workflow.add_edge("rerank_context", "build_messages")
    workflow.add_edge("build_messages", "generate_answer")
    workflow.add_edge("generate_answer", "persist_memory")
    workflow.add_edge("persist_memory", END)
    return workflow.compile()


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


def _format_history_lines(history: list[ChatMessagePayload]) -> str:
    if not history:
        return "(empty)"
    return "\n".join(f"- {item.role}: {item.content.strip()}" for item in history[-6:])


def _parse_planner_decision(
    raw_text: str,
    query: str,
    history: list[ChatMessagePayload],
    fallback: PlannerDecision,
) -> PlannerDecision:
    match = JSON_OBJECT_PATTERN.search(raw_text or "")
    if not match:
        return fallback
    try:
        payload = json.loads(match.group(0))
        decision = PlannerDecision.model_validate(payload)
    except Exception:
        return fallback

    if not decision.rewritten_query.strip():
        decision.rewritten_query = _sanitize_query(query)
    if decision.intent == "follow_up" and not decision.need_history:
        decision.need_history = True
    if decision.intent == "chitchat" and not decision.skip_retrieval:
        decision.skip_retrieval = True
    if decision.intent == "troubleshooting" and decision.retrieval_mode == "hybrid":
        decision.retrieval_mode = "bm25"
    if not history and decision.need_history:
        decision.need_history = False
    return decision


def _heuristic_decision(query: str, history: list[ChatMessagePayload]) -> PlannerDecision:
    cleaned = query.strip()
    lower = cleaned.lower()
    intent = "single_hop"
    retrieval_mode = "hybrid"
    need_history = False
    skip_retrieval = False
    enable_rerank: bool | None = None

    if any(token in lower for token in ("hi", "hello", "你好", "你是谁")):
        intent = "chitchat"
        skip_retrieval = True
    elif any(token in cleaned for token in ("继续", "刚才", "上一个", "前面", "这个问题")) and history:
        intent = "follow_up"
        need_history = True
    elif any(token in cleaned for token in ("对比", "比较", "区别", "优缺点")):
        intent = "comparison"
        enable_rerank = True
    elif any(token in cleaned for token in ("报错", "异常", "失败", "排查", "问题")):
        intent = "troubleshooting"
        retrieval_mode = "bm25"

    return PlannerDecision(
        intent=intent,  # type: ignore[arg-type]
        rewritten_query=_sanitize_query(cleaned),
        sub_queries=[],
        retrieval_mode=retrieval_mode,  # type: ignore[arg-type]
        enable_rerank=enable_rerank,
        need_history=need_history,
        skip_retrieval=skip_retrieval,
        reason="heuristic fallback",
    )


def _should_use_heuristic_planner(query: str, history: list[ChatMessagePayload]) -> bool:
    cleaned = query.strip()
    if not cleaned:
        return True

    if len(history) <= 2 and len(cleaned) <= 80:
        follow_up_tokens = ("继续", "刚才", "上一个", "前面", "这个问题", "它", "那这个")
        comparison_tokens = ("对比", "比较", "区别", "优缺点")
        troubleshooting_tokens = ("报错", "异常", "失败", "排查", "问题")
        if not any(token in cleaned for token in follow_up_tokens + comparison_tokens + troubleshooting_tokens):
            return True

    return False


def _sanitize_query(query: str) -> str:
    rewritten = query.strip()
    for prefix in ("继续追问：", "继续追问:", "请直接回答", "请回答", "帮我看看"):
        rewritten = rewritten.replace(prefix, "").strip()
    return rewritten or query.strip()


def _merge_document_groups(groups: list[list[Document]], top_k: int) -> list[Document]:
    merged: dict[str, Document] = {}
    for docs in groups:
        for doc in docs:
            key = str(doc.metadata.get("doc_key") or doc.metadata.get("id") or doc.page_content[:80])
            current_score = float(doc.metadata.get("score", 0.0) or 0.0)
            existing = merged.get(key)
            if existing is None or current_score > float(existing.metadata.get("score", 0.0) or 0.0):
                merged[key] = doc

    ranked = list(merged.values())
    ranked.sort(
        key=lambda item: (
            -float(item.metadata.get("score", 0.0) or 0.0),
            str(item.metadata.get("doc_key", "")),
        )
    )
    if top_k > 0:
        ranked = ranked[:top_k]
    return ranked


def _build_context_text(docs: list[Document]) -> str:
    if not docs:
        return ""
    parts: list[str] = []
    for idx, doc in enumerate(docs, start=1):
        metadata = doc.metadata or {}
        label = str(metadata.get("label") or metadata.get("sourceType") or "context")
        snippet = doc.page_content
        if len(snippet) > 800:
            snippet = snippet[:800] + "..."
        parts.append(f"[{idx}] ({label}) {snippet}")
    return "\n".join(parts)


def _build_system_message(
    *,
    rules: str,
    memory_prelude: str,
    context_text: str,
    ref_start: str,
    ref_end: str,
    no_result_text: str,
) -> str:
    parts: list[str] = []
    if rules.strip():
        parts.append(rules.strip())
    if memory_prelude.strip():
        parts.append(memory_prelude.strip())

    reference_body = context_text.strip() or no_result_text.strip()
    parts.append(f"{ref_start}\n{reference_body}\n{ref_end}")
    return "\n\n".join(parts)


def _build_citations(docs: list[Document]) -> list[str]:
    citations: list[str] = []
    seen: set[str] = set()
    for doc in docs:
        label = str(doc.metadata.get("label") or doc.metadata.get("id") or "").strip()
        if not label or label in seen:
            continue
        seen.add(label)
        citations.append(label)
        if len(citations) >= 5:
            break
    return citations


def _to_langchain_message(message: ChatMessagePayload) -> BaseMessage:
    role = message.role.lower().strip()
    if role == "assistant":
        return AIMessage(content=message.content)
    if role == "system":
        return SystemMessage(content=message.content)
    return HumanMessage(content=message.content)


def _extract_chunk_text(chunk: Any) -> str:
    content = getattr(chunk, "content", "")
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        texts: list[str] = []
        for item in content:
            if isinstance(item, str):
                texts.append(item)
            elif isinstance(item, dict):
                text = item.get("text")
                if isinstance(text, str):
                    texts.append(text)
        return "".join(texts)
    return str(content) if content else ""
