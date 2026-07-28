FROM python:3.11-slim

ENV PIP_NO_CACHE_DIR=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1

WORKDIR /app

COPY deployments/embedding/requirements.txt /tmp/requirements.txt
RUN pip install --upgrade pip && pip install -r /tmp/requirements.txt

COPY scripts/embedding_server.py /app/scripts/embedding_server.py

EXPOSE 8009

CMD ["python", "-m", "uvicorn", "embedding_server:app", "--app-dir", "scripts", "--host", "0.0.0.0", "--port", "8009"]
