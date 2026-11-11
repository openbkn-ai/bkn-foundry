# Dockerfile for the core-data-migrator image.
#
# Build context is the monorepo root (see release-data-migrator.yml,
# context: '..'). The consolidated SQL lives at the repo root under
# migrations/<service>/<db_type>/<version>/ — which is already the exact
# layout the runtime expects under /app/repos, so it is copied in directly
# (no collect step / copy_repos.py / git fetch / PAT / network).

# python:3.12-slim (Debian) rather than Ubuntu plus apt: the build then reaches
# only PyPI and GitHub, and keeps working when the Ubuntu mirrors do not resolve.
# Pinned to trixie so a change of the image's default Debian release is a
# deliberate edit here, not a surprise.
ARG BASE_IMAGE=python:3.12-slim-trixie

FROM ${BASE_IMAGE}

WORKDIR /app

COPY data-migrator/server ./server

# ksycopg2 (Kingbase) ships the libraries it loads next to its own module, so
# its directory goes on the loader path. The directory is asked of Python rather
# than written out, since it differs between distributions.
RUN set -ex; \
    pip install --no-cache-dir --root-user-action=ignore -r server/requirements.txt; \
    python3 -c 'import os, ksycopg2; print(os.path.dirname(ksycopg2.__file__))' > /etc/ld.so.conf.d/ksycopg2.conf; \
    ldconfig

# Bake config + migrations. migrations/<service>/<db_type>/<version>/ maps 1:1
# onto the runtime's repos/<service>/<db_type>/<version>/ layout.
COPY data-migrator/config.monorepo.yaml /app/config.yaml
COPY migrations /app/repos

CMD ["python3", "server/data-migrator.py"]
