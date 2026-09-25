# Copyright 2026 openbkn.ai
# Copyright The kweaver.ai Authors.
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.

########################### Stage 0 ########################
ARG BUILD_IMAGE=golang:1.25.14
ARG BASE_IMAGE=ubuntu:24.04

FROM ${BUILD_IMAGE} AS builder

COPY comm-go /workspace/comm-go
COPY adp/bkn/bkn-backend/server /workspace/adp/bkn/bkn-backend/server

ARG SERVER_VERSION=0.1.0
ARG GOPROXY_URL="https://proxy.golang.org,direct"

ENV GOPROXY=${GOPROXY_URL}

RUN set -ex; \
    cd /workspace/adp/bkn/bkn-backend/server; \
    go mod download -x; \
    export flags="-X 'bkn-backend/version.ServerVersion=${SERVER_VERSION}'"; \
    CGO_ENABLED=1 go build -ldflags "-s -w $flags" -o ./bin/bkn-backend-server;

########################### Stage 1 ########################
# Copy working directory to the actual release docker images
FROM ${BASE_IMAGE} AS prod

ARG UID=1001
ARG GID=1001

RUN groupadd -r -g $GID openbkn \
    && useradd -r -u $UID -g $GID openbkn

COPY --from=builder --chown=$UID:$GID /workspace/adp/bkn/bkn-backend/server/bin /opt/bkn-backend

ADD adp/bkn/bkn-backend/server/config/bkn-backend-config.yaml /opt/bkn-backend/config/
ADD adp/bkn/bkn-backend/server/locale /opt/bkn-backend/locale

# Set the working directory.
WORKDIR /opt/bkn-backend/

# Change user
USER $UID

# Expose port
EXPOSE 13014

# Set the startup command.
ENTRYPOINT [ "./bkn-backend-server" ]
