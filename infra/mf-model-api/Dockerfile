# Base image, rebuilt from public sources and pushed to GHCR; override with a build argument if needed.
ARG BASE_IMAGE=ghcr.io/openbkn-ai/model-factory-base:v2
FROM ${BASE_IMAGE}
# Set the application working directory to /app.
WORKDIR /app
# The shared base already contains the OTel SDK and FastAPI instrumentation.
# Keep only the matching OTLP HTTP exporter service-local until the next base release.
COPY requirements-observability.txt /tmp/requirements-observability.txt
RUN pip install --no-cache-dir -r /tmp/requirements-observability.txt
# Copy the current source tree into /app.
ADD . /app

# Port.
EXPOSE 9898
# CMD
#CMD ["python3", "/app/main.py"]
ENTRYPOINT [ "uvicorn", "main:app", "--host", "0.0.0.0", "--port", "9898", "--workers", "1"]
