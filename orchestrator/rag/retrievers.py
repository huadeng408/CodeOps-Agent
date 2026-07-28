from __future__ import annotations

import asyncio
from typing import Any

from langchain_core.callbacks.manager import (
    AsyncCallbackManagerForRetrieverRun,
    CallbackManagerForRetrieverRun,
)
from langchain_core.documents import Document
from langchain_core.retrievers import BaseRetriever
from pydantic import ConfigDict

from .backend import GoBackendClient
from .models import (
    ContextSnippetPayload,
    KnowledgeSearchRequestPayload,
    MemorySearchRequestPayload,
    RetrievePlanPayload,
    SearchResultPayload,
    UserPayload,
)


class GoKnowledgeRetriever(BaseRetriever):
    backend: GoBackendClient
    user: UserPayload
    mode: str = "hybrid"
    top_k: int = 8
    disable_rerank: bool = True

    model_config = ConfigDict(arbitrary_types_allowed=True)

    def _get_relevant_documents(
        self,
        query: str,
        *,
        run_manager: CallbackManagerForRetrieverRun,
    ) -> list[Document]:
        raise NotImplementedError("Use the async retriever API")

    async def _aget_relevant_documents(
        self,
        query: str,
        *,
        run_manager: AsyncCallbackManagerForRetrieverRun,
    ) -> list[Document]:
        response = await self.backend.search_knowledge(
            KnowledgeSearchRequestPayload(
                user=self.user,
                query=query,
                topK=self.top_k,
                disableRerank=self.disable_rerank,
                mode=self.mode,
            )
        )
        return [search_result_to_document(item, self.mode) for item in response.results]


class GoMemoryRetriever(BaseRetriever):
    backend: GoBackendClient
    user: UserPayload
    history: list[Any]
    plan: RetrievePlanPayload

    model_config = ConfigDict(arbitrary_types_allowed=True)

    def _get_relevant_documents(
        self,
        query: str,
        *,
        run_manager: CallbackManagerForRetrieverRun,
    ) -> list[Document]:
        raise NotImplementedError("Use the async retriever API")

    async def _aget_relevant_documents(
        self,
        query: str,
        *,
        run_manager: AsyncCallbackManagerForRetrieverRun,
    ) -> list[Document]:
        response = await self.backend.search_memory(
            MemorySearchRequestPayload(
                user=self.user,
                query=query,
                history=self.history,
                plan=self.plan,
            )
        )
        return [context_snippet_to_document(item) for item in response.items]


class HybridKnowledgeRetriever(BaseRetriever):
    backend: GoBackendClient
    user: UserPayload
    top_k: int = 8
    rrf_k: int = 60

    model_config = ConfigDict(arbitrary_types_allowed=True)

    def _get_relevant_documents(
        self,
        query: str,
        *,
        run_manager: CallbackManagerForRetrieverRun,
    ) -> list[Document]:
        raise NotImplementedError("Use the async retriever API")

    async def _aget_relevant_documents(
        self,
        query: str,
        *,
        run_manager: AsyncCallbackManagerForRetrieverRun,
    ) -> list[Document]:
        bm25 = GoKnowledgeRetriever(
            backend=self.backend,
            user=self.user,
            mode="bm25",
            top_k=self.top_k,
            disable_rerank=True,
        )
        vector = GoKnowledgeRetriever(
            backend=self.backend,
            user=self.user,
            mode="vector",
            top_k=self.top_k,
            disable_rerank=True,
        )

        results = await asyncio.gather(
            bm25.ainvoke(query),
            vector.ainvoke(query),
            return_exceptions=True,
        )

        doc_lists: list[list[Document]] = []
        for item in results:
            if isinstance(item, Exception):
                continue
            doc_lists.append(item)

        return rrf_fuse_documents(doc_lists, self.rrf_k, self.top_k)


def search_result_to_document(item: SearchResultPayload, mode: str) -> Document:
    key = f"{item.fileMd5}:{item.chunkId}" if item.fileMd5 else f"{item.fileName}:{item.chunkId}"
    metadata = {
        "id": key,
        "doc_key": key,
        "sourceType": "knowledge",
        "label": item.fileName or "knowledge",
        "score": item.score,
        "raw_score": item.score,
        "fileMd5": item.fileMd5,
        "fileName": item.fileName,
        "chunkId": item.chunkId,
        "retrievalMode": mode,
        "orgTag": item.orgTag,
        "isPublic": item.isPublic,
    }
    return Document(page_content=item.textContent, metadata=metadata)


def context_snippet_to_document(item: ContextSnippetPayload) -> Document:
    key = item.id or item.label or "context"
    metadata = {
        "id": key,
        "doc_key": f"{item.sourceType}:{key}",
        "sourceType": item.sourceType,
        "label": item.label or item.sourceType or "context",
        "score": item.score,
        "raw_score": item.score,
        "timestamp": item.timestamp.isoformat() if item.timestamp else "",
    }
    return Document(page_content=item.text, metadata=metadata)


def document_to_context_snippet(doc: Document) -> ContextSnippetPayload:
    metadata = dict(doc.metadata or {})
    timestamp = metadata.get("timestamp")
    if not timestamp:
        timestamp = None
    return ContextSnippetPayload(
        id=str(metadata.get("id", "")),
        sourceType=str(metadata.get("sourceType", "")),
        label=str(metadata.get("label", "")),
        text=doc.page_content,
        score=float(metadata.get("score", 0.0) or 0.0),
        timestamp=timestamp,
    )


def rrf_fuse_documents(doc_lists: list[list[Document]], rrf_k: int, top_k: int) -> list[Document]:
    fused: dict[str, dict[str, Any]] = {}
    for docs in doc_lists:
        for rank, doc in enumerate(docs):
            key = str(doc.metadata.get("doc_key") or doc.metadata.get("id") or doc.page_content[:80])
            entry = fused.setdefault(key, {"doc": doc, "score": 0.0})
            entry["score"] += 1.0 / float(rrf_k + rank + 1)
            existing_raw = float(entry["doc"].metadata.get("raw_score", 0.0) or 0.0)
            current_raw = float(doc.metadata.get("raw_score", 0.0) or 0.0)
            if current_raw > existing_raw:
                entry["doc"] = doc

    ranked: list[Document] = []
    for entry in fused.values():
        doc = entry["doc"]
        metadata = dict(doc.metadata or {})
        metadata["score"] = entry["score"]
        ranked.append(Document(page_content=doc.page_content, metadata=metadata))

    ranked.sort(
        key=lambda item: (
            -float(item.metadata.get("score", 0.0) or 0.0),
            str(item.metadata.get("doc_key", "")),
        )
    )
    if top_k > 0:
        ranked = ranked[:top_k]
    return ranked
