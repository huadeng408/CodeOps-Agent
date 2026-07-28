from __future__ import annotations

import asyncio
import json
import os
import time
from pathlib import Path

from fastapi import Depends, FastAPI, HTTPException, Request, status
from fastapi.responses import StreamingResponse

from .backend import GoBackendClient
from .config import Settings, load_settings
from .graph import build_graph
from .ingestion import IngestionService
from .memory_tasks import MemoryTaskService
from .models import (
    ChatStreamRequest,
    ChunkRequestPayload,
    EmbedRequestPayload,
    IndexRequestPayload,
    MemorySummaryRequestPayload,
    MemoryWriteRequestPayload,
    ParseRequestPayload,
    StreamEvent,
)
from .trace import configure_logging, current_trace_id, elapsed_ms, log_request, reset_trace_id, set_trace_id


def _load_env_file() -> None:
    env_path = Path(__file__).resolve().parent.parent / ".env"
    if not env_path.exists():
        return
    for line in env_path.read_text(encoding="utf-8").splitlines():
        stripped = line.strip()
        if not stripped or stripped.startswith("#") or "=" not in stripped:
            continue
        key, value = stripped.split("=", 1)
        os.environ.setdefault(key.strip(), value.strip())


_load_env_file()
configure_logging()
settings: Settings = load_settings()
backend = GoBackendClient(settings)
ingestion_service = IngestionService(settings)
memory_task_service = MemoryTaskService(settings)
graph = build_graph(settings, backend)

app = FastAPI(title="PaiSmart LangGraph Orchestrator", version="0.1.0")


@app.middleware("http")
async def trace_middleware(request: Request, call_next):
    start = time.perf_counter()
    token = set_trace_id(request.headers.get("X-Trace-ID", ""))
    trace_id = current_trace_id()
    try:
        response = await call_next(request)
    except Exception:
        log_request(
            "http_request_error",
            method=request.method,
            path=request.url.path,
            latency_ms=elapsed_ms(start),
        )
        reset_trace_id(token)
        raise

    response.headers["X-Trace-ID"] = trace_id
    log_request(
        "http_request",
        method=request.method,
        path=request.url.path,
        status_code=response.status_code,
        latency_ms=elapsed_ms(start),
    )
    reset_trace_id(token)
    return response


async def verify_internal_token(request: Request) -> None:
    expected = settings.internal_token.strip()
    if not expected:
        raise HTTPException(
            status_code=status.HTTP_503_SERVICE_UNAVAILABLE,
            detail="internal orchestrator token is not configured",
        )
    if request.headers.get("X-Internal-Token", "") != expected:
        raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail="invalid internal token")


@app.on_event("shutdown")
async def _shutdown() -> None:
    await backend.close()
    await ingestion_service.close()


@app.get("/healthz")
async def healthz() -> dict[str, str]:
    return {"status": "ok"}


@app.post("/v1/chat/stream")
async def chat_stream(payload: ChatStreamRequest, request: Request, _: None = Depends(verify_internal_token)):
    async def event_stream():
        run_task: asyncio.Task | None = None
        try:
            queue: asyncio.Queue[str] = asyncio.Queue()

            def on_chunk(text: str) -> None:
                if text:
                    queue.put_nowait(text)

            initial_state = {
                "query": payload.query,
                "user": payload.user.model_dump(mode="json"),
                "stream_callback": on_chunk,
            }

            run_task = asyncio.create_task(graph.ainvoke(initial_state))
            while True:
                if await request.is_disconnected():
                    run_task.cancel()
                    break
                if run_task.done() and queue.empty():
                    break
                try:
                    chunk = await asyncio.wait_for(queue.get(), timeout=0.25)
                except asyncio.TimeoutError:
                    continue
                yield json.dumps(StreamEvent(type="chunk", chunk=chunk).model_dump(mode="json"), ensure_ascii=False) + "\n"
            if run_task is not None:
                await run_task
        except asyncio.CancelledError:
            if run_task is not None:
                run_task.cancel()
            return
        except Exception as exc:
            if run_task is not None:
                run_task.cancel()
            error_event = StreamEvent(type="error", error=str(exc))
            yield json.dumps(error_event.model_dump(mode="json"), ensure_ascii=False) + "\n"
            return

        done_event = StreamEvent(type="done", done=True, metadata={"userId": payload.user.id, "traceId": current_trace_id()})
        yield json.dumps(done_event.model_dump(mode="json"), ensure_ascii=False) + "\n"

    return StreamingResponse(event_stream(), media_type="application/x-ndjson")


@app.post("/v1/ingestion/parse")
async def ingest_parse(payload: ParseRequestPayload, _: None = Depends(verify_internal_token)):
    result = await ingestion_service.parse(payload)
    return {"code": 200, "data": result.model_dump(mode="json"), "message": "success"}


@app.post("/v1/memory/summarize")
async def memory_summarize(payload: MemorySummaryRequestPayload, _: None = Depends(verify_internal_token)):
    result = await memory_task_service.summarize(payload)
    return {"code": 200, "data": result.model_dump(mode="json"), "message": "success"}


@app.post("/v1/memory/extract")
async def memory_extract(payload: MemoryWriteRequestPayload, _: None = Depends(verify_internal_token)):
    result = await memory_task_service.extract(payload)
    return {"code": 200, "data": result.model_dump(mode="json"), "message": "success"}


@app.post("/v1/ingestion/chunk")
async def ingest_chunk(payload: ChunkRequestPayload, _: None = Depends(verify_internal_token)):
    result = await ingestion_service.chunk(payload)
    return {"code": 200, "data": result.model_dump(mode="json"), "message": "success"}


@app.post("/v1/ingestion/embed")
async def ingest_embed(payload: EmbedRequestPayload, _: None = Depends(verify_internal_token)):
    result = await ingestion_service.embed(payload)
    return {"code": 200, "data": result.model_dump(mode="json"), "message": "success"}


@app.post("/v1/ingestion/index")
async def ingest_index(payload: IndexRequestPayload, _: None = Depends(verify_internal_token)):
    result = await ingestion_service.index(payload)
    return {"code": 200, "data": result.model_dump(mode="json"), "message": "success"}
