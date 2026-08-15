from pathlib import Path


def test_l3_acceptance_helper_is_read_only() -> None:
    script = Path("scripts/verify-claude-mem-l3.ps1").read_text(encoding="utf-8")

    assert "/health" in script
    assert "/api/search" in script
    assert "/api/observations/batch" in script
    assert "/api/sessions/" not in script
    assert "sqlite" not in script.lower()
    assert "chroma" not in script.lower()


def test_l3_receipt_excludes_content_and_secrets() -> None:
    script = Path("scripts/verify-claude-mem-l3.ps1").read_text(encoding="utf-8")

    assert "worker_version" in script
    assert "elapsed_ms" in script
    assert "citation_ids" in script
    assert "observation_text" not in script


def test_l3_acceptance_helper_uses_bounded_probe_timeouts() -> None:
    script = Path("scripts/verify-claude-mem-l3.ps1").read_text(encoding="utf-8")

    assert script.count("-TimeoutSec 2") == 3
