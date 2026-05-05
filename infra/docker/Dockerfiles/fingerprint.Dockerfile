FROM python:3.11-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
        ffmpeg libsndfile1 ca-certificates \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY fingerprint/pyproject.toml ./
RUN pip install --no-cache-dir -e .
COPY fingerprint/ ./
ENV PYTHONUNBUFFERED=1
ENTRYPOINT ["python", "-m", "fingerprint.main"]
