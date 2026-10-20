"""Select full CI unless the complete Git diff is confirmed explanatory Markdown."""

from __future__ import annotations

import os
from pathlib import Path

from eval.release_gate import documentation_only_change


def main() -> None:
    docs_only = documentation_only_change(
        Path.cwd(), os.environ.get("CI_BASE_SHA", ""), os.environ.get("CI_HEAD_SHA", ""),
    )
    print("full=false" if docs_only else "full=true")


if __name__ == "__main__":
    main()
