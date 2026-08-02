"""Release evidence package and alias drill consistency tests (plan Task 9.2).

These tests validate the static contract between the cutover PowerShell
scripts and the cutover runbook: parameter names, read-only preflight
invariants, the single-call aliases API shape, and the runbook section
structure. They never touch a live Elasticsearch/MinIO/MySQL.

Runtime validation of the scripts against a real environment is out of
scope here; see docs/releases/RAG-CUTOVER-techdocs-v2.md for the evidence
runbook.
"""

from __future__ import annotations

from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
SCRIPTS = {
    "preflight": REPO_ROOT / "scripts" / "rag" / "preflight-cutover.ps1",
    "switch": REPO_ROOT / "scripts" / "rag" / "switch-alias.ps1",
    "rollback": REPO_ROOT / "scripts" / "rag" / "rollback-alias.ps1",
}
RUNBOOK = REPO_ROOT / "docs" / "releases" / "RAG-CUTOVER-techdocs-v2.md"


def _script_text(name: str) -> str:
    path = SCRIPTS[name]
    assert path.is_file(), f"missing script: {path}"
    return path.read_text(encoding="utf-8")


def _runbook_text() -> str:
    assert RUNBOOK.is_file(), f"missing runbook: {RUNBOOK}"
    return RUNBOOK.read_text(encoding="utf-8")


def test_switch_alias_has_required_params_and_single_atomic_call() -> None:
    text = _script_text("switch")
    for param in ("Alias", "Target", "Previous"):
        # PowerShell declares parameters as variables ($Alias); the
        # hyphenated -Alias form only appears at call sites.
        assert f"${param}" in text, f"switch-alias.ps1 missing parameter ${param}"
    assert "/_aliases" in text
    # One atomic aliases API call: remove + add in the same POST body.
    assert '"remove"' in text and '"add"' in text
    # Never issues an HTTP DELETE against a physical index (Remove-Item in
    # the scripts only cleans transient temp files, never indices).
    assert "Method Delete" not in text
    assert "never removes physical indices" in text.lower()
    # Evidence output: request/response must be printed.
    assert "REQUEST" in text and "RESPONSE" in text


def test_rollback_alias_has_required_params_and_inverse_shape() -> None:
    text = _script_text("rollback")
    for param in ("Alias", "Target", "Current"):
        assert f"${param}" in text, f"rollback-alias.ps1 missing parameter ${param}"
    assert "/_aliases" in text
    assert '"remove"' in text and '"add"' in text
    # Never issues an HTTP DELETE against a physical index.
    assert "Method Delete" not in text
    assert "never removes physical indices" in text.lower()
    assert "REQUEST" in text and "RESPONSE" in text


def test_scripts_set_stop_on_error_and_exit_codes() -> None:
    for name in ("switch", "rollback"):
        text = _script_text(name)
        assert '$ErrorActionPreference = "Stop"' in text, f"{name} script lacks Stop"
        assert "exit" in text, f"{name} script lacks explicit exit codes"


def test_preflight_is_read_only() -> None:
    text = _script_text("preflight")
    # Evidence params from the plan: contamination report, mapping/model,
    # Phoenix, and configurable read-only count commands.
    for param in (
        "ContaminationReport",
        "ExpectedVectorDims",
        "EmbeddingServiceUrl",
        "PhoenixUrl",
        "MySqlCountQuery",
        "MinioCountCommand",
        "LegacyIndex",
        "V2Index",
        "ReadAlias",
    ):
        assert f"${param}" in text, f"preflight-cutover.ps1 missing parameter ${param}"
    # Strictly read-only: no HTTP verbs other than GET/HEAD, no aliases
    # mutation, and the default MySQL query is a SELECT.
    for verb in ("Method Post", "Method Put", "Method Delete"):
        assert verb not in text, f"preflight must be read-only but uses {verb}"
    assert "/_aliases" not in text, "preflight must not touch the aliases API"
    assert "SELECT" in text, "default MySQL count query must be a SELECT"
    # Graceful skip when Phoenix is unreachable must be present.
    assert "Phoenix" in text
    assert "$ErrorActionPreference = \"Stop\"" in text
    assert "exit" in text


def test_runbook_is_complete_and_self_consistent() -> None:
    text = _runbook_text()
    for name in ("preflight-cutover.ps1", "switch-alias.ps1", "rollback-alias.ps1"):
        assert name in text, f"runbook must reference {name}"
    # Section headers from the plan (1..6).
    for marker in (
        "## 1",
        "## 2",
        "## 3",
        "## 4",
        "## 5",
        "## 6",
    ):
        assert marker in text, f"runbook missing section {marker}"
    # Naming invariants shared with pkg/es/knowledge_index.go.
    for name in ("knowledge_base_current", "knowledge_base_v2_bge_m3", "knowledge_base", "1024"):
        assert name in text, f"runbook must mention {name}"
    # Old-asset cleanup must require separate approval.
    assert "批准" in text or "approval" in text


def test_runbook_parameter_names_match_scripts() -> None:
    runbook = _runbook_text()
    switch = _script_text("switch")
    rollback = _script_text("rollback")
    preflight = _script_text("preflight")
    # Every parameter the runbook documents for a script must exist in the
    # script: -Alias/-Target/-Previous on switch, -Alias/-Target/-Current on
    # rollback (declared as $Alias/$Target/... inside the script).
    for param in ("-Alias", "-Target", "-Previous"):
        if param in runbook:
            assert param[1:] in switch, f"runbook documents {param} but switch-alias.ps1 lacks it"
    for param in ("-Alias", "-Target", "-Current"):
        if param in runbook:
            assert param[1:] in rollback, f"runbook documents {param} but rollback-alias.ps1 lacks it"
    for param in ("-ContaminationReport", "-PhoenixUrl", "-EsBaseUrl"):
        if param in runbook:
            assert param[1:] in preflight or param[1:] in switch or param[1:] in rollback
