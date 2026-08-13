from __future__ import annotations

from pathlib import Path


def test_reranker_uses_scoped_host_proxy_and_internal_no_proxy() -> None:
    compose = (Path(__file__).parents[1] / "docker-compose.yml").read_text(
        encoding="utf-8"
    )
    reranker = compose.split("\n  reranker:\n", 1)[1].split("\n  phoenix:\n", 1)[0]

    assert 'HTTP_PROXY: "http://host.docker.internal:7890"' in reranker
    assert 'HTTPS_PROXY: "http://host.docker.internal:7890"' in reranker
    assert 'NO_PROXY: "localhost,127.0.0.1,host.docker.internal,codeagent-network"' in reranker
