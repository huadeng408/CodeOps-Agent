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
    source_sha256: str = ""
    source_url: str = ""
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
    target_index: str = ""
    overlap_tokens: int = 0


def chunk_elements(
    elements: list[Element],
    *,
    child_tokens: int = 384,
    parent_tokens: int = 1500,
    overlap_tokens: int | None = None,
    corpus_generation: str = "techdocs-2026-07-30-v1",
) -> list[StructuredChunk]:
    if child_tokens <= 0 or parent_tokens <= 0:
        raise ValueError("chunk token limits must be positive")
    if overlap_tokens is None:
        overlap_tokens = min(48, child_tokens // 8)
    if overlap_tokens < 0 or overlap_tokens >= child_tokens:
        raise ValueError("overlap_tokens must be non-negative and smaller than child_tokens")
    result: list[StructuredChunk] = []
    parent_number = 0
    chunk_number = 0
    for section, section_elements in _sections(elements):
        if not section_elements:
            continue
        children = _chunk_section(
            section_elements,
            child_tokens,
            overlap_tokens,
            corpus_generation,
            chunk_number,
        )
        chunk_number += len(children)
        parent_id = ""
        parent_size = 0
        previous_element_ids: list[str] = []
        for child in children:
            same_source_element = child.element_ids == previous_element_ids
            same_hard_boundary = same_source_element and any(
                element_type in {"table", "image", "equation", "code"}
                for element_type in child.element_types
            )
            if not parent_id or (
                parent_size > 0
                and parent_size + child.token_count > parent_tokens
                and not same_hard_boundary
            ):
                parent_number += 1
                parent_id = f"{section_elements[0].document_id}:parent:{parent_number}"
                parent_size = 0
            child.parent_chunk_id = parent_id
            parent_size += child.token_count
            previous_element_ids = child.element_ids
            child.embedding_text = _contextual_text(child, section, section_elements)
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


def _chunk_section(
    elements: list[Element],
    child_tokens: int,
    overlap_tokens: int,
    generation: str,
    sequence_offset: int = 0,
) -> list[StructuredChunk]:
    chunks: list[StructuredChunk] = []
    sequence = 0
    pending_elements: list[Element] = []
    pending_parts: list[str] = []

    def append_chunk(chunk_elements: list[Element], text: str, overlap: int = 0) -> None:
        nonlocal sequence
        if not text.strip():
            return
        sequence += 1
        chunks.append(_make_chunk(chunk_elements, text, sequence + sequence_offset, generation, overlap))

    def flush_pending() -> None:
        if pending_elements:
            append_chunk(list(pending_elements), "\n\n".join(pending_parts))
            pending_elements.clear()
            pending_parts.clear()

    for element in elements:
        source = _element_source(element)
        if not source.strip():
            continue
        if element.type in {"heading", "text", "list", "footnote"}:
            if _token_count(source) > child_tokens:
                flush_pending()
                for piece, overlap in _split_text(source, child_tokens, overlap_tokens):
                    append_chunk([element], piece, overlap)
                continue
            candidate = "\n\n".join([*pending_parts, source])
            if pending_elements and _token_count(candidate) > child_tokens:
                flush_pending()
            pending_elements.append(element)
            pending_parts.append(source)
            continue

        flush_pending()
        if element.type == "table":
            pieces = _split_table(element, child_tokens)
        elif element.type == "code":
            pieces = _split_lines(source, child_tokens)
        else:
            pieces = [source]
        for piece in pieces:
            append_chunk([element], piece)
    flush_pending()
    return chunks


def _make_chunk(
    elements: list[Element],
    text: str,
    sequence: int,
    generation: str,
    overlap_tokens: int = 0,
) -> StructuredChunk:
    first = elements[0]
    pages = sorted({element.page_index for element in elements})
    page_id = f"{first.document_id}:p{pages[0]}"
    return StructuredChunk(
        document_id=first.document_id,
        source_sha256=first.source_sha256,
        source_url=first.source_url,
        chunk_id=f"{first.document_id}:chunk:{sequence}",
        parent_chunk_id="",
        text=text,
        embedding_text=text,
        section_path=list(first.heading_path),
        page_id=page_id,
        page_span=[pages[0], pages[-1]],
        element_ids=[element.element_id for element in elements],
        element_types=list(dict.fromkeys(element.type for element in elements)),
        bbox_refs=[
            f"{element.element_id}:{','.join(str(value) for value in element.bbox)}"
            for element in elements
        ],
        asset_refs=list(dict.fromkeys(element.image_path for element in elements if element.image_path)),
        token_count=_token_count(text),
        parser_name=next((element.parser_name for element in elements if element.parser_name), "mineru"),
        parser_version=next((element.parser_version for element in elements if element.parser_version), ""),
        corpus_generation=generation,
        overlap_tokens=overlap_tokens,
    )


def _element_source(element: Element) -> str:
    if element.type == "equation":
        return element.latex or element.text
    if element.type == "image":
        return element.text or element.caption or element.html
    return element.text or element.html or element.latex or element.caption


def _split_text(text: str, limit: int, overlap: int) -> list[tuple[str, int]]:
    words = text.split()
    if not words:
        return []
    step = limit - overlap
    pieces: list[tuple[str, int]] = []
    for index in range(0, len(words), step):
        piece = words[index : index + limit]
        if not piece:
            break
        pieces.append((" ".join(piece), 0 if index == 0 else min(overlap, len(piece))))
        if index + limit >= len(words):
            break
    return pieces


def _split_lines(text: str, limit: int) -> list[str]:
    lines = text.splitlines() or [text]
    pieces: list[str] = []
    current: list[str] = []
    for line in lines:
        candidate = "\n".join([*current, line])
        if current and _token_count(candidate) > limit:
            pieces.append("\n".join(current))
            current = []
        current.append(line)
    if current:
        pieces.append("\n".join(current))
    return pieces


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


def _contextual_text(chunk: StructuredChunk, section: tuple[str, ...], elements: list[Element]) -> str:
    prefix = " / ".join(item for item in section if item)
    captions = list(dict.fromkeys(element.caption for element in elements if element.element_id in chunk.element_ids and element.caption))
    parts = [item for item in (prefix, *captions, chunk.text) if item]
    return "\n".join(parts)


def _token_count(text: str) -> int:
    return len(text.split())
