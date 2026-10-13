FROM python@sha256:78387bc3881b8273120a12ebe6c1ab22b018ccc2c9adf565ae1ac9b536e184ea
LABEL codeops.docker.project="localcode"
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1
ENV SANDBOX_CONFIG_PATH=/app/codeops-runtime.toml
COPY requirements.txt wheels /prepared/
RUN python -m pip install --no-index --find-links=/prepared --require-hashes --no-cache-dir --disable-pip-version-check --requirement /prepared/requirements.txt && rm -r /prepared
WORKDIR /app
COPY opensandbox_server /app/opensandbox_server
COPY codeops-profile.json codeops-runtime.toml /app/
COPY LICENSE-OpenSandbox /usr/share/licenses/OpenSandbox/LICENSE
CMD ["python", "-m", "uvicorn", "opensandbox_server.main:app", "--host", "0.0.0.0", "--port", "8080", "--no-access-log"]
