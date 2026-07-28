FROM python:3.11-slim

WORKDIR /app

ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1
ENV RERANKER_PORT=8008
ENV RERANKER_MODEL=BAAI/bge-reranker-base
ENV RERANKER_THREADS=4

COPY deployments/reranker/requirements.txt /app/deployments/reranker/requirements.txt
RUN pip install --no-cache-dir --upgrade pip && \
    pip install --no-cache-dir -r /app/deployments/reranker/requirements.txt

COPY scripts/reranker_server.py /app/scripts/reranker_server.py

EXPOSE 8008

CMD ["python", "-m", "uvicorn", "reranker_server:app", "--app-dir", "scripts", "--host", "0.0.0.0", "--port", "8008"]
