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

# Common packages baked into a dedicated dir, opt-in per template via
# PREINSTALL_COMMON (build.sh / CI enable it only for data-oriented templates
# like python-basic; multi-language stays lean). Like the SDK dir this survives
# dependency syncs (which wipe /opt/sandbox-venv), and it sits after the
# dependency dir on PYTHONPATH so a function's own declared dependencies still
# override these baseline versions. The dir is always created so bwrap can bind
# it even when the template opts out.
ARG USE_MIRROR=false
ARG PIP_MIRROR=mirrors.ustc.edu.cn
ARG PREINSTALL_COMMON=false
COPY images/templates/executor/common-requirements.txt /tmp/common-requirements.txt
COPY images/templates/executor/bkn-requirements.txt /tmp/bkn-requirements.txt
# The BKN SDK goes into every template, opt-in or not: sandbox_sdk (which carries
# the built-in `bkn` face) is COPY'd unconditionally above, and the scaffolds and
# generator prompts tell authors the SDK is pre-installed without naming a
# template. The data-science stack stays opt-in.
#
# Both requirement files go into ONE pip invocation rather than two. Two
# `--target` installs into the same directory corrupt it: pip skips a package
# whose directory already exists (warning only, unless --upgrade) but still
# writes the new dist-info, so the directory ends up with two dist-info entries
# for one package and the earlier pin silently loses. Resolving both files in a
# single pass keeps `httpx==0.27.2` here authoritative over the SDK's
# `httpx>=0.27`.
RUN mkdir -p /opt/sandbox-common && \
    REQS="-r /tmp/bkn-requirements.txt" && \
    if [ "$PREINSTALL_COMMON" = "true" ]; then REQS="$REQS -r /tmp/common-requirements.txt"; fi && \
    if [ "$USE_MIRROR" = "true" ]; then \
      pip install --no-cache-dir -i "https://$PIP_MIRROR/pypi/simple" --target /opt/sandbox-common $REQS; \
    else \
      pip install --no-cache-dir --target /opt/sandbox-common $REQS; \
    fi && \
    rm -f /tmp/common-requirements.txt /tmp/bkn-requirements.txt

RUN chown -R sandbox:sandbox /app /workspace /opt/sandbox-venv /opt/sandbox-sdk /opt/sandbox-common

USER sandbox

ENV PYTHONPATH=/app/.venv/lib/python3.11/site-packages:/app:/opt/sandbox-sdk:/opt/sandbox-common
ENV PATH="/app/.venv/bin:${PATH}"

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8080/health || exit 1

CMD [".venv/bin/python", "-m", "executor.interfaces.http.rest"]
