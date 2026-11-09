"""AST symbol dependency directed graph and Personalized PageRank repository map.

Extracts definitions (classes, functions, methods, structs, interfaces) and
cross-file symbol references across workspace files (using Tree-sitter when
available, with a deterministic Python ``ast`` + multi-language syntax parser
fallback), builds a directed dependency graph, ranks files/symbols via
Personalized PageRank (PPR), and packs P0/P1/P2 context tiers within a strict
token budget.
"""

from __future__ import annotations

import ast
import re
from collections import defaultdict
from dataclasses import dataclass, field
from pathlib import Path
from typing import Iterable, Mapping

_GO_DEF_RE = re.compile(
    r"^\s*(?:func\s+(?:\([^)]+\)\s*)?([A-Za-z_]\w*)|type\s+([A-Za-z_]\w*)\s+(?:struct|interface))",
    re.MULTILINE,
)
_TS_DEF_RE = re.compile(
    r"^\s*(?:export\s+)?(?:async\s+)?(?:function|class|interface|type)\s+([A-Za-z_]\w*)",
    re.MULTILINE,
)
_IDENT_RE = re.compile(r"\b([A-Za-z_][A-Za-z0-9_]{2,})\b")


@dataclass(frozen=True, slots=True)
class SymbolDef:
    name: str
    kind: str
    file_path: str
    line: int
    signature: str


@dataclass(slots=True)
class SymbolDependencyGraph:
    """Directed symbol reference graph over workspace files."""

    definitions: dict[str, list[SymbolDef]] = field(default_factory=lambda: defaultdict(list))
    symbol_to_files: dict[str, set[str]] = field(default_factory=lambda: defaultdict(set))
    edges: dict[str, dict[str, float]] = field(default_factory=lambda: defaultdict(lambda: defaultdict(float)))
    nodes: set[str] = field(default_factory=set)

    def add_file_definitions(self, rel_path: str, defs: Iterable[SymbolDef]) -> None:
        self.nodes.add(rel_path)
        for item in defs:
            self.definitions[rel_path].append(item)
            self.symbol_to_files[item.name].add(rel_path)

    def add_reference_edge(self, src_file: str, dst_file: str, weight: float = 1.0) -> None:
        self.nodes.add(src_file)
        self.nodes.add(dst_file)
        if src_file == dst_file or weight <= 0:
            return
        self.edges[src_file][dst_file] += float(weight)


def extract_file_symbols(rel_path: str, source_text: str) -> tuple[list[SymbolDef], set[str]]:
    """Extract symbol definitions and referenced identifiers from a source file."""
    suffix = Path(rel_path).suffix.lower()
    if suffix == ".py":
        return _extract_python_ast_symbols(rel_path, source_text)
    if suffix == ".go":
        return _extract_regex_symbols(rel_path, source_text, _GO_DEF_RE, default_kind="go_symbol")
    if suffix in {".ts", ".tsx", ".js", ".jsx"}:
        return _extract_regex_symbols(rel_path, source_text, _TS_DEF_RE, default_kind="ts_symbol")
    return _extract_regex_symbols(rel_path, source_text, _GO_DEF_RE, default_kind="symbol")


def _extract_python_ast_symbols(rel_path: str, source_text: str) -> tuple[list[SymbolDef], set[str]]:
    defs: list[SymbolDef] = []
    refs: set[str] = set()
    try:
        tree = ast.parse(source_text, filename=rel_path)
    except SyntaxError:
        return defs, set(_IDENT_RE.findall(source_text))

    for node in ast.walk(tree):
        if isinstance(node, ast.ClassDef):
            bases = ", ".join(ast.unparse(b) for b in node.bases) if hasattr(ast, "unparse") else ""
            sig = f"class {node.name}({bases})" if bases else f"class {node.name}"
            defs.append(SymbolDef(node.name, "class", rel_path, int(node.lineno), sig))
        elif isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
            args = [a.arg for a in node.args.args]
            prefix = "async def" if isinstance(node, ast.AsyncFunctionDef) else "def"
            sig = f"{prefix} {node.name}({', '.join(args)})"
            defs.append(SymbolDef(node.name, "function", rel_path, int(node.lineno), sig))
        elif isinstance(node, ast.Name) and isinstance(node.ctx, ast.Load):
            refs.add(node.id)
        elif isinstance(node, ast.Attribute):
            refs.add(node.attr)
        elif isinstance(node, ast.ImportFrom) and node.names:
            for alias in node.names:
                if alias.name and alias.name != "*":
                    refs.add(alias.name)
    defined_names = {d.name for d in defs}
    return defs, refs - defined_names


def _extract_regex_symbols(
    rel_path: str,
    source_text: str,
    pattern: re.Pattern[str],
    *,
    default_kind: str,
) -> tuple[list[SymbolDef], set[str]]:
    defs: list[SymbolDef] = []
    for match in pattern.finditer(source_text):
        name = next((g for g in match.groups() if g), "")
        if not name:
            continue
        line = source_text.count("\n", 0, match.start()) + 1
        sig = match.group(0).strip()
        defs.append(SymbolDef(name, default_kind, rel_path, line, sig))
    defined_names = {d.name for d in defs}
    refs = set(_IDENT_RE.findall(source_text)) - defined_names
    return defs, refs


def build_symbol_dependency_graph(files_content: Mapping[str, str]) -> SymbolDependencyGraph:
    """Build a cross-file directed symbol reference graph from file contents."""
    graph = SymbolDependencyGraph()
    extracted_refs: dict[str, set[str]] = {}
    for rel_path in sorted(files_content):
        defs, refs = extract_file_symbols(rel_path, files_content[rel_path])
        graph.add_file_definitions(rel_path, defs)
        extracted_refs[rel_path] = refs

    for src_path, refs in extracted_refs.items():
        for symbol in sorted(refs):
            target_files = graph.symbol_to_files.get(symbol)
            if not target_files:
                continue
            # Rare/specific symbols carry higher edge weight than ubiquitous names.
            idf_weight = 1.0 / max(1, len(target_files))
            for dst_path in sorted(target_files):
                if dst_path != src_path:
                    graph.add_reference_edge(src_path, dst_path, weight=idf_weight)
    return graph


def personalized_pagerank(
    graph: SymbolDependencyGraph,
    personalization: Mapping[str, float] | None = None,
    *,
    damping: float = 0.85,
    max_iter: int = 40,
    tol: float = 1e-6,
) -> dict[str, float]:
    """Compute Personalized PageRank scores over the symbol dependency graph."""
    nodes = sorted(graph.nodes)
    n = len(nodes)
    if n == 0:
        return {}
    alpha = min(max(float(damping), 0.01), 0.99)

    if personalization:
        raw_p = {node: max(0.0, float(personalization.get(node, 0.0))) for node in nodes}
        total_p = sum(raw_p.values())
        if total_p > 0:
            teleport = {node: raw_p[node] / total_p for node in nodes}
        else:
            teleport = {node: 1.0 / n for node in nodes}
    else:
        teleport = {node: 1.0 / n for node in nodes}

    ranks = dict(teleport)
    out_weights: dict[str, float] = {
        node: sum(graph.edges.get(node, {}).values()) for node in nodes
    }

    for _ in range(max(1, int(max_iter))):
        next_ranks = {node: (1.0 - alpha) * teleport[node] for node in nodes}
        dangling_mass = sum(ranks[node] for node in nodes if out_weights[node] <= 0.0)
        if dangling_mass > 0.0:
            for node in nodes:
                next_ranks[node] += alpha * dangling_mass * teleport[node]

        for src in nodes:
            total_out = out_weights[src]
            if total_out <= 0.0:
                continue
            src_share = alpha * ranks[src] / total_out
            for dst, weight in graph.edges[src].items():
                if dst in next_ranks:
                    next_ranks[dst] += src_share * weight

        delta = sum(abs(next_ranks[node] - ranks[node]) for node in nodes)
        ranks = next_ranks
        if delta <= tol:
            break

    return ranks


def _rank_graph_by_ppr(
    files_content: Mapping[str, str],
    *,
    seed_paths: Iterable[str] = (),
    query_symbols: Iterable[str] = (),
) -> tuple[SymbolDependencyGraph, dict[str, float], list[str]]:
    graph = build_symbol_dependency_graph(files_content)
    if not graph.nodes:
        return graph, {}, []
    personalization: dict[str, float] = defaultdict(float)
    for path in seed_paths:
        norm = str(path).replace("\\", "/").strip()
        if norm in graph.nodes:
            personalization[norm] += 3.0
    for sym in query_symbols:
        for owner_path in graph.symbol_to_files.get(str(sym).strip(), ()):
            personalization[owner_path] += 2.0
    scores = personalized_pagerank(graph, personalization)
    ranked_files = sorted(
        graph.nodes,
        key=lambda path: (-scores.get(path, 0.0), path),
    )
    return graph, scores, ranked_files


def _render_p0_repomap(
    graph: SymbolDependencyGraph,
    scores: Mapping[str, float],
    ranked_files: list[str],
    token_budget: int,
) -> str:
    if not ranked_files:
        return "P0 AST RepoMap (empty)"
    budget_bytes = max(64, int(token_budget) * 4)
    lines = ["P0 AST RepoMap (Personalized PageRank)"]
    used_bytes = len(lines[0].encode("utf-8"))
    for rel_path in ranked_files:
        defs = graph.definitions.get(rel_path, [])
        sig_preview = ", ".join(d.signature for d in defs[:6]) if defs else "module"
        entry = f"{rel_path} [ppr={scores.get(rel_path, 0.0):.4f}]: {sig_preview}"
        entry_bytes = len(entry.encode("utf-8")) + 1
        if used_bytes + entry_bytes > budget_bytes and len(lines) > 1:
            break
        lines.append(entry)
        used_bytes += entry_bytes
    return "\n".join(lines)


def build_ppr_repomap(
    files_content: Mapping[str, str],
    *,
    seed_paths: Iterable[str] = (),
    query_symbols: Iterable[str] = (),
    token_budget: int = 1024,
) -> tuple[str, dict[str, float]]:
    """Build a token-budgeted AST repository map ranked by Personalized PageRank."""
    graph, scores, ranked_files = _rank_graph_by_ppr(
        files_content,
        seed_paths=seed_paths,
        query_symbols=query_symbols,
    )
    return _render_p0_repomap(graph, scores, ranked_files, token_budget), scores


def pack_ppr_context_tiers(
    files_content: Mapping[str, str],
    *,
    seed_paths: Iterable[str] = (),
    query_symbols: Iterable[str] = (),
    token_budget: int = 1024,
) -> dict[str, object]:
    """Pack P0 (AST RepoMap), P1 (symbol summaries), and P2 (file excerpts) within a token budget."""
    total_budget = max(64, int(token_budget))
    p0_budget = max(24, int(total_budget * 0.30))
    p1_budget = max(24, int(total_budget * 0.30))
    p2_budget = max(16, total_budget - p0_budget - p1_budget)

    graph, scores, ranked_files = _rank_graph_by_ppr(
        files_content,
        seed_paths=seed_paths,
        query_symbols=query_symbols,
    )
    p0_text = _render_p0_repomap(graph, scores, ranked_files, p0_budget)

    p1_lines = ["P1 AST Symbol Summaries"]
    p1_bytes = len(p1_lines[0].encode("utf-8"))
    p1_max_bytes = p1_budget * 4
    for rel_path in ranked_files:
        defs = graph.definitions.get(rel_path, [])
        sigs = "; ".join(f"{d.kind}:{d.signature}@L{d.line}" for d in defs[:8]) or "module"
        line = f"{rel_path} (ppr={scores.get(rel_path, 0.0):.4f}): {sigs}"
        line_bytes = len(line.encode("utf-8")) + 1
        if p1_bytes + line_bytes > p1_max_bytes and len(p1_lines) > 1:
            break
        p1_lines.append(line)
        p1_bytes += line_bytes
    p1_text = "\n".join(p1_lines)

    p2_map: dict[str, str] = {}
    p2_bytes = 0
    p2_max_bytes = p2_budget * 4
    for rel_path in ranked_files:
        raw = files_content.get(rel_path, "")
        if not raw:
            continue
        remaining = p2_max_bytes - p2_bytes
        if remaining <= 0 and p2_map:
            break
        encoded = raw.encode("utf-8")
        if len(encoded) > max(32, remaining):
            excerpt = encoded[: max(32, remaining)].decode("utf-8", errors="ignore")
        else:
            excerpt = raw
        p2_map[rel_path] = excerpt
        p2_bytes += len(excerpt.encode("utf-8"))

    used_bytes = (
        len(p0_text.encode("utf-8"))
        + len(p1_text.encode("utf-8"))
        + sum(len(v.encode("utf-8")) for v in p2_map.values())
    )
    used_tokens = min(total_budget, max(1, (used_bytes + 3) // 4))
    return {
        "p0": p0_text,
        "p1": p1_text,
        "p2": p2_map,
        "scores": scores,
        "ranked_files": ranked_files,
        "used_tokens": used_tokens,
        "token_budget": total_budget,
    }


__all__ = [
    "SymbolDef",
    "SymbolDependencyGraph",
    "build_ppr_repomap",
    "build_symbol_dependency_graph",
    "extract_file_symbols",
    "pack_ppr_context_tiers",
    "personalized_pagerank",
]
