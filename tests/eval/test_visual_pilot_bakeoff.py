from __future__ import annotations

import json

from eval.scripts.visual_pilot_bakeoff import main


def test_bakeoff_marks_missing_late_and_bbox_as_not_run_or_not_applicable(tmp_path) -> None:
    qrels = tmp_path / "qrels.jsonl"
    text = tmp_path / "text.jsonl"
    visual = tmp_path / "visual.jsonl"
    report = tmp_path / "report.json"
    qrels.write_text('{"query_id":"q1","document_id":"d1","page_id":"p1"}\n', encoding="utf-8")
    text.write_text('{"query_id":"q1","document_id":"d1","page_id":"p1","score":1}\n', encoding="utf-8")
    visual.write_text('{"query_id":"q1","document_id":"d1","page_id":"p1","score":1}\n', encoding="utf-8")

    assert main(["--qrels", str(qrels), "--text", str(text), "--visual", str(visual), "--out", str(report)]) == 0

    payload = json.loads(report.read_text(encoding="utf-8"))
    assert [path["status"] for path in payload["paths"]] == ["SCORED", "SCORED", "NOT_RUN"]
    assert payload["bbox"] == {"status": "NOT_APPLICABLE", "labeled_qrels": 0}
