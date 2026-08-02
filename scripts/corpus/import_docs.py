"""Corpus import via the RAG server upload pipeline (real ingestion).

One-shot script used during corpus bring-up: uploads each document through
the standard check -> chunk -> merge flow, which triggers the Kafka pipeline
(parse -> chunk -> embed -> index) exactly like a user upload. Idempotent:
files already merged (FastUpload true) are skipped.

Usage:
    python scripts/corpus/import_docs.py \
        --server http://127.0.0.1:8081 --token-file /tmp/corpus-token.txt \
        --staging /tmp/corpus-pins --source go [--limit 5]
"""

from __future__ import annotations

import argparse
import hashlib
import json
import sys
from pathlib import Path

import requests

CHUNK_SIZE = 5 * 1024 * 1024


def md5_of(data: bytes) -> str:
    return hashlib.md5(data).hexdigest()


def headers(token: str) -> dict[str, str]:
    return {"Authorization": f"Bearer {token}"}


def fast_upload(server: str, token: str, file_md5: str) -> bool:
    resp = requests.post(
        f"{server}/api/v1/upload/fast-upload",
        headers=headers(token),
        json={"md5": file_md5},
        timeout=30,
    )
    resp.raise_for_status()
    return bool(resp.json().get("data"))


def upload_one(server: str, token: str, path: Path, file_name: str, org_tag: str = "", is_public: bool = True) -> None:
    data = path.read_bytes()
    file_md5 = md5_of(data)

    if fast_upload(server, token, file_md5):
        print(f"  skip (already imported): {file_name}")
        return

    # check
    resp = requests.post(
        f"{server}/api/v1/upload/check",
        headers=headers(token),
        json={"md5": file_md5, "fileName": file_name},
        timeout=30,
    )
    resp.raise_for_status()

    total_chunks = max(1, (len(data) + CHUNK_SIZE - 1) // CHUNK_SIZE)
    for idx in range(total_chunks):
        chunk = data[idx * CHUNK_SIZE : (idx + 1) * CHUNK_SIZE]
        files = {"file": (f"{file_md5}-{idx}", chunk, "application/octet-stream")}
        form = {
            "fileMd5": file_md5,
            "fileName": file_name,
            "totalSize": str(len(data)),
            "chunkIndex": str(idx),
            "chunkMd5": md5_of(chunk),
            "orgTag": org_tag,
            "isPublic": "true" if is_public else "false",
        }
        resp = requests.post(
            f"{server}/api/v1/upload/chunk",
            headers=headers(token),
            files=files,
            data=form,
            timeout=120,
        )
        resp.raise_for_status()

    resp = requests.post(
        f"{server}/api/v1/upload/merge",
        headers=headers(token),
        json={"md5": file_md5, "fileName": file_name},
        timeout=120,
    )
    resp.raise_for_status()
    print(f"  imported: {file_name} ({len(data)} bytes, {total_chunks} chunk(s))")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--server", default="http://127.0.0.1:8081")
    parser.add_argument("--token-file", required=True)
    parser.add_argument("--staging", required=True)
    parser.add_argument("--source", required=True)
    parser.add_argument("--limit", type=int, default=0, help="0 = all")
    parser.add_argument("--ext", nargs="*", default=["md", "rst", "html", "txt", "sgml", "xml", "adoc"])
    args = parser.parse_args(argv)

    token = Path(args.token_file).read_text(encoding="utf-8").strip()
    source_dir = Path(args.staging) / args.source
    if not source_dir.is_dir():
        print(f"source dir not found: {source_dir}", file=sys.stderr)
        return 1

    files = []
    for ext in args.ext:
        files.extend(source_dir.rglob(f"*.{ext}"))
    files = sorted(files)

    # Apply manifest include paths (mirror loader rules for the source).
    from eval.datasets.loader import load_dataset_manifest  # noqa: F401  (not used; keep simple)

    print(f"source {args.source}: {len(files)} candidate files")
    imported = 0
    for path in files:
        if args.limit and imported >= args.limit:
            break
        rel = path.relative_to(source_dir).as_posix()
        try:
            upload_one(args.server, token, path, f"{args.source}/{rel}")
            imported += 1
        except Exception as exc:  # noqa: BLE001
            print(f"  FAILED {rel}: {exc}", file=sys.stderr)
    print(f"done: {imported} imported")
    return 0


if __name__ == "__main__":
    sys.exit(main())
