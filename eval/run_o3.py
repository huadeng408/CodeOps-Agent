"""Run the fixed TechDocs O3 receipt, or perform its safe preflight.

The default command is intentionally non-mutating and does not inspect model
credentials. ``--execute`` is required before an LLM call may happen. A real
receipt is accepted only when its Phoenix-backed shared v2 assertion is PASS
and the artifact checksum verifies; otherwise this command exits 2.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import time
import urllib.error
import urllib.request
import uuid
from datetime import UTC, datetime
from pathlib import Path
from urllib.parse import urlparse

from eval.benchmarks import trace_o3
from eval.driver_headless import create_driver
from eval.harness import Budget, HarnessRun, RunArtifacts

REQUIRED_EXECUTION_ENV = (
    "LOCAL_LLM_BASE_URL",
    "LOCAL_LLM_API_KEY",
    "LOCAL_LLM_MODEL",
    "CODE_AGENT_RAG_SERVER_URL",
    "CODE_AGENT_RAG_INTERNAL_SECRET",
    "CODE_AGENT_RAG_USER_ID",
    "PHOENIX_URL",
)
DEFAULT_RERANKER_URL = "http://127.0.0.1:8008"
DEFAULT_ELASTICSEARCH_URL = "http://127.0.0.1:9200"
DEFAULT_READ_ALIAS = "knowledge_base_current"


def _blocked(reason: str) -> int:
    print(f"BLOCKED: {reason}")
    return 2


def _health(url: str, name: str) -> str | None:
    try:
        with urllib.request.urlopen(url, timeout=10) as response:
            if 200 <= response.status < 300:
                return None
            return f"{name} health returned HTTP {response.status}"
    except (OSError, urllib.error.URLError):
        return f"{name} health is unavailable"


def _json_get(url: str, name: str) -> tuple[dict[str, object] | None, str | None]:
    try:
        with urllib.request.urlopen(url, timeout=10) as response:
            if not 200 <= response.status < 300:
                return None, f"{name} returned HTTP {response.status}"
            payload = json.loads(response.read().decode("utf-8"))
    except (OSError, urllib.error.URLError, UnicodeDecodeError, json.JSONDecodeError):
        return None, f"{name} is unavailable"
    if not isinstance(payload, dict):
        return None, f"{name} returned an invalid response"
    return payload, None


def _json_post(url: str, payload: dict[str, object], name: str) -> tuple[dict[str, object] | None, str | None]:
    request = urllib.request.Request(
        url,
        data=json.dumps(payload).encode("utf-8"),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=10) as response:
            if not 200 <= response.status < 300:
                return None, f"{name} returned HTTP {response.status}"
            response_payload = json.loads(response.read().decode("utf-8"))
    except (OSError, urllib.error.URLError, UnicodeDecodeError, json.JSONDecodeError):
        return None, f"{name} is unavailable"
    if not isinstance(response_payload, dict):
        return None, f"{name} returned an invalid response"
    return response_payload, None


def _live_corpus_preflight(elasticsearch_url: str, read_alias: str) -> str | None:
    cluster, problem = _json_get(f"{elasticsearch_url}/_cluster/health", "Elasticsearch health")
    if problem:
        return problem
    if str(cluster.get("status", "")).lower() not in {"green", "yellow"}:
        return "Elasticsearch cluster is not ready"
    aliases, problem = _json_get(
        f"{elasticsearch_url}/_alias/{read_alias}", "Elasticsearch alias"
    )
    if problem:
        return problem
    targets = sorted(str(index) for index in aliases)
    if targets != [trace_o3.PHYSICAL_INDEX]:
        return "Elasticsearch alias target mismatch"
    snapshot, problem = _json_post(
        f"{elasticsearch_url}/{trace_o3.PHYSICAL_INDEX}/_search",
        {
            "size": 0,
            "track_total_hits": True,
            "aggs": {
                "corpus_generations": {
                    "terms": {"field": "corpus_generation", "size": 20}
                }
            },
        },
        "Elasticsearch corpus provenance",
    )
    if problem:
        return problem
    hits = snapshot.get("hits")
    total = hits.get("total") if isinstance(hits, dict) else None
    total_value = total.get("value") if isinstance(total, dict) else total
    if not isinstance(total_value, int) or total_value <= 0:
        return "Elasticsearch corpus is empty"
    aggregations = snapshot.get("aggregations")
    generations = aggregations.get("corpus_generations") if isinstance(aggregations, dict) else None
    buckets = generations.get("buckets") if isinstance(generations, dict) else None
    if not isinstance(buckets, list) or not any(
        isinstance(bucket, dict)
        and bucket.get("key") == trace_o3.CORPUS_GENERATION
        and isinstance(bucket.get("doc_count"), int)
        and bucket["doc_count"] > 0
        for bucket in buckets
    ):
        return "Elasticsearch corpus generation mismatch"
    return None


def _required_environment() -> str | None:
    missing = [name for name in REQUIRED_EXECUTION_ENV if not os.environ.get(name, "").strip()]
    if missing:
        # Names are safe operational diagnostics. Values must never reach output.
        return "missing required execution configuration: " + ", ".join(missing)
    try:
        if int(os.environ["CODE_AGENT_RAG_USER_ID"]) <= 0:
            raise ValueError
    except ValueError:
        return "CODE_AGENT_RAG_USER_ID must be a positive integer"
    return None


def _execution_preflight() -> str | None:
    problem = _required_environment()
    if problem:
        return problem
    rag_url = os.environ["CODE_AGENT_RAG_SERVER_URL"].rstrip("/")
    phoenix_url = os.environ["PHOENIX_URL"].rstrip("/")
    reranker_url = os.environ.get("CODE_AGENT_RERANKER_URL", DEFAULT_RERANKER_URL).rstrip("/")
    elasticsearch_url = os.environ.get(
        "CODE_AGENT_ELASTICSEARCH_URL", DEFAULT_ELASTICSEARCH_URL
    ).rstrip("/")
    read_alias = os.environ.get("CODE_AGENT_RAG_READ_ALIAS", DEFAULT_READ_ALIAS).strip()
    return (
        _health(f"{rag_url}/healthz", "RAG server")
        or _health(f"{phoenix_url}/healthz", "Phoenix")
        or _health(f"{reranker_url}/health", "Reranker")
        or _live_corpus_preflight(elasticsearch_url, read_alias)
    )


def _model_allowlist(base_url: str) -> tuple[str, ...]:
    host = (urlparse(base_url).hostname or "").strip()
    if not host or host in {"localhost", "127.0.0.1", "::1"}:
        return ()
    return (host,)


def _git_head() -> str:
    import subprocess

    result = subprocess.run(
        ["git", "rev-parse", "HEAD"], capture_output=True, text=True, check=False
    )
    return result.stdout.strip() if result.returncode == 0 else "unknown"


def _args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--execute", action="store_true", help="perform one real, authorized O3 receipt")
    parser.add_argument("--output-dir", default="eval_results/o3", help="artifact parent directory")
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = _args(sys.argv[1:] if argv is None else argv)
    pins = trace_o3.preflight()
    if not pins["ok"]:
        return _blocked("; ".join(pins["problems"]))
    if not args.execute:
        return _blocked("O3 preflight passed; re-run with --execute to authorize a real model call")
    if problem := _execution_preflight():
        return _blocked(problem)

    run_id = f"trace-o3-{uuid.uuid4().hex[:8]}"
    phoenix_url = os.environ["PHOENIX_URL"].rstrip("/")
    base_url = os.environ["LOCAL_LLM_BASE_URL"].rstrip("/")
    artifacts = RunArtifacts(run_id=run_id, root=args.output_dir)
    harness = HarnessRun(
        run_id=run_id,
        artifacts=artifacts,
        adapter=create_driver(
            model=os.environ["LOCAL_LLM_MODEL"],
            base_url=base_url,
            api_key=os.environ["LOCAL_LLM_API_KEY"],
            use_runner=True,
            strict_o3=True,
        ),
        scorer=trace_o3.score,
        network_allowlist=_model_allowlist(base_url),
        budget=Budget(wall_clock_seconds=900, max_tokens=250_000, max_cost=2.0),
        config={
            "git_sha": _git_head(),
            "model": os.environ["LOCAL_LLM_MODEL"],
            "benchmark": "trace_o3",
            "mode": "official",
            "synthetic": False,
            "trace_profile": trace_o3.TRACE_PROFILE,
            "trace_capabilities": trace_o3.TRACE_CAPABILITIES,
            "corpus_generation": trace_o3.CORPUS_GENERATION,
            "qrels_hash": trace_o3.QRELS_SHA256,
            "index_name": trace_o3.PHYSICAL_INDEX,
            "phoenix_url": phoenix_url,
            "phoenix_project": os.environ.get("PHOENIX_PROJECT", "default"),
            "phoenix_otlp_endpoint": f"{phoenix_url}/v1/traces",
            "trace_start_time": datetime.now(UTC).isoformat(),
            "start_time": datetime.now(UTC).isoformat(),
            "dataset_pin": {
                "queries_sha256": trace_o3.QUERIES_SHA256,
                "qrels_sha256": trace_o3.QRELS_SHA256,
                "scope": "non_release_dev_smoke",
            },
        },
    )
    try:
        result = harness.run(trace_o3.load_instances())
    except Exception as exc:  # noqa: BLE001 - emit a safe bounded blocker
        return _blocked(f"O3 harness failed: {type(exc).__name__}")

    assertion_path = artifacts.root / "traces" / "span-assertion.json"
    try:
        assertion = json.loads(assertion_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return _blocked("O3 harness did not produce a readable trace assertion")
    if result["summary"].get("failed", 0) or assertion.get("verdict") != "PASS":
        return _blocked("O3 run completed without a PASS shared trace contract")
    if artifacts.verify_checksums():
        return _blocked("O3 artifact checksum verification failed")
    print(f"O3 receipt: {artifacts.root}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
