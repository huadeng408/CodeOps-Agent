from __future__ import annotations

from dataclasses import dataclass, field

from .elements import Element


@dataclass
class StructuredChunk:
    document_id: str
    chunk_id: str
    parent_chunk_id: str
    text: str
    embedding_text: str
    section_path: list[str] = field(default_factory=list)
    page_id: str = ""
    page_span: list[int] = field(default_factory=list)
    element_ids: list[str] = field(default_factory=list)
    element_types: list[str] = field(default_factory=list)
    bbox_refs: list[str] = field(default_factory=list)
    asset_refs: list[str] = field(default_factory=list)
    token_count: int = 0
    tokenizer_id: str = "whitespace-v1"
    parser_name: str = "mineru"
    parser_version: str = ""
    corpus_generation: str = "techdocs-2026-07-30-v1"
    overlap_tokens: int = 0


def chunk_elements(
    elements: list[Element],
    *,
    child_tokens: int = 384,
    parent_tokens: int = 1500,
    corpus_generation: str = "techdocs-2026-07-30-v1",
) -> list[StructuredChunk]:
    if child_tokens <= 0 or parent_tokens <= 0:
        raise ValueError("chunk token limits must be positive")
    result: list[StructuredChunk] = []
    parent_number = 0
    for section, section_elements in _sections(elements):
        if not section_elements:
            continue
        parent_number += 1
        parent_id = f"{section_elements[0].document_id}:parent:{parent_number}"
        children = _chunk_section(section_elements, parent_id, child_tokens, corpus_generation)
        # Keep parent context bounded without changing child text or provenance.
        parent_context = " ".join(item.text for item in children)
        if _token_count(parent_context) > parent_tokens:
            parent_context = " ".join(parent_context.split()[:parent_tokens])
        for child in children:
            child.embedding_text = _contextual_text(child, section, parent_context)
        result.extend(children)
    return result


def _sections(elements: list[Element]):
    current_path: tuple[str, ...] | None = None
    current: list[Element] = []
    for element in elements:
        path = tuple(element.heading_path)
        if current and path != current_path:
            yield current_path or (), current
            current = []
        current_path = path
        current.append(element)
    if current:
        yield current_path or (), current


def _chunk_section(elements: list[Element], parent_id: str, child_tokens: int, generation: str) -> list[StructuredChunk]:
    chunks: list[StructuredChunk] = []
    sequence = 0
    for element in elements:
        if element.type == "table":
            pieces = _split_table(element, child_tokens)
        elif element.type in {"image", "equation", "code", "heading", "list", "footnote", "page_break"}:
            pieces = [element.text or element.latex or element.caption or element.html]
        else:
            pieces = _split_text(element.text or element.html, child_tokens)
        for piece in pieces:
            if not piece.strip():
                continue
            sequence += 1
            chunks.append(_make_chunk(element, piece, parent_id, sequence, generation))
    return chunks


def _make_chunk(element: Element, text: str, parent_id: str, sequence: int, generation: str) -> StructuredChunk:
    page_id = f"{element.document_id}:p{element.page_index}"
    bbox = ",".join(str(value) for value in element.bbox)
    return StructuredChunk(
        document_id=element.document_id,
        chunk_id=f"{element.document_id}:chunk:{sequence}",
        parent_chunk_id=parent_id,
        text=text.strip(),
        embedding_text=text.strip(),
        section_path=list(element.heading_path),
        page_id=page_id,
        page_span=[element.page_index, element.page_index],
        element_ids=[element.element_id],
        element_types=[element.type],
        bbox_refs=[f"{element.element_id}:{bbox}"],
        asset_refs=[element.image_path] if element.image_path else [],
        token_count=_token_count(text),
        parser_name=element.parser_name,
        parser_version=element.parser_version,
        corpus_generation=generation,
    )


def _split_text(text: str, limit: int) -> list[str]:
    words = text.split()
    if not words:
        return []
    return [" ".join(words[index : index + limit]) for index in range(0, len(words), limit)]


def _split_table(element: Element, limit: int) -> list[str]:
    source = element.text or element.html
    rows = [row.strip() for row in source.splitlines() if row.strip()]
    if len(rows) <= 1:
        return [source]
    header = rows[0]
    body = rows[1:]
    pieces: list[str] = []
    current: list[str] = []
    current_tokens = _token_count(header)
    for row in body:
        row_tokens = _token_count(row)
        if current and current_tokens + row_tokens > limit:
            pieces.append("\n".join([header, *current]))
            current = []
            current_tokens = _token_count(header)
        current.append(row)
        current_tokens += row_tokens
    if current:
        pieces.append("\n".join([header, *current]))
    return pieces or [source]


def _contextual_text(chunk: StructuredChunk, section: tuple[str, ...], parent_context: str) -> str:
    prefix = " / ".join(item for item in section if item)
    parts = [item for item in (prefix, chunk.text) if item]
    if chunk.asset_refs:
        parts.append(" ".join(chunk.asset_refs))
    return "\n".join(parts)


def _token_count(text: str) -> int:
    return len(text.split())

