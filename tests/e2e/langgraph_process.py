from __future__ import annotations

import argparse
import json
import time
from pathlib import Path

from orchestrator.graph.main_graph import build_graph
from orchestrator.graph.nodes import GraphState


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--db", required=True)
    parser.add_argument("--marker", required=True)
    parser.add_argument("--resume", action="store_true")
    args = parser.parse_args()

    graph = build_graph(
        checkpoint_path=args.db,
        interrupt_after=() if args.resume else ("execute",),
    )
    try:
        state = graph.run(
            GraphState(tool_requests=[{"name": "Read"}]),
            thread_id="langgraph-process-e2e",
        )
        if not args.resume:
            Path(args.marker).write_text("checkpointed\n", encoding="utf-8")
            while True:
                time.sleep(1)
        print(
            json.dumps(
                {
                    "status": "completed",
                    "tool_rounds": state.tool_rounds,
                    "verified": state.metadata.get("verified", False),
                    "done": state.done,
                },
                sort_keys=True,
            ),
            flush=True,
        )
        return 0
    finally:
        graph.close()


if __name__ == "__main__":
    raise SystemExit(main())
