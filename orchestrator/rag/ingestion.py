from __future__ import annotations

import asyncio
import json
import mimetypes
import re
import time
from collections import Counter
from functools import lru_cache

import httpx
from langchain_core.documents import Document
from langchain_openai import OpenAIEmbeddings
from langchain_text_splitters import MarkdownHeaderTextSplitter, RecursiveCharacterTextSplitter

from .config import Settings
from .models import (
    ChunkRequestPayload,
    ChunkResponsePayload,
    EmbedRequestPayload,
    EmbedResponsePayload,
    IndexRequestPayload,
    IndexResponsePayload,
    ParseRequestPayload,
    ParseResponsePayload,
)
from .trace import current_trace_id, elapsed_ms, log_request

TOKEN_CHUNK_SIZE = 500
TOKEN_CHUNK_OVERLAP = 50


class IngestionService:
    def __init__(self, settings: Settings) -> None:
        self._settings = settings
        self._http = httpx.AsyncClient(timeout=settings.request_timeout_seconds, trust_env=False)
        embedding_kwargs: dict[str, object] = {
            "model": settings.embedding.model,
            "api_key": settings.embedding.api_key or "not-needed",
            "base_url": settings.embedding.base_url,
            "request_timeout": settings.request_timeout_seconds,
            "max_retries": 2,
        }
        if settings.embedding_dimensions > 0:
            embedding_kwargs["dimensions"] = settings.embedding_dimensions
        self._embeddings = OpenAIEmbeddings(**embedding_kwargs)
        fallback_kwargs = dict(embedding_kwargs)
        fallback_kwargs.pop("dimensions", None)
        self._embeddings_without_dimensions = OpenAIEmbeddings(**fallback_kwargs)
        _warm_splitter_cache()

    async def close(self) -> None:
        await self._http.aclose()

    async def parse(self, payload: ParseRequestPayload) -> ParseResponsePayload:
        start = time.perf_counter()
        source_resp = await self._http.get(payload.objectUrl)
        source_resp.raise_for_status()

        tika_resp = await self._http.put(
            f"{self._settings.tika_url.rstrip('/')}/tika",
            content=source_resp.content,
            headers={
                "Accept": "text/plain",
                "Content-Type": _detect_mime_type(payload.task.file_name),
            },
        )
        tika_resp.raise_for_status()
        parsed_text = _clean_parsed_text(tika_resp.text, payload.task.file_name)
        log_request(
            "ingestion_parse",
            latency_ms=elapsed_ms(start),
            file_md5=payload.task.file_md5,
            file_type=_detect_file_type(payload.task.file_name),
        )
        return ParseResponsePayload(parsedText=parsed_text)

    async def chunk(self, payload: ChunkRequestPayload) -> ChunkResponsePayload:
        start = time.perf_counter()
        file_name = payload.task.file_name
        file_type = _detect_file_type(file_name)
        cleaned_text = _clean_parsed_text(payload.text, file_name)
        documents = _split_documents_by_type(cleaned_text, payload.task.file_md5, file_name, file_type)
        chunks = [doc.page_content for doc in documents if doc.page_content.strip()]
        log_request(
            "ingestion_chunk",
            latency_ms=elapsed_ms(start),
            file_md5=payload.task.file_md5,
            file_type=file_type,
            chunk_size=TOKEN_CHUNK_SIZE,
            chunk_overlap=TOKEN_CHUNK_OVERLAP,
            chunks=len(chunks),
        )
        return ChunkResponsePayload(chunks=chunks)

    async def embed(self, payload: EmbedRequestPayload) -> EmbedResponsePayload:
        start = time.perf_counter()
        if not payload.texts:
            return EmbedResponsePayload(vectors=[])
        try:
            vectors = await self._embed_via_http_with_retry(payload.texts, use_dimensions=True)
        except Exception as exc:
            if self._settings.embedding_dimensions > 0 and _is_dimension_mismatch_error(exc):
                log_request(
                    "ingestion_embed_dimension_fallback",
                    file_md5=payload.task.file_md5,
                    requested_dimensions=self._settings.embedding_dimensions,
                    error=str(exc),
                )
                vectors = await self._embed_via_http_with_retry(payload.texts, use_dimensions=False)
            else:
                # Keep the LangChain client as a last-resort fallback for non-local providers.
                vectors = await self._embed_with_retry(payload.texts, use_dimensions=False)
        log_request("ingestion_embed", latency_ms=elapsed_ms(start), file_md5=payload.task.file_md5, batch=len(payload.texts))
        return EmbedResponsePayload(vectors=[[float(value) for value in item] for item in vectors])

    async def index(self, payload: IndexRequestPayload) -> IndexResponsePayload:
        start = time.perf_counter()
        if not payload.docs:
            return IndexResponsePayload(indexedCount=0)

        bulk_lines: list[str] = []
        for doc in payload.docs:
            vector_id = str(doc.get("vector_id", "")).strip()
            meta = {"index": {"_index": payload.indexName}}
            if vector_id:
                meta["index"]["_id"] = vector_id
            bulk_lines.append(json.dumps(meta, ensure_ascii=False))
            bulk_lines.append(json.dumps(doc, ensure_ascii=False))
        bulk_body = "\n".join(bulk_lines) + "\n"

        headers = {"Content-Type": "application/x-ndjson"}
        auth = None
        if self._settings.es_username:
            auth = (self._settings.es_username, self._settings.es_password)
        headers["X-Trace-ID"] = current_trace_id()

        response = await self._http.post(
            f"{self._settings.es_url.rstrip('/')}/_bulk?refresh=true",
            content=bulk_body.encode("utf-8"),
            headers=headers,
            auth=auth,
        )
        response.raise_for_status()
        body = response.json()
        if body.get("errors"):
            raise RuntimeError("bulk index completed with partial failures")
        items = body.get("items") or []
        log_request("ingestion_index", latency_ms=elapsed_ms(start), file_md5=payload.task.file_md5, indexed=len(items))
        return IndexResponsePayload(indexedCount=len(items))

    async def _embed_with_retry(self, texts: list[str], *, use_dimensions: bool) -> list[list[float]]:
        last_exc: Exception | None = None
        for attempt in range(1, 4):
            try:
                client = self._embeddings if use_dimensions else self._embeddings_without_dimensions
                vectors = await client.aembed_documents(texts)
                return [[float(value) for value in item] for item in vectors]
            except Exception as exc:
                last_exc = exc
                if attempt == 3:
                    break
                await asyncio.sleep(0.4 * attempt)

        try:
            direct_vectors = await self._embed_via_http(texts, use_dimensions=use_dimensions)
            log_request(
                "ingestion_embed_http_fallback",
                use_dimensions=use_dimensions,
                error=str(last_exc) if last_exc else "",
            )
            return direct_vectors
        except Exception:
            if last_exc is not None:
                raise last_exc
        raise RuntimeError("embedding failed without exception")

    async def _embed_via_http(self, texts: list[str], *, use_dimensions: bool) -> list[list[float]]:
        payload: dict[str, object] = {
            "model": self._settings.embedding.model,
            "input": texts,
        }
        if use_dimensions and self._settings.embedding_dimensions > 0:
            payload["dimensions"] = self._settings.embedding_dimensions
        response = await self._http.post(
            f"{self._settings.embedding.base_url.rstrip('/')}/embeddings",
            json=payload,
        )
        response.raise_for_status()
        body = response.json()
        vectors: list[list[float]] = []
        for item in body.get("data") or []:
            vector = item.get("embedding") or []
            vectors.append([float(value) for value in vector])
        if not vectors:
            raise RuntimeError("embedding http fallback returned empty vectors")
        return vectors

    async def _embed_via_http_with_retry(self, texts: list[str], *, use_dimensions: bool) -> list[list[float]]:
        last_exc: Exception | None = None
        for attempt in range(1, 4):
            try:
                return await self._embed_via_http(texts, use_dimensions=use_dimensions)
            except Exception as exc:
                last_exc = exc
                if attempt == 3:
                    break
                await asyncio.sleep(0.5 * attempt)
        if last_exc is not None:
            raise last_exc
        raise RuntimeError("embedding http retry failed without exception")


def _detect_mime_type(file_name: str) -> str:
    mime_type, _ = mimetypes.guess_type(file_name)
    return mime_type or "application/octet-stream"


def _detect_file_type(file_name: str) -> str:
    lower_name = (file_name or "").strip().lower()
    if lower_name.endswith((".md", ".markdown")):
        return "markdown"
    if lower_name.endswith(".pdf"):
        return "pdf"
    if lower_name.endswith((".xls", ".xlsx", ".csv")):
        return "excel"
    return "default"


@lru_cache(maxsize=8)
def _build_token_splitter(*, separators: tuple[str, ...]) -> RecursiveCharacterTextSplitter:
    return RecursiveCharacterTextSplitter.from_tiktoken_encoder(
        chunk_size=TOKEN_CHUNK_SIZE,
        chunk_overlap=TOKEN_CHUNK_OVERLAP,
        separators=list(separators),
    )


def _warm_splitter_cache() -> None:
    _build_token_splitter(separators=("\n\n", "\n", "?", "?", "?", ".", " ", ""))
    _build_token_splitter(separators=("\n## ", "\n### ", "\n#### ", "\n\n", "\n", "```", " ", ""))
    _build_token_splitter(separators=("\n\n", "\n", "?", "?", "?", " ", ""))
    _build_token_splitter(separators=("\n\n", "\n", " | ", " ", ""))


def _base_metadata(file_md5: str, file_name: str) -> dict[str, str]:
    return {
        "file_md5": file_md5,
        "file_name": file_name,
    }


def _split_documents_by_type(text: str, file_md5: str, file_name: str, file_type: str) -> list[Document]:
    metadata = _base_metadata(file_md5, file_name)
    if not text.strip():
        return []

    if file_type == "markdown":
        return _chunk_markdown(text, metadata)
    if file_type == "pdf":
        return _chunk_pdf(text, metadata)
    if file_type == "excel":
        return _chunk_excel(text, metadata)
    return _chunk_default(text, metadata)


def _chunk_default(text: str, metadata: dict[str, str]) -> list[Document]:
    separators = ("\n\n", "\n", "?", "?", "?", ".", " ", "")
    splitter = _build_token_splitter(separators=separators)
    return splitter.split_documents([Document(page_content=text, metadata=metadata)])


def _chunk_markdown(text: str, metadata: dict[str, str]) -> list[Document]:
    headers_to_split_on = [
        ("#", "h1"),
        ("##", "h2"),
        ("###", "h3"),
        ("####", "h4"),
    ]
    header_splitter = MarkdownHeaderTextSplitter(headers_to_split_on=headers_to_split_on, strip_headers=False)
    sections = header_splitter.split_text(text)
    if not sections:
        sections = [Document(page_content=text, metadata={})]

    separators = ("\n## ", "\n### ", "\n#### ", "\n\n", "\n", "```", " ", "")
    splitter = _build_token_splitter(separators=separators)
    docs: list[Document] = []
    for section in sections:
        merged_metadata = dict(metadata)
        merged_metadata.update({str(k): str(v) for k, v in section.metadata.items()})
        docs.extend(splitter.split_documents([Document(page_content=section.page_content, metadata=merged_metadata)]))
    return docs


def _chunk_pdf(text: str, metadata: dict[str, str]) -> list[Document]:
    pages = _split_pdf_pages(text)
    cleaned_pages = _remove_pdf_repeated_headers_and_footers(pages)
    separators = ("\n\n", "\n", "?", "?", "?", " ", "")
    splitter = _build_token_splitter(separators=separators)
    docs: list[Document] = []
    if not cleaned_pages:
        return []

    for idx, page in enumerate(cleaned_pages, start=1):
        if not page.strip():
            continue
        page_metadata = dict(metadata)
        page_metadata["page"] = str(idx)
        docs.extend(splitter.split_documents([Document(page_content=page, metadata=page_metadata)]))
    return docs


def _chunk_excel(text: str, metadata: dict[str, str]) -> list[Document]:
    sections = _build_excel_sections(text)
    splitter = _build_token_splitter(separators=("\n\n", "\n", " | ", " ", ""))
    docs: list[Document] = []
    if not sections:
        sections = [text]

    for idx, section in enumerate(sections, start=1):
        if not section.strip():
            continue
        section_metadata = dict(metadata)
        section_metadata["section"] = str(idx)
        docs.extend(splitter.split_documents([Document(page_content=section, metadata=section_metadata)]))
    return docs


def _clean_parsed_text(text: str, file_name: str) -> str:
    file_type = _detect_file_type(file_name)
    cleaned = (text or "").replace("\r\n", "\n").replace("\r", "\n")
    cleaned = cleaned.replace("\u00a0", " ").replace("\u200b", "")
    cleaned = cleaned.replace("\x00", "")

    if file_type == "markdown":
        cleaned = _clean_markdown_text(cleaned)
    elif file_type == "pdf":
        cleaned = _clean_pdf_text(cleaned)
    elif file_type == "excel":
        cleaned = _clean_excel_text(cleaned)
    else:
        cleaned = _clean_generic_text(cleaned)

    cleaned = re.sub(r"[ \t]+\n", "\n", cleaned)
    cleaned = re.sub(r"\n{3,}", "\n\n", cleaned)
    return cleaned.strip()


def _clean_generic_text(text: str) -> str:
    return re.sub(r"[ \t]{2,}", " ", text)


def _clean_markdown_text(text: str) -> str:
    text = re.sub(r"^---\n.*?\n---\n+", "", text, flags=re.DOTALL)
    text = re.sub(r"<!--.*?-->", "", text, flags=re.DOTALL)
    text = re.sub(r"[ \t]+\n", "\n", text)
    text = re.sub(r"\n{3,}", "\n\n", text)
    return text


def _clean_pdf_text(text: str) -> str:
    text = text.replace("\f", "\n\f\n")
    text = re.sub(r"[ \t]{2,}", " ", text)
    text = re.sub(r"\n\s*(缁楃悵s*\d+\s*妞ょpage\s*\d+|[0-9]+/[0-9]+)\s*\n", "\n", text, flags=re.IGNORECASE)
    text = re.sub(r"\n{3,}", "\n\n", text)
    return text


def _clean_excel_text(text: str) -> str:
    normalized_lines: list[str] = []
    for raw_line in text.splitlines():
        line = raw_line.replace("\t", " | ")
        line = re.sub(r"(?:\s*\|\s*){2,}", " | ", line)
        line = re.sub(r"[ \t]{2,}", " ", line).strip(" |")
        if not line:
            normalized_lines.append("")
            continue
        normalized_lines.append(line)
    return "\n".join(normalized_lines)


def _split_pdf_pages(text: str) -> list[str]:
    if "\f" not in text:
        return [text]
    return [page.strip() for page in re.split(r"\f+", text) if page.strip()]


def _remove_pdf_repeated_headers_and_footers(pages: list[str]) -> list[str]:
    if len(pages) < 2:
        return pages

    header_candidates: Counter[str] = Counter()
    footer_candidates: Counter[str] = Counter()
    per_page_lines: list[list[str]] = []

    for page in pages:
        lines = [line.strip() for line in page.splitlines() if line.strip()]
        per_page_lines.append(lines)
        for line in lines[:2]:
            if _looks_like_short_repeating_line(line):
                header_candidates[line] += 1
        for line in lines[-2:]:
            if _looks_like_short_repeating_line(line):
                footer_candidates[line] += 1

    repeated_headers = {line for line, count in header_candidates.items() if count >= 2}
    repeated_footers = {line for line, count in footer_candidates.items() if count >= 2}

    cleaned_pages: list[str] = []
    for lines in per_page_lines:
        trimmed = list(lines)
        while trimmed and trimmed[0] in repeated_headers:
            trimmed.pop(0)
        while trimmed and trimmed[-1] in repeated_footers:
            trimmed.pop()
        cleaned_pages.append("\n".join(trimmed).strip())
    return cleaned_pages


def _looks_like_short_repeating_line(line: str) -> bool:
    if len(line) > 80:
        return False
    if any(token in line for token in ("。", "！", "？", ".", "!", "?", ";", ":")):
        return False
    if re.fullmatch(r"(第\s*\d+\s*页|page\s*\d+|[0-9]+/[0-9]+|[0-9]+)", line, flags=re.IGNORECASE):
        return True
    return len(line.split()) <= 8


def _build_excel_sections(text: str) -> list[str]:
    sections: list[str] = []
    current_rows: list[str] = []

    for raw_line in text.splitlines():
        line = raw_line.strip()
        if not line:
            if current_rows:
                sections.append("\n".join(current_rows))
                current_rows = []
            continue

        if _looks_like_excel_sheet_header(line) and current_rows:
            sections.append("\n".join(current_rows))
            current_rows = [line]
            continue

        current_rows.append(line)
        if len(current_rows) >= 40:
            sections.append("\n".join(current_rows))
            current_rows = []

    if current_rows:
        sections.append("\n".join(current_rows))

    return sections


def _looks_like_excel_sheet_header(line: str) -> bool:
    lower_line = line.lower()
    return lower_line.startswith("sheet") or lower_line.startswith("工作表") or lower_line.endswith(":")


def _is_dimension_mismatch_error(exc: Exception) -> bool:
    message = str(exc).lower()
    return (
        "requested dimensions" in message and "model outputs" in message
    ) or "dimension mismatch" in message or ("dimensions" in message and "invalid" in message)


