"""Blind arbitration worksheet for DISPUTED qrels (E2 human review).

157 of 180 techdocs qrels are ``DISPUTED``: the two Sol review passes did not
agree, or one of them failed to parse (43 rows).  A human has to arbitrate them
before the golden set can carry a release claim, so this module builds the
worksheet that review works from.

Why not reuse :mod:`orchestrator.eval.annotation_export`: it strips only
``relevance``.  These rows additionally carry ``pass_a_verdict`` and
``pass_b_verdict`` — each a dict containing ``relevance_correct`` — plus
``dispute_reason``, ``review_confidence`` and ``review_evidence``.  Exporting
any of those hands the reviewer the prior AI judgement, which is exactly the
prior-score leakage the blind-review firewall exists to prevent.  Showing "pass
A said not-relevant, pass B said relevant" also anchors the arbitration on the
disagreement rather than on the document.

What the reviewer needs instead is the *evidence*: the query text and the real
section text of the document.  ``review_evidence`` in the qrels cannot serve —
it is the same Elasticsearch document id repeated seven times, carrying no
information beyond ``document_id``.  So the text is pulled from the live index.

Section-path caveat recorded in every row: 178 of 180 qrel ``section_path``
values do not exist in the index for the document they name (measured
2026-08-10 against ``knowledge_base_v2_bge_m3``).  The worksheet therefore
shows the document's actual sections and asks the reviewer to judge
document-level relevance, rather than pretending the named section is real.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import random
import sys
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any

__all__ = [
    "ANSWER_FIELDS",
    "WORKSHEET_SEED",
    "build_worksheet",
    "fetch_document_sections",
    "main",
    "redact_hash",
]

#: Deterministic shuffle so the same qrels always yield the same worksheet.
WORKSHEET_SEED = 20260810

#: Fields that carry, or proxy for, the answer. None may reach the reviewer.
#: pass_a_verdict / pass_b_verdict each contain "relevance_correct", so they
#: are answer-bearing even though they are not the label itself.
ANSWER_FIELDS = frozenset(
    {
        "relevance",
        "pass_a_verdict",
        "pass_b_verdict",
        "dispute_reason",
        "review_confidence",
        "review_evidence",
        "review_status",
        "review_prompt_hash",
        "review_timestamp",
    }
)

#: Judgements the arbitrator records, left blank in the exported worksheet.
REVIEW_FIELDS = (
    "verdict_relevant",
    "verdict_answerable",
    "verdict_language_correct",
    "verdict_query_type_correct",
    "verdict_evidence_sufficient",
    "reviewer_notes",
)

DEFAULT_ES = "http://127.0.0.1:9200"
DEFAULT_INDEX = "knowledge_base_v2_bge_m3"
#: Chunks shown per document. Enough to judge relevance without dumping a book.
MAX_SECTIONS = 6
#: Characters of each chunk shown.
SNIPPET_CHARS = 600


def redact_hash(query_id: str, document_id: str, reviewer: str) -> str:
    """Stable 16-hex-char row identity, built only from fields already present."""
    payload = f"{query_id}|{document_id}|{reviewer}"
    return hashlib.sha256(payload.encode("utf-8")).hexdigest()[:16]


def _es_post(base_url: str, path: str, body: dict[str, Any]) -> dict[str, Any]:
    request = urllib.request.Request(
        base_url.rstrip("/") + path,
        data=json.dumps(body).encode("utf-8"),
        headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(request, timeout=30) as response:
        return json.loads(response.read())


def fetch_document_sections(
    document_id: str,
    *,
    base_url: str = DEFAULT_ES,
    index: str = DEFAULT_INDEX,
    max_sections: int = MAX_SECTIONS,
    snippet_chars: int = SNIPPET_CHARS,
) -> list[dict[str, Any]]:
    """Return the document's real sections and text snippets from the index.

    Raises on an unreachable index rather than returning an empty list: a
    worksheet whose evidence silently went missing would send the reviewer to
    judge a blank page.
    """
    try:
        result = _es_post(
            base_url,
            f"/{index}/_search",
            {
                "size": max_sections,
                "query": {"term": {"document_id": document_id}},
                "_source": ["section_path", "chunk_id", "text_content", "source_url"],
                "sort": [{"chunk_id": "asc"}],
            },
        )
    except (urllib.error.URLError, OSError) as exc:
        raise RuntimeError(
            f"INDEX_UNREACHABLE: cannot read {index} at {base_url}: {exc}. The "
            "worksheet needs real document text; refusing to emit blank evidence."
        ) from exc

    hits = result.get("hits", {}).get("hits", [])
    sections: list[dict[str, Any]] = []
    for hit in hits:
        source = hit.get("_source", {})
        text = (source.get("text_content") or "").strip()
        sections.append(
            {
                "chunk_id": source.get("chunk_id"),
                "section_path": source.get("section_path") or [],
                "text": text[:snippet_chars],
                "truncated": len(text) > snippet_chars,
            }
        )
    return sections


def _load_rows(path: Path) -> list[dict[str, Any]]:
    rows: list[dict[str, Any]] = []
    for lineno, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        line = line.strip()
        if not line:
            continue
        row = json.loads(line)
        if not isinstance(row, dict):
            raise ValueError(f"qrels line {lineno} is not an object")
        rows.append(row)
    return rows


def build_worksheet(
    qrels_path: Path | str,
    queries_path: Path | str,
    out_path: Path | str,
    *,
    reviewer: str = "reviewer-1",
    status: str = "DISPUTED",
    base_url: str = DEFAULT_ES,
    index: str = DEFAULT_INDEX,
    with_evidence: bool = True,
) -> tuple[Path, int]:
    """Write the blind arbitration worksheet; returns (path, row count)."""
    rows = [r for r in _load_rows(Path(qrels_path)) if r.get("review_status") == status]
    queries = {
        r["query_id"]: r.get("query", "")
        for r in _load_rows(Path(queries_path))
        if "query_id" in r
    }

    rng = random.Random(WORKSHEET_SEED)
    rng.shuffle(rows)

    worksheet: list[dict[str, Any]] = []
    for row in rows:
        query_id = str(row.get("query_id", ""))
        document_id = str(row.get("document_id", ""))

        out_row = {k: v for k, v in row.items() if k not in ANSWER_FIELDS}
        out_row["query"] = queries.get(query_id, "")
        out_row["reviewer"] = reviewer
        out_row["row_hash"] = redact_hash(query_id, document_id, reviewer)
        # The qrel's own section_path is unreliable (178/180 do not resolve),
        # so it is relabelled as a claim to be checked, not a fact.
        out_row["section_path_claimed"] = out_row.pop("section_path", [])
        out_row["section_path_note"] = (
            "This claimed section was NOT found in the index for this document "
            "in 178 of 180 rows. Judge document-level relevance using the "
            "actual sections below."
        )
        if with_evidence:
            out_row["document_sections"] = fetch_document_sections(
                document_id, base_url=base_url, index=index
            )
        for field in REVIEW_FIELDS:
            out_row[field] = ""
        worksheet.append(out_row)

    out = Path(out_path)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(
        "".join(json.dumps(r, ensure_ascii=False) + "\n" for r in worksheet),
        encoding="utf-8",
    )
    return out, len(worksheet)


def _write_markdown(jsonl_path: Path, md_path: Path) -> Path:
    """Render the worksheet as Markdown for review outside a JSON viewer."""
    rows = _load_rows(jsonl_path)
    lines: list[str] = [
        "# DISPUTED qrels — blind arbitration worksheet",
        "",
        f"Rows: **{len(rows)}**. Deterministic order (seed {WORKSHEET_SEED}).",
        "",
        "The prior AI verdicts, the dispute reason, the confidence and the gold "
        "relevance are all withheld on purpose: seeing them would anchor the "
        "arbitration on the earlier disagreement instead of on the document.",
        "",
        "For each row decide **is this document relevant to this query** and fill "
        "in `verdict_relevant` (`yes` / `no`). The other verdict fields are "
        "optional. Judge at document level: the claimed section path is unreliable "
        "(178 of 180 do not exist in the index for the document they name).",
        "",
        "---",
        "",
    ]
    for i, row in enumerate(rows, 1):
        lines.append(f"## {i}. `{row.get('row_hash','')}`  ({row.get('source_id','')})")
        lines.append("")
        lines.append(f"**Query** ({row.get('language','')} / {row.get('query_type','')}): "
                     f"{row.get('query','')}")
        lines.append("")
        lines.append(f"**Document**: `{row.get('document_id','')}`")
        lines.append("")
        claimed = row.get("section_path_claimed") or []
        lines.append(f"**Claimed section** (unverified): `{claimed}`")
        lines.append("")
        sections = row.get("document_sections") or []
        if sections:
            lines.append("**Actual sections in the index:**")
            lines.append("")
            for section in sections:
                path = section.get("section_path") or []
                lines.append(f"- `{path}`")
                text = (section.get("text") or "").replace("\n", " ").strip()
                if text:
                    suffix = "…" if section.get("truncated") else ""
                    lines.append(f"  > {text}{suffix}")
            lines.append("")
        else:
            lines.append("_No sections retrieved for this document._")
            lines.append("")
        lines.append("`verdict_relevant:` ______   `notes:` ______")
        lines.append("")
        lines.append("---")
        lines.append("")

    md_path.parent.mkdir(parents=True, exist_ok=True)
    md_path.write_text("\n".join(lines), encoding="utf-8")
    return md_path


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        prog="dispute_worksheet",
        description="Build the blind arbitration worksheet for DISPUTED qrels",
    )
    parser.add_argument("--qrels", default="data/eval/techdocs/qrels.sol-review.jsonl")
    parser.add_argument("--queries", default="data/eval/techdocs/queries.text.jsonl")
    parser.add_argument("--out", default="data/eval/techdocs/review/disputed-worksheet.jsonl")
    parser.add_argument("--markdown", default="data/eval/techdocs/review/disputed-worksheet.md")
    parser.add_argument("--reviewer", default="reviewer-1")
    parser.add_argument("--status", default="DISPUTED")
    parser.add_argument("--es", default=DEFAULT_ES)
    parser.add_argument("--index", default=DEFAULT_INDEX)
    parser.add_argument("--no-evidence", action="store_true")
    args = parser.parse_args(argv)

    try:
        path, count = build_worksheet(
            args.qrels,
            args.queries,
            args.out,
            reviewer=args.reviewer,
            status=args.status,
            base_url=args.es,
            index=args.index,
            with_evidence=not args.no_evidence,
        )
    except (RuntimeError, ValueError, OSError) as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        return 2

    print(f"worksheet : {path}  ({count} rows)")

    # Leakage self-check: no answer-bearing field may survive into the export.
    leaked = set()
    for row in _load_rows(path):
        leaked |= ANSWER_FIELDS & set(row)
    if leaked:
        print(f"ERROR: answer fields leaked into the worksheet: {sorted(leaked)}",
              file=sys.stderr)
        return 2
    print(f"leak check: clean ({len(ANSWER_FIELDS)} answer-bearing fields withheld)")

    if args.markdown:
        md = _write_markdown(path, Path(args.markdown))
        print(f"markdown  : {md}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
