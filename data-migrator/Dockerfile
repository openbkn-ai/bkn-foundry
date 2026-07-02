# Dockerfile for the core-data-migrator image.
#
# Build context is the monorepo root (see release-data-migrator.yml,
# context: '..'). The consolidated SQL lives at the repo root under
# migrations/<service>/<db_type>/<version>/ — which is already the exact
# layout the runtime expects under /app/repos, so it is copied in directly
# (no collect step / copy_repos.py / git fetch / PAT / network).

ARG BASE_IMAGE=ubuntu:24.04

FROM ${BASE_IMAGE}

WORKDIR /app

COPY data-migrator/server ./server

# git is required for pip to install the git-sourced RDS driver in requirements.txt.
RUN set -ex; \
    apt-get update; \
    apt-get install -y --no-install-recommends git python3-pip ca-certificates; \
    pip3 install --no-cache-dir -r server/requirements.txt --break-system-packages; \
    apt-get clean; \
    rm -rf /var/lib/apt/lists/*

RUN set -ex; \
    echo "/usr/local/lib/python3.12/dist-packages/ksycopg2" > /etc/ld.so.conf.d/ksycopg2.conf; \
    ldconfig

# Bake config + migrations. migrations/<service>/<db_type>/<version>/ maps 1:1
# onto the runtime's repos/<service>/<db_type>/<version>/ layout.
COPY data-migrator/config.monorepo.yaml /app/config.yaml
COPY migrations /app/repos

CMD ["python3", "server/data-migrator.py"]
