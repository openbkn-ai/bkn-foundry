# Sandbox Template Executor Image
# Final versioned executor/template image built from a stable runtime base.

ARG BASE_IMAGE=ghcr.io/openbkn-ai/sandbox-python-executor-base:v1
ARG USE_MIRROR=false
ARG PIP_MIRROR=mirrors.ustc.edu.cn

FROM ${BASE_IMAGE} AS builder

ARG USE_MIRROR=false
ARG PIP_MIRROR=mirrors.ustc.edu.cn

USER root
WORKDIR /app

RUN if [ "$USE_MIRROR" = "true" ]; then \
      pip install --no-cache-dir -i "https://$PIP_MIRROR/pypi/simple" uv; \
    else \
      pip install --no-cache-dir uv; \
    fi

COPY runtime/executor/pyproject.toml runtime/executor/uv.lock ./

RUN if [ "$USE_MIRROR" = "true" ]; then \
      export UV_INDEX_URL="https://$PIP_MIRROR/pypi/simple"; \
    fi && \
    uv sync --frozen --no-dev

FROM ${BASE_IMAGE}

ARG TEMPLATE_NAME=sandbox-template
ARG TEMPLATE_DESCRIPTION="Sandbox executor template"
ARG TEMPLATE_VERSION=latest

LABEL maintainer="Sandbox Team"
LABEL template.name="${TEMPLATE_NAME}"
LABEL template.description="${TEMPLATE_DESCRIPTION}"
LABEL template.version="${TEMPLATE_VERSION}"

USER root
WORKDIR /app

COPY --from=builder /app/.venv ./.venv
COPY runtime/executor/ ./executor/
# sandbox_sdk: user-facing runtime (@tool decorator, event unpacking, schema
# inference). Ships with the image rather than being pip-installed per session,
# so `import sandbox_sdk` works offline and stays version-locked to the wrapper.
#
# It lives in /opt rather than /app because user code has to import it: bwrap
# does not mount /app, and the dependency directory is emptied before every
# dependency sync.
COPY runtime/sandbox_sdk/ /opt/sandbox-sdk/sandbox_sdk/

RUN chown -R sandbox:sandbox /app /workspace /opt/sandbox-venv /opt/sandbox-sdk

USER sandbox

ENV PYTHONPATH=/app/.venv/lib/python3.11/site-packages:/app:/opt/sandbox-sdk
ENV PATH="/app/.venv/bin:${PATH}"

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8080/health || exit 1

CMD [".venv/bin/python", "-m", "executor.interfaces.http.rest"]
