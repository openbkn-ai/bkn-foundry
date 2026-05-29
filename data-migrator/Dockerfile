# Multi-stage Dockerfile for the kweaver-core-data-migrator image.
#
# Build context is the monorepo root (see release-data-migrator.yml,
# context: '..'). Stage 1 collects each service's migrations/ tree into
# /workspace/repos via copy_repos.py — no git fetch, no PAT, no network.
# Stage 2 is the runtime image with the python runtime, server code,
# baked config (renamed to /app/config.yaml), and the pre-collected repos.
#
# docker/Dockerfile.base + docker/Dockerfile.example are kept untouched
# for the internal/China build flow that uses aliyun mirrors.

ARG BASE_IMAGE=ubuntu:24.04

# ─── stage 1: collect migrations ──────────────────────────────────────────
FROM ${BASE_IMAGE} AS builder

RUN set -ex; \
    apt-get update; \
    apt-get install -y --no-install-recommends python3 python3-yaml ca-certificates; \
    rm -rf /var/lib/apt/lists/*

WORKDIR /workspace
COPY . /workspace

RUN set -ex; \
    python3 data-migrator/copy_repos.py /workspace \
      -o /workspace/data-migrator/repos \
      -c /workspace/data-migrator/config.monorepo.yaml

# ─── stage 2: runtime ─────────────────────────────────────────────────────
FROM ${BASE_IMAGE}

WORKDIR /app

COPY data-migrator/server ./server

RUN set -ex; \
    apt-get update; \
    apt-get install -y --no-install-recommends git python3-pip ca-certificates; \
    pip3 install --no-cache-dir -r server/requirements.txt --break-system-packages; \
    apt-get clean; \
    rm -rf /var/lib/apt/lists/*

RUN set -ex; \
    echo "/usr/local/lib/python3.12/dist-packages/ksycopg2" > /etc/ld.so.conf.d/ksycopg2.conf; \
    ldconfig

# Bake config + pre-collected migrations.
COPY --from=builder /workspace/data-migrator/config.monorepo.yaml /app/config.yaml
COPY --from=builder /workspace/data-migrator/repos /app/repos

CMD ["python3", "server/data-migrator.py"]
