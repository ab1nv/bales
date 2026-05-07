FROM python:3.12-slim

# Security: run as non-root user
RUN groupadd -r bales && useradd -r -g bales bales

# System deps for torch (CPU only)
RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc g++ \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Copy requirements first for layer caching
COPY requirements.txt .

# Install CPU-only torch first (smaller image), then remaining deps
RUN pip install --no-cache-dir torch>=2.6.0 --index-url https://download.pytorch.org/whl/cpu \
    && pip install --no-cache-dir -r requirements.txt

# Copy application code
COPY . .

# Change ownership to non-root user
RUN chown -R bales:bales /app
USER bales

# Healthcheck: poll /health every 10s
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD python -c "import urllib.request; urllib.request.urlopen('http://localhost:8000/health')"

EXPOSE 8000

CMD ["python", "-m", "uvicorn", "main:app",
     "--host", "0.0.0.0", "--port", "8000",
     "--loop", "uvloop", "--http", "httptools",
     "--workers", "1", "--no-access-log"]
