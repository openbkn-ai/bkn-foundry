# Copyright 2026 openbkn.ai
# Copyright The kweaver.ai Authors.
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.

# Vega Python Runtime Base
# This image contains dependencies shared by Vega backend releases. Keep it free
# of application artifacts so it only needs rebuilding when this context changes.
ARG BASE_IMAGE=ubuntu:24.04@sha256:33ceb71981b602c1a7443a53469e4dba065f7503eab3078a2d7a57a2ab987517

FROM ${BASE_IMAGE}

ARG UID=1001
ARG GID=1001

RUN groupadd -r -g $GID openbkn \
    && useradd -r -u $UID -g $GID openbkn \
    && DEBIAN_FRONTEND=noninteractive apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends python3 python3-pip \
    && pip3 install --no-cache-dir --break-system-packages --index-url https://pypi.org/simple/ "sqlglot==30.14.0" \
    && rm -rf /var/lib/apt/lists/*
