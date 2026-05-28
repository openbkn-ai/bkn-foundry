# Public-mirror Dockerfile for GHCR builds.
# docker/Dockerfile.base is kept for internal builds that need the
# China-region mirror rewrite; this one uses upstream sources only so
# it builds on GitHub Actions runners without aliyun rewrites.
ARG BASE_IMAGE=ubuntu:24.04
FROM ${BASE_IMAGE}

WORKDIR /app

COPY server ./server

RUN set -ex; \
  apt-get update; \
  apt-get install -y --no-install-recommends git python3-pip ca-certificates; \
  pip3 install --no-cache-dir -r server/requirements.txt --break-system-packages; \
  apt-get clean; \
  rm -rf /var/lib/apt/lists/*

RUN set -ex; \
    echo "/usr/local/lib/python3.12/dist-packages/ksycopg2" > /etc/ld.so.conf.d/ksycopg2.conf; \
    ldconfig

CMD ["python3", "server/data-migrator.py"]
