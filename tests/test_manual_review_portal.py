from pathlib import Path
import re
import subprocess


PORTAL = Path(__file__).resolve().parents[1] / "docs" / "manual-review-portal.html"


def test_portal_declares_blind_worksheet_import_and_draft_export_contract() -> None:
    html = PORTAL.read_text(encoding="utf-8")

    assert 'id="worksheet-file"' in html
    assert 'accept=".jsonl,application/jsonl"' in html
    assert "function validateWorksheetRows" in html
    assert 'id="export-text-draft"' in html
    assert "UNSUBMITTED_REVIEW_DRAFT" in html


def test_portal_embedded_javascript_has_valid_node_syntax() -> None:
    html = PORTAL.read_text(encoding="utf-8")
    match = re.search(r"<script>([\s\S]*?)</script>", html)

    assert match is not None
    result = subprocess.run(
        ["node", "--check"],
        input=match.group(1),
        text=True,
        encoding="utf-8",
        capture_output=True,
        check=False,
    )

    assert result.returncode == 0, result.stderr


def test_portal_declares_safe_source_and_holdout_exports() -> None:
    html = PORTAL.read_text(encoding="utf-8")

    assert 'id="source-declaration-form"' in html
    assert 'id="export-source-declaration"' in html
    assert 'id="holdout-intake-form"' in html
    assert 'id="export-holdout-intake"' in html
    assert "UNSUBMITTED_SOURCE_DECLARATION" in html
    assert "UNSUBMITTED_HOLDOUT_INTAKE" in html
    assert "Create holdout" not in html
