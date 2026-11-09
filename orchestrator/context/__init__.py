"""Context management utilities."""

from .backends import (
    ContextStore,
    MySQLContextStore,
    RedisContextStore,
    build_context_store,
)
from .budget import BudgetStatus, TokenBudget
from .compaction import (
    CompactionRequest,
    CompactionResult,
    CompactionSummarizer,
    LLMCompactionSummarizer,
    compact_surface,
)
from .compactor import Compactor
from .gitdiff import GitDiffSnapshot, load_git_diff_context, load_git_diff_snapshot
from .memory import (
    ContextEvent,
    ContextSnapshot,
    LayeredContext,
    LongTermMemory,
    SQLiteContextStore,
)
from .repomap import (
    SymbolDef,
    SymbolDependencyGraph,
    build_ppr_repomap,
    build_symbol_dependency_graph,
    extract_file_symbols,
    pack_ppr_context_tiers,
    personalized_pagerank,
)

__all__ = [
    "BudgetStatus",
    "CompactionRequest",
    "CompactionResult",
    "CompactionSummarizer",
    "Compactor",
    "ContextEvent",
    "ContextSnapshot",
    "ContextStore",
    "GitDiffSnapshot",
    "LLMCompactionSummarizer",
    "LayeredContext",
    "LongTermMemory",
    "MySQLContextStore",
    "RedisContextStore",
    "SQLiteContextStore",
    "SymbolDef",
    "SymbolDependencyGraph",
    "TokenBudget",
    "build_context_store",
    "build_ppr_repomap",
    "build_symbol_dependency_graph",
    "compact_surface",
    "extract_file_symbols",
    "load_git_diff_context",
    "load_git_diff_snapshot",
    "pack_ppr_context_tiers",
    "personalized_pagerank",
]
