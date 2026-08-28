"""MinerU subprocess runtime contract.

These tests pin the runtime behaviour that broke the Python MinerU path on
Windows: a running event loop that refuses to create subprocess transports
(``_WindowsSelectorEventLoop``, which is what uvicorn selects for
``--reload``/``--workers>1`` and what ``modal``/``phoenix`` install
process-wide through ``WindowsSelectorEventLoopPolicy``).

The MinerU launcher must therefore not depend on the running loop's
subprocess support. Every test here drives a loop whose
``_make_subprocess_transport`` raises ``NotImplementedError`` so the
regression is guarded on POSIX CI as well, and every test executes a *real*
child process.
"""

from __future__ import annotations

import asyncio
import json
import os
import stat
import subprocess
import sys
import time
from pathlib import Path
from types import SimpleNamespace

import pytest

from orchestrator.rag.ingestion import _parse_pdf_with_mineru, _run_mineru

MARKER = "MINERU_SUBPROCESS_RUNTIME_MARKER"


def test_mineru_command_accepts_json_argv_prefix() -> None:
    command = json.dumps(
        [sys.executable, "-c", "import sys; print(sys.argv[1])"]
    )

    async def invoke() -> tuple[bytes, bytes]:
        return await _run_mineru(command, MARKER, timeout_seconds=30)

    stdout, stderr = asyncio.run(invoke())

    assert stdout.decode().strip() == MARKER
    assert stderr == b""


class _NoSubprocessLoop(asyncio.SelectorEventLoop):
    """A real event loop that refuses subprocess transports.

    On Windows ``asyncio.SelectorEventLoop`` already behaves this way; the
    explicit override reproduces the same constraint on POSIX so the test is
    meaningful on every platform.
    """

    async def _make_subprocess_transport(self, *args, **kwargs):  # type: ignore[override]
        raise NotImplementedError


def _run_on_loop_without_subprocess_support(factory):
    loop = _NoSubprocessLoop()
    try:
        return loop.run_until_complete(factory(loop))
    finally:
        loop.close()


def _sleep_child_code() -> str:
    return (
        "import pathlib, sys, time\n"
        "pathlib.Path(sys.argv[1]).write_text('started', encoding='utf-8')\n"
        "time.sleep(30)\n"
        "pathlib.Path(sys.argv[2]).write_text('finished', encoding='utf-8')\n"
    )


def _wait_for(path: Path, timeout: float = 30.0) -> bool:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if path.exists():
            return True
        time.sleep(0.05)
    return False


def test_loop_fixture_really_lacks_subprocess_support() -> None:
    """Guard the guard: the test loop must reject native asyncio subprocesses."""

    async def probe(_loop):
        with pytest.raises(NotImplementedError):
            await asyncio.create_subprocess_exec(sys.executable, "-c", "pass")

    _run_on_loop_without_subprocess_support(probe)


def test_run_mineru_executes_real_subprocess_without_loop_support() -> None:
    async def scenario(_loop):
        return await _run_mineru(
            sys.executable,
            "-c",
            f"print('{MARKER}')",
            timeout_seconds=60,
        )

    stdout, _stderr = _run_on_loop_without_subprocess_support(scenario)
    assert MARKER in stdout.decode("utf-8", errors="replace")


def test_run_mineru_reports_nonzero_exit_with_detail() -> None:
    async def scenario(_loop):
        with pytest.raises(RuntimeError) as excinfo:
            await _run_mineru(
                sys.executable,
                "-c",
                "import sys; sys.stderr.write('stub ocr failure'); sys.exit(3)",
                timeout_seconds=60,
            )
        return str(excinfo.value)

    message = _run_on_loop_without_subprocess_support(scenario)
    assert "3" in message
    assert "stub ocr failure" in message


def test_run_mineru_timeout_kills_the_child(tmp_path: Path) -> None:
    started = tmp_path / "started.txt"
    finished = tmp_path / "finished.txt"

    async def scenario(_loop):
        began = time.monotonic()
        with pytest.raises(TimeoutError):
            await _run_mineru(
                sys.executable,
                "-c",
                _sleep_child_code(),
                str(started),
                str(finished),
                timeout_seconds=1,
            )
        return time.monotonic() - began

    elapsed = _run_on_loop_without_subprocess_support(scenario)

    assert started.exists(), "child process never started"
    assert elapsed < 20, f"timeout did not interrupt the child (took {elapsed:.1f}s)"
    time.sleep(1.5)
    assert not finished.exists(), "child survived the timeout instead of being killed"


def test_run_mineru_cancellation_kills_the_child(tmp_path: Path) -> None:
    started = tmp_path / "started.txt"
    finished = tmp_path / "finished.txt"

    async def scenario(_loop):
        task = asyncio.ensure_future(
            _run_mineru(
                sys.executable,
                "-c",
                _sleep_child_code(),
                str(started),
                str(finished),
                timeout_seconds=600,
            )
        )
        deadline = time.monotonic() + 30
        while not started.exists():
            if task.done():
                # Surface the real failure instead of hanging forever.
                task.result()
                raise AssertionError("runner returned before the child started")
            if time.monotonic() > deadline:
                task.cancel()
                raise AssertionError("child process never started within 30s")
            await asyncio.sleep(0.05)
        task.cancel()
        with pytest.raises(asyncio.CancelledError):
            await task

    _run_on_loop_without_subprocess_support(scenario)

    assert started.exists()
    time.sleep(1.5)
    assert not finished.exists(), "cancelling the caller left an orphan MinerU process"


def test_run_mineru_forces_localhost_proxy_bypass() -> None:
    async def scenario(_loop):
        stdout, _stderr = await _run_mineru(
            sys.executable,
            "-c",
            "import os; print(os.environ.get('NO_PROXY', ''))",
            timeout_seconds=60,
        )
        return stdout.decode("utf-8", errors="replace")

    no_proxy = _run_on_loop_without_subprocess_support(scenario)
    for required in ("127.0.0.1", "localhost", "::1"):
        assert required in no_proxy


def _write_stub_mineru_cli(tmp_path: Path) -> str:
    """Create an executable that mimics the MinerU CLI output contract.

    This stubs the *CLI surface* so the production PDF path can be exercised
    end to end on a loop without subprocess support. Real MinerU OCR is
    verified separately by tests/integration/mineru_pdf_e2e.py.
    """

    script = tmp_path / "stub_mineru_cli.py"
    script.write_text(
        "import json, sys\n"
        "from pathlib import Path\n"
        "args = sys.argv[1:]\n"
        "assert '-m' in args and args[args.index('-m') + 1] == 'ocr', 'explicit OCR mode required'\n"
        "output_dir = Path(args[args.index('-o') + 1]) / 'document' / 'ocr'\n"
        "output_dir.mkdir(parents=True, exist_ok=True)\n"
        "(output_dir / 'document_content_list.json').write_text(\n"
        f"    json.dumps([{{'type': 'text', 'text': '{MARKER}', 'bbox': [0, 0, 100, 20], 'page_idx': 0}}]),\n"
        "    encoding='utf-8',\n"
        ")\n"
        "(output_dir / 'document_middle.json').write_text(\n"
        "    json.dumps({'version': '3.4.4-stub', 'backend': 'pipeline'}), encoding='utf-8'\n"
        ")\n",
        encoding="utf-8",
    )

    if os.name == "nt":
        launcher = tmp_path / "stub_mineru.cmd"
        launcher.write_text(f'@echo off\r\n"{sys.executable}" "{script}" %*\r\n', encoding="utf-8")
    else:
        launcher = tmp_path / "stub_mineru.sh"
        launcher.write_text(f'#!/bin/sh\nexec "{sys.executable}" "{script}" "$@"\n', encoding="utf-8")
        launcher.chmod(launcher.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)
    return str(launcher)


def test_pdf_parse_path_survives_loop_without_subprocess_support(tmp_path: Path) -> None:
    command = _write_stub_mineru_cli(tmp_path)
    settings = SimpleNamespace(
        mineru_command=command,
        mineru_backend="pipeline",
        mineru_timeout_seconds=120,
    )
    pdf_bytes = b"%PDF-1.4\n% stub pdf body\n"

    async def scenario(_loop):
        return await _parse_pdf_with_mineru(pdf_bytes, "scan.pdf", "doc-runtime-1", settings)

    result = _run_on_loop_without_subprocess_support(scenario)

    assert MARKER in result.parsedText
    assert result.parserName == "mineru"
    assert result.parserVersion == "3.4.4-stub"


def test_grpc_aio_server_can_launch_mineru_subprocess() -> None:
    """The design map requires the fix to work under the async gRPC server.

    A real grpc.aio server is started on a loop that refuses subprocess
    transports, and its handler launches a real child process through the
    production launcher.
    """
    grpc = pytest.importorskip("grpc")

    async def scenario(_loop):
        async def handler(_request, _context):
            stdout, _stderr = await _run_mineru(
                sys.executable,
                "-c",
                f"print('{MARKER}')",
                timeout_seconds=60,
            )
            return stdout

        server = grpc.aio.server()
        server.add_generic_rpc_handlers(
            (
                grpc.method_handlers_generic_handler(
                    "mineru.Probe",
                    {
                        "Run": grpc.unary_unary_rpc_method_handler(
                            handler,
                            request_deserializer=bytes,
                            response_serializer=bytes,
                        )
                    },
                ),
            )
        )
        port = server.add_insecure_port("127.0.0.1:0")
        await server.start()
        try:
            async with grpc.aio.insecure_channel(f"127.0.0.1:{port}") as channel:
                call = channel.unary_unary(
                    "/mineru.Probe/Run",
                    request_serializer=bytes,
                    response_deserializer=bytes,
                )
                return await asyncio.wait_for(call(b"go"), timeout=120)
        finally:
            await server.stop(None)

    response = _run_on_loop_without_subprocess_support(scenario)
    assert MARKER in response.decode("utf-8", errors="replace")


def test_stub_cli_contract_matches_real_invocation(tmp_path: Path) -> None:
    """The stub asserts explicit OCR mode, so this pins the production flags."""

    command = _write_stub_mineru_cli(tmp_path)
    output_dir = tmp_path / "out"
    completed = subprocess.run(
        [command, "-p", str(tmp_path / "x.pdf"), "-o", str(output_dir), "-m", "txt", "-b", "pipeline"],
        capture_output=True,
        timeout=120,
    )
    assert completed.returncode != 0, "stub must reject non-OCR mode"
    assert b"explicit OCR mode required" in completed.stderr
    assert not output_dir.exists(), "stub must not emit artifacts when OCR mode is missing"
