from __future__ import annotations

from datetime import datetime
from typing import Any, Literal

from pydantic import BaseModel, Field


class UserPayload(BaseModel):
    id: int
    username: str = ""
    role: str = "USER"
    orgTags: str = ""
    primaryOrg: str = ""


class ChatMessagePayload(BaseModel):
    role: str
    content: str
    timestamp: datetime | None = None


class ContextSnippetPayload(BaseModel):
    id: str = ""
    sourceType: str = ""
    label: str = ""
    text: str = ""
    score: float = 0.0
    timestamp: datetime | None = None


class FileProcessingTaskPayload(BaseModel):
    file_md5: str
    object_url: str = ""
    file_name: str
    user_id: int
    org_tag: str = ""
    is_public: bool = False
    stage: str
    task_chunk_id: int = 0
    chunk_start: int = 0
    total_chunks: int = 0
    parsed_object: str = ""
    last_error: str = ""


class ChatStreamRequest(BaseModel):
    query: str = Field(min_length=1)
    user: UserPayload


class SessionResponse(BaseModel):
    conversationId: str
    history: list[ChatMessagePayload] = Field(default_factory=list)


class RetrievePlanPayload(BaseModel):
    intent: str = "single_hop"
    rewritten_query: str = ""
    sub_queries: list[str] = Field(default_factory=list)
    retrieval_mode: str = "hybrid"
    enable_rerank: bool | None = None
    need_history: bool = False
    skip_retrieval: bool = False
    reason: str = ""


class RetrieveRequestPayload(BaseModel):
    user: UserPayload
    query: str
    conversationId: str = ""
    history: list[ChatMessagePayload] = Field(default_factory=list)
    plan: RetrievePlanPayload


class RetrieveResponse(BaseModel):
    conversationId: str
    history: list[ChatMessagePayload] = Field(default_factory=list)
    sensoryHistory: list[ChatMessagePayload] = Field(default_factory=list)
    memoryPrelude: str = ""
    knowledgeItems: list[ContextSnippetPayload] = Field(default_factory=list)
    memoryItems: list[ContextSnippetPayload] = Field(default_factory=list)
    contextItems: list[ContextSnippetPayload] = Field(default_factory=list)
    contextText: str = ""
    systemMessage: str = ""


class PromptContextRequestPayload(BaseModel):
    user: UserPayload
    conversationId: str = ""
    history: list[ChatMessagePayload] = Field(default_factory=list)
    plan: RetrievePlanPayload


class PromptContextResponse(BaseModel):
    conversationId: str
    history: list[ChatMessagePayload] = Field(default_factory=list)
    sensoryHistory: list[ChatMessagePayload] = Field(default_factory=list)
    memoryPrelude: str = ""
    promptRules: str = ""
    refStart: str = "<<REF>>"
    refEnd: str = "<<END>>"
    noResultText: str = "(no retrieval result in this turn)"
    knowledgeTopK: int = 8
    contextTopK: int = 6
    rrfK: int = 60


class SearchResultPayload(BaseModel):
    fileMd5: str = ""
    fileName: str = ""
    chunkId: int = 0
    textContent: str = ""
    score: float = 0.0
    userId: str = ""
    orgTag: str = ""
    isPublic: bool = False


class KnowledgeSearchRequestPayload(BaseModel):
    user: UserPayload
    query: str
    topK: int = 8
    disableRerank: bool = True
    mode: Literal["hybrid", "bm25", "vector"] = "hybrid"


class KnowledgeSearchResponse(BaseModel):
    results: list[SearchResultPayload] = Field(default_factory=list)


class MemorySearchRequestPayload(BaseModel):
    user: UserPayload
    query: str
    history: list[ChatMessagePayload] = Field(default_factory=list)
    plan: RetrievePlanPayload


class MemorySearchResponse(BaseModel):
    items: list[ContextSnippetPayload] = Field(default_factory=list)


class RerankContextRequestPayload(BaseModel):
    query: str
    topK: int = 6
    items: list[ContextSnippetPayload] = Field(default_factory=list)


class RerankContextResponse(BaseModel):
    items: list[ContextSnippetPayload] = Field(default_factory=list)


class ProfileUpdatePayload(BaseModel):
    slot_key: str = ""
    slot_value: str = ""
    confidence: float = 0.0


class MemorySummaryRequestPayload(BaseModel):
    history: list[ChatMessagePayload] = Field(default_factory=list)
    workingHistoryMessages: int = 12
    workingMaxFacts: int = 6


class MemorySummaryResponsePayload(BaseModel):
    summary: str = ""
    facts: list[str] = Field(default_factory=list)
    entities: list[str] = Field(default_factory=list)
    profile_updates: list[ProfileUpdatePayload] = Field(default_factory=list)


class MemoryWriteRequestPayload(BaseModel):
    question: str
    answer: str
    workingSummary: str = ""


class MemoryWriteResponsePayload(BaseModel):
    should_store: bool = False
    memory_type: str = "fact"
    summary: str = ""
    content: str = ""
    entities: list[str] = Field(default_factory=list)
    importance: float = 0.0
    profile_updates: list[ProfileUpdatePayload] = Field(default_factory=list)


class ParseRequestPayload(BaseModel):
    task: FileProcessingTaskPayload
    objectUrl: str


class ParseResponsePayload(BaseModel):
    parsedText: str


class ChunkRequestPayload(BaseModel):
    task: FileProcessingTaskPayload
    text: str
    chunkSize: int = 500
    chunkOverlap: int = 50


class ChunkResponsePayload(BaseModel):
    chunks: list[str] = Field(default_factory=list)


class EmbedRequestPayload(BaseModel):
    task: FileProcessingTaskPayload
    texts: list[str] = Field(default_factory=list)


class EmbedResponsePayload(BaseModel):
    vectors: list[list[float]] = Field(default_factory=list)


class IndexRequestPayload(BaseModel):
    task: FileProcessingTaskPayload
    indexName: str
    docs: list[dict[str, Any]] = Field(default_factory=list)


class IndexResponsePayload(BaseModel):
    indexedCount: int = 0


class PersistRequestPayload(BaseModel):
    user: UserPayload
    conversationId: str = ""
    history: list[ChatMessagePayload] = Field(default_factory=list)
    query: str
    answer: str


class PlannerDecision(BaseModel):
    intent: Literal["single_hop", "follow_up", "comparison", "troubleshooting", "chitchat"] = "single_hop"
    rewritten_query: str = ""
    sub_queries: list[str] = Field(default_factory=list)
    retrieval_mode: Literal["hybrid", "bm25", "vector"] = "hybrid"
    enable_rerank: bool | None = None
    need_history: bool = False
    skip_retrieval: bool = False
    reason: str = ""


class StreamEvent(BaseModel):
    type: Literal["chunk", "trace", "error", "done"] = "chunk"
    chunk: str = ""
    trace: str = ""
    error: str = ""
    done: bool = False
    metadata: dict[str, Any] = Field(default_factory=dict)
