FROM python:3.14-slim

# Security: run as non-root user
RUN groupadd -r bales && useradd -r -g bales bales

# System deps for torch (CPU only)
RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc g++ curl \
    && rm -rf /var/lib/apt/lists/*

# Install uv
COPY --from=ghcr.io/astral-sh/uv:latest /uv /usr/local/bin/uv

WORKDIR /app

# Copy dependency files first for layer caching
COPY pyproject.toml uv.lock .python-version ./

# Install from lockfile -- no network needed if lock is current
RUN uv sync --frozen --no-dev

# Copy app code
COPY . .

# Change ownership to non-root user
RUN chown -R bales:bales /app
USER bales

# Healthcheck: poll /health every 10s
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD python -c "import urllib.request; urllib.request.urlopen('http://localhost:8000/health')"

EXPOSE 8000

CMD uv run uvicorn main:app --host 0.0.0.0 --port 8000 --loop uvloop --http httptools --workers 1 --no-access-log
