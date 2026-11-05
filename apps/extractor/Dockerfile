FROM python:3.12.13-slim
WORKDIR /app
RUN pip install --no-cache-dir uv==0.13.6
COPY apps/extractor/pyproject.toml apps/extractor/uv.lock ./
ARG EXTRACTOR_BACKEND=gliner
RUN uv sync --frozen --no-dev --extra ${EXTRACTOR_BACKEND}
COPY apps/extractor/server.py ./server.py
ENV MEMSTATION_EXTRACTOR_HOST=0.0.0.0
EXPOSE 8092
CMD [".venv/bin/python", "server.py"]
