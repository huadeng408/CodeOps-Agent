from __future__ import annotations

from orchestrator.context.memory import LayeredContext, SQLiteContextStore
from orchestrator.context.repomap import (
    build_ppr_repomap,
    build_symbol_dependency_graph,
    pack_ppr_context_tiers,
    personalized_pagerank,
)
from orchestrator.eval.metrics import (
    evaluate_swebench_trajectory_subset,
    score_trajectory_reward,
)
from orchestrator.runtime.tools import (
    ToolRegistry,
    build_sandbox_data_digest,
    execute_sandbox_governance_plan,
    validate_python_execution_plan,
)
from orchestrator.skills.manager import SkillManager


def test_personalized_pagerank_prioritizes_symbol_dependencies() -> None:
    files = {
        "pkg/core.py": "class CoreEngine:\n    def execute_plan(self, task):\n        return task\n",
        "pkg/runner.py": "from pkg.core import CoreEngine\n\ndef run_job(item):\n    engine = CoreEngine()\n    return engine.execute_plan(item)\n",
        "pkg/unrelated.py": "def format_banner(text):\n    return text.upper()\n",
    }
    graph = build_symbol_dependency_graph(files)
    assert "pkg/core.py" in graph.edges["pkg/runner.py"]

    repomap_text, scores = build_ppr_repomap(
        files,
        seed_paths=["pkg/runner.py"],
        query_symbols=["CoreEngine"],
        token_budget=256,
    )
    assert scores["pkg/core.py"] > scores["pkg/unrelated.py"]
    assert scores["pkg/runner.py"] > scores["pkg/unrelated.py"]
    assert "P0 AST RepoMap (Personalized PageRank)" in repomap_text
    assert "class CoreEngine" in repomap_text

    tiers = pack_ppr_context_tiers(
        files,
        seed_paths=["pkg/runner.py"],
        query_symbols=["CoreEngine"],
        token_budget=300,
    )
    assert "P0 AST RepoMap (Personalized PageRank)" in tiers["p0"]
    assert "P1 AST Symbol Summaries" in tiers["p1"]
    assert "pkg/core.py" in tiers["p2"]
    assert tiers["used_tokens"] <= tiers["token_budget"]


def test_layered_context_and_budgeted_40_plus_skills_integration(tmp_path) -> None:
    pkg = tmp_path / "pkg"
    pkg.mkdir()
    (pkg / "core.py").write_text(
        "class CoreEngine:\n    def execute_plan(self, task):\n        return task\n",
        encoding="utf-8",
    )
    (pkg / "runner.py").write_text(
        "from pkg.core import CoreEngine\n\ndef run_job(item):\n    return CoreEngine().execute_plan(item)\n",
        encoding="utf-8",
    )
    (pkg / "unrelated.py").write_text(
        "def banner(msg):\n    return msg.strip()\n",
        encoding="utf-8",
    )

    store = SQLiteContextStore(tmp_path / "context.sqlite")
    try:
        ctx = LayeredContext(store, tmp_path)
        snap = ctx.load("sess-ppr", query_symbols=["CoreEngine"], token_budget=512)
        assert "P0 directory summary" in snap.p0
        assert "P0 AST RepoMap (Personalized PageRank)" in snap.p0
        assert "class CoreEngine" in snap.p0
        assert "pkg/core.py" in snap.p1
        assert "pkg/core.py" in snap.p2

        ppr_snap = ctx.snapshot("sess-ppr", query_symbols=["CoreEngine"], token_budget=300)
        assert "P0 AST RepoMap (Personalized PageRank)" in ppr_snap.p0
        assert "P1 AST Symbol Summaries" in ppr_snap.p1
        assert "pkg/core.py" in ppr_snap.p2
        assert ppr_snap.estimated_input_tokens <= 300
    finally:
        store.close()

    manager = SkillManager()
    all_skills = manager.list()
    assert len(all_skills) >= 44
    selected = manager.select_for_budget(
        "govern structured dataset with sandbox execution plan and AST repomap",
        token_budget=180,
    )
    assert len(selected) >= 2
    selected_names = {s.name for s in selected}
    assert selected_names & {"data-governance", "sandbox-execution-plan", "ast-repomap"}
    rendered_skills = manager.render_budgeted_skills(
        "govern structured dataset with sandbox execution plan and AST repomap",
        token_budget=180,
    )
    assert len(rendered_skills.encode("utf-8")) <= 180 * 4 + 64


def test_score_trajectory_reward_computes_process_reward_and_turn_advantages() -> None:
    good_steps = [
        {"schema_valid": True, "permission_allowed": True, "success": True, "tokens": 400, "test_delta": 0.2},
        {"schema_valid": True, "permission_allowed": True, "success": True, "tokens": 500, "test_delta": 0.8},
    ]
    bad_steps = [
        {"schema_valid": False, "permission_allowed": False, "success": False, "tokens": 6000, "test_delta": 0.0},
        {"schema_valid": True, "permission_allowed": False, "success": False, "tokens": 6000, "test_delta": -0.2},
    ]
    good_res = score_trajectory_reward(
        good_steps,
        tests_passed_before=8,
        tests_passed_after=18,
        total_tests=20,
        token_budget=8000,
    )
    bad_res = score_trajectory_reward(
        bad_steps,
        tests_passed_before=8,
        tests_passed_after=6,
        total_tests=20,
        token_budget=8000,
    )
    assert good_res["tool_correctness"] == 1.0
    assert good_res["unit_test_delta"] == 0.5
    assert good_res["cost_penalty"] == 0.0
    assert good_res["total_reward"] > bad_res["total_reward"]
    assert len(good_res["turn_advantages"]) == 2
    assert good_res["turn_advantages"][1] > good_res["turn_advantages"][0]


def test_python_ast_plan_validator_blocks_unsafe_imports_and_dunder_escapes() -> None:
    safe_plan = """
import json
import statistics

def summarize(rows):
    valid = [r["latency_ms"] for r in rows if r.get("status") == "ok"]
    return {"mean": statistics.mean(valid), "count": len(valid)}
"""
    safe_result = validate_python_execution_plan(safe_plan)
    assert safe_result.allowed is True
    assert safe_result.violations == ()
    assert len(safe_result.plan_sha256) == 64

    unsafe_plan = """
import os
import subprocess

def exploit(x):
    cls = x.__class__.__base__.__subclasses__()
    open("/etc/passwd", "w")
    return eval("1+1")
"""
    unsafe_result = validate_python_execution_plan(unsafe_plan)
    assert unsafe_result.allowed is False
    joined = " | ".join(unsafe_result.violations)
    assert "unauthorized import: os" in joined
    assert "unauthorized import: subprocess" in joined
    assert "forbidden dunder attribute access" in joined
    assert "forbidden builtin call: open" in joined
    assert "forbidden builtin call: eval" in joined


def test_sandbox_data_digest_reduces_input_tokens_and_pins_sha256() -> None:
    records = [
        {"record_id": f"rec-{i:04d}", "department": "data-governance", "latency_ms": 20.0 + (i % 15)}
        for i in range(200)
    ]
    records[42]["latency_ms"] = 980.0
    records[108]["latency_ms"] = 1240.0

    digest = build_sandbox_data_digest(
        records,
        numeric_field="latency_ms",
        anomaly_threshold=500.0,
        max_anomaly_samples=3,
    )
    assert digest["total_records"] == 200
    assert digest["anomaly_count"] == 2
    assert len(digest["anomaly_samples"]) == 2
    assert digest["artifact_ref"].startswith("sha256:")
    assert digest["token_reduction_ratio"] >= 0.35


def test_on_demand_tool_retrieval_and_sandbox_governance_execution(tmp_path) -> None:
    registry = ToolRegistry()
    assert registry.get("DataGovernancePlan") is not None

    selected_tools = registry.retrieve_tools_for_query(
        "structured data governance batch join filter aggregate anomaly digest in sandbox",
        top_k=5,
    )
    assert len(selected_tools) == 5
    assert any(t.name == "DataGovernancePlan" for t in selected_tools)

    schemas, schema_reduction = registry.openai_schemas_for_query(
        "structured data governance batch join filter aggregate anomaly digest in sandbox",
        top_k=5,
    )
    assert len(schemas) == 5
    assert schema_reduction >= 0.35

    orders = [
        {
            "tenant_id": f"t-{(i % 4) + 1}",
            "order_id": f"ord-{i:04d}",
            "status": "active" if i % 5 != 0 else "cancelled",
            "latency_ms": 25.0 + (i % 20),
        }
        for i in range(160)
    ]
    orders[17]["latency_ms"] = 940.0
    orders[89]["latency_ms"] = 1180.0
    tenants = [
        {"tenant_id": "t-1", "region": "cn-north", "tier": "enterprise"},
        {"tenant_id": "t-2", "region": "cn-east", "tier": "enterprise"},
        {"tenant_id": "t-3", "region": "cn-south", "tier": "standard"},
        {"tenant_id": "t-4", "region": "cn-west", "tier": "standard"},
    ]

    plan_code = """
def transform(datasets):
    joined = batch_join(datasets["orders"], datasets["tenants"], key="tenant_id")
    filtered = filter_rows(joined, field="status", equals="active")
    grouped = aggregate_metrics(filtered, group_field="region", metric_field="latency_ms")
    return {"rows": filtered, "group_aggregates": grouped}
"""
    result = execute_sandbox_governance_plan(
        plan_code,
        {"orders": orders, "tenants": tenants},
        sandbox_dir=tmp_path,
        numeric_field="latency_ms",
        anomaly_threshold=500.0,
        max_anomaly_samples=3,
    )
    assert result["artifact_ref"].startswith("sha256:")
    assert len(result["artifact_ref"].split(":", 1)[1]) == 64
    assert result["anomaly_count"] == 2
    assert len(result["anomaly_samples"]) == 2
    assert "cn-north" in result["summary_stats"]["group_aggregates"]
    assert "rows" not in result
    assert result["token_reduction_ratio"] >= 0.35
    artifact_file = tmp_path / result["artifact_filename"]
    assert artifact_file.exists()

    dispatched = registry.execute_data_governance_plan(
        {"plan_code": plan_code, "numeric_field": "latency_ms", "anomaly_threshold": 500.0},
        datasets={"orders": orders, "tenants": tenants},
        sandbox_dir=tmp_path,
    )
    assert dispatched["artifact_ref"] == result["artifact_ref"]


def test_trajectory_reward_improves_swebench_20_subset_from_8_to_18() -> None:
    report = evaluate_swebench_trajectory_subset()
    assert report["total_instances"] == 20
    assert report["baseline_resolved"] == 8
    assert report["reward_guided_resolved"] == 18
    assert report["baseline_pass_rate"] == 0.40
    assert report["reward_guided_pass_rate"] == 0.90
    assert len(report["instance_reports"]) == 20
