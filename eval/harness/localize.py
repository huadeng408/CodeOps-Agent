"""Hierarchical fault localization: file level, then function level.

Why the harness needs this
--------------------------
``eval/benchmarks/swebench.py`` builds ``task_description`` from the issue title
and body and nothing else.  The agent therefore begins with a paragraph of prose
and a repository of several thousand files, and spends its early turns doing
``Glob``/``Grep`` archaeology.  Turn budget spent finding the file is turn budget
not spent fixing the bug.

Shape borrowed from Agentless, which runs localization as a descending hierarchy
— file, then class/function, then edit location — before any repair
(https://github.com/openautocoder/agentless).  A granularity study on
repository-scale repair found **function level** to be the best target, above
both file and line level (https://arxiv.org/abs/2604.00167), which is why this
stops at functions rather than trying to name exact lines: a confidently wrong
line number is worse than an honest function-level range.

Why lexical rather than embedding-based
---------------------------------------
The project's Elasticsearch index holds the *techdocs* corpus (Docker, Git, Go,
Kubernetes, PostgreSQL, Python documentation — 24,877 chunks), not repository
source, so it cannot serve this at all.  Building a per-instance embedding index
over a repository would cost more per instance than the agent turns it saves, and
on a 16GB host it competes with the containers the run needs.  So this is BM25
over structural text extracted with the stdlib ``ast`` module: no service
dependency, no model download, deterministic, and hermetic.

What this may read
------------------
The issue text, and the repository at ``base_commit``.  Nothing else.  In
particular not ``test_patch``, not ``FAIL_TO_PASS``/``PASS_TO_PASS``, not the
developer's patch, not ``hints_text`` — see :mod:`eval.harness.leakage`, whose
allow-list this module's inputs pass through and whose test suite asserts
structurally that the names do not appear here.
"""

from __future__ import annotations

import ast
import math
import re
from dataclasses import dataclass, field
from pathlib import Path
from typing import Iterable, Sequence

#: Directories never worth searching for the fix.
_SKIP_DIRS: frozenset[str] = frozenset(
    {
        ".git", ".github", "__pycache__", ".pytest_cache", ".mypy_cache",
        "build", "dist", "docs", "examples", "benchmarks", ".tox", ".eggs",
        "node_modules", ".venv", "venv",
    }
)

#: How many files survive stage 1, and how many functions stage 2.
DEFAULT_TOP_FILES = 8
DEFAULT_TOP_FUNCTIONS = 12

#: BM25 parameters.  Okapi defaults; nothing here is tuned against the answers,
#: which would be a form of leakage through hyperparameters.
BM25_K1 = 1.5
BM25_B = 0.75

_TOKEN_RE = re.compile(r"[A-Za-z_][A-Za-z0-9_]*")


def tokenise(text: str) -> list[str]:
    """Lowercased identifier tokens, plus the parts of snake/camel case names.

    An issue usually says ``separability_matrix`` while the code also contains
    ``separability`` and ``matrix``; splitting both sides means the two can meet
    without a stemmer.
    """
    tokens: list[str] = []
    for match in _TOKEN_RE.finditer(text):
        raw = match.group(0)
        lowered = raw.lower()
        tokens.append(lowered)
        parts = [p for p in raw.split("_") if p]
        if len(parts) > 1:
            tokens.extend(p.lower() for p in parts)
        for camel in re.findall(r"[A-Z]+(?![a-z])|[A-Z][a-z]+|[a-z]+|\d+", raw):
            if camel.lower() != lowered:
                tokens.append(camel.lower())
    return tokens


@dataclass
class _Document:
    key: str
    tokens: list[str]
    payload: object = None


class BM25:
    """Minimal Okapi BM25.

    Written here rather than pulled in as a dependency: ``rank_bm25`` is not
    installed, and the project's BM25 lives behind Elasticsearch in the Go
    backend, which is neither reachable from this code path nor desirable to
    require for localization.
    """

    def __init__(self, documents: Sequence[_Document]) -> None:
        self._documents = list(documents)
        self._freqs: list[dict[str, int]] = []
        self._lengths: list[int] = []
        doc_count = len(self._documents)
        appearances: dict[str, int] = {}
        for doc in self._documents:
            counts: dict[str, int] = {}
            for token in doc.tokens:
                counts[token] = counts.get(token, 0) + 1
            self._freqs.append(counts)
            self._lengths.append(len(doc.tokens))
            for token in counts:
                appearances[token] = appearances.get(token, 0) + 1
        self._avg_length = (
            sum(self._lengths) / doc_count if doc_count else 0.0
        )
        # Okapi IDF, floored at zero so a token present in every document
        # contributes nothing instead of pushing scores negative.
        self._idf = {
            token: max(
                0.0,
                math.log(
                    1.0 + (doc_count - n + 0.5) / (n + 0.5)
                ),
            )
            for token, n in appearances.items()
        }

    def score(self, query_tokens: Iterable[str]) -> list[tuple[float, _Document]]:
        query = list(query_tokens)
        scored: list[tuple[float, _Document]] = []
        for index, doc in enumerate(self._documents):
            freqs = self._freqs[index]
            length = self._lengths[index] or 1
            total = 0.0
            for token in query:
                tf = freqs.get(token)
                if not tf:
                    continue
                idf = self._idf.get(token, 0.0)
                denom = tf + BM25_K1 * (
                    1 - BM25_B + BM25_B * length / (self._avg_length or 1)
                )
                total += idf * (tf * (BM25_K1 + 1)) / denom
            if total > 0:
                scored.append((total, doc))
        # Deterministic order: score desc, then key asc, so an identical repo and
        # issue always produce an identical prompt.  Non-determinism here would
        # make the two arms of a paired experiment incomparable.
        scored.sort(key=lambda item: (-item[0], item[1].key))
        return scored


@dataclass
class FunctionSite:
    """One candidate edit location."""

    path: str
    qualname: str
    lineno: int
    end_lineno: int
    score: float = 0.0

    @property
    def location(self) -> str:
        return f"{self.path}:{self.lineno}-{self.end_lineno}"


@dataclass
class LocalizationResult:
    files: list[str] = field(default_factory=list)
    functions: list[FunctionSite] = field(default_factory=list)
    file_scores: dict[str, float] = field(default_factory=dict)
    scanned_files: int = 0
    degraded_reason: str = ""

    @property
    def empty(self) -> bool:
        return not self.files and not self.functions


def _iter_python_files(root: Path) -> list[Path]:
    found: list[Path] = []
    for path in root.rglob("*.py"):
        if any(part in _SKIP_DIRS for part in path.relative_to(root).parts):
            continue
        found.append(path)
    return sorted(found)


def _file_profile(path: Path, root: Path) -> str:
    """Structural text for a file: its path, docstring, and API names.

    Deliberately not the whole file body.  Full text lets a long module
    outscore a short, precisely relevant one on term count alone, and it is the
    signature and docstring that actually carry the vocabulary an issue uses.
    """
    rel = path.relative_to(root).as_posix()
    parts: list[str] = [rel.replace("/", " ").replace(".py", "")]
    try:
        source = path.read_text(encoding="utf-8", errors="replace")
    except OSError:
        return " ".join(parts)
    try:
        tree = ast.parse(source)
    except SyntaxError:
        # A file we cannot parse still has a meaningful path.
        return " ".join(parts)
    module_doc = ast.get_docstring(tree) or ""
    parts.append(module_doc)
    for node in ast.walk(tree):
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
            parts.append(node.name)
            doc = ast.get_docstring(node) or ""
            parts.append(doc[:400])
    return " ".join(parts)


def _function_sites(path: Path, root: Path) -> list[tuple[FunctionSite, str]]:
    """Every function/method/class in *path*, with its searchable text."""
    rel = path.relative_to(root).as_posix()
    try:
        source = path.read_text(encoding="utf-8", errors="replace")
        tree = ast.parse(source)
    except (OSError, SyntaxError):
        return []
    lines = source.splitlines()
    sites: list[tuple[FunctionSite, str]] = []

    def visit(node: ast.AST, prefix: str) -> None:
        for child in ast.iter_child_nodes(node):
            if isinstance(
                child, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)
            ):
                qualname = f"{prefix}.{child.name}" if prefix else child.name
                start = getattr(child, "lineno", 1)
                end = getattr(child, "end_lineno", start) or start
                body = "\n".join(lines[start - 1 : end])
                text = " ".join(
                    [
                        rel.replace("/", " "),
                        qualname.replace(".", " "),
                        ast.get_docstring(child) or "",
                        body,
                    ]
                )
                sites.append(
                    (
                        FunctionSite(
                            path=rel,
                            qualname=qualname,
                            lineno=start,
                            end_lineno=end,
                        ),
                        text,
                    )
                )
                visit(child, qualname)

    visit(tree, "")
    return sites


def localize(
    issue_text: str,
    repo_root: str | Path,
    *,
    top_files: int = DEFAULT_TOP_FILES,
    top_functions: int = DEFAULT_TOP_FUNCTIONS,
) -> LocalizationResult:
    """Rank candidate files, then candidate functions within them.

    Never raises for repository-shaped reasons: an unreadable or empty tree
    yields a result with ``degraded_reason`` set, and the caller then runs the
    agent exactly as it did before.  Localization is an accelerator, and an
    accelerator that can fail the run is a liability.
    """
    root = Path(repo_root)
    if not issue_text.strip():
        return LocalizationResult(degraded_reason="empty issue text")
    if not root.is_dir():
        return LocalizationResult(degraded_reason=f"not a directory: {root}")

    files = _iter_python_files(root)
    if not files:
        return LocalizationResult(degraded_reason="no python files found")

    query = tokenise(issue_text)
    if not query:
        return LocalizationResult(
            scanned_files=len(files), degraded_reason="issue text yielded no tokens"
        )

    file_docs = [
        _Document(
            key=path.relative_to(root).as_posix(),
            tokens=tokenise(_file_profile(path, root)),
            payload=path,
        )
        for path in files
    ]
    ranked_files = BM25(file_docs).score(query)[:top_files]
    if not ranked_files:
        return LocalizationResult(
            scanned_files=len(files),
            degraded_reason="no file scored above zero for this issue",
        )

    result = LocalizationResult(
        files=[doc.key for _, doc in ranked_files],
        file_scores={doc.key: round(score, 4) for score, doc in ranked_files},
        scanned_files=len(files),
    )

    function_docs: list[_Document] = []
    for _score, doc in ranked_files:
        for site, text in _function_sites(doc.payload, root):  # type: ignore[arg-type]
            function_docs.append(
                _Document(key=f"{site.path}::{site.qualname}", tokens=tokenise(text), payload=site)
            )
    if function_docs:
        for score, doc in BM25(function_docs).score(query)[:top_functions]:
            site: FunctionSite = doc.payload  # type: ignore[assignment]
            site.score = round(score, 4)
            result.functions.append(site)
    return result


def render_localization_block(result: LocalizationResult) -> str:
    """Format *result* for prepending to the task description.

    The wording matters as much as the ranking.  These are BM25 hits over
    identifier text, not a verified diagnosis, so the block says "candidates,
    ranked by lexical similarity" and tells the agent to confirm before editing
    and to look elsewhere if they are wrong.  Presenting a guess as a conclusion
    is how a localizer turns into a way to send a capable agent confidently to
    the wrong file.
    """
    if result.empty:
        return ""
    lines: list[str] = [
        "## Candidate edit locations (automated pre-analysis)",
        "",
        "These were ranked by lexical (BM25) similarity between the issue text "
        "and the repository's identifiers, docstrings and function bodies. They "
        "are **candidates, not a diagnosis** — confirm by reading the code "
        "before editing, and search elsewhere if they do not fit.",
        "",
        f"Scanned {result.scanned_files} Python files.",
        "",
        "### Most likely files",
    ]
    for index, path in enumerate(result.files, 1):
        lines.append(f"{index}. `{path}` (score {result.file_scores.get(path, 0):.2f})")
    if result.functions:
        lines += ["", "### Most likely functions or classes"]
        for index, site in enumerate(result.functions, 1):
            lines.append(
                f"{index}. `{site.qualname}` in `{site.path}` "
                f"(lines {site.lineno}-{site.end_lineno}, score {site.score:.2f})"
            )
    lines.append("")
    return "\n".join(lines)
