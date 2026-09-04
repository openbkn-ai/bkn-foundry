# Copyright 2026 openbkn.ai
# Copyright The kweaver.ai Authors.
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.

########################### Stage 0 ########################
ARG BUILD_IMAGE=golang:1.25.14
ARG PYTHON_BASE_IMAGE=ghcr.io/openbkn-ai/vega-backend-python-base:sha-c757948463031d6d

FROM ${BUILD_IMAGE} AS builder

COPY ./server /go/src

ARG SERVER_VERSION=0.1.0
ARG GOPROXY_URL="https://proxy.golang.org,direct"

ENV GOPROXY=${GOPROXY_URL}

RUN set -ex; \
    cd /go/src; \
    go mod download -x; \
    export flags="-X 'vega-backend/version.ServerVersion=${SERVER_VERSION}'"; \
    CGO_ENABLED=1 go build -ldflags "-s -w $flags" -o ./bin/vega-backend-server;

########################### Stage 1 ########################
# Copy working directory to the actual release docker images
FROM ${PYTHON_BASE_IMAGE} AS prod

ARG UID=1001
ARG GID=1001

COPY --from=builder --chown=$UID:$GID /go/src/bin /opt/vega-backend

ADD server/config/vega-backend-config.yaml /opt/vega-backend/config/
ADD server/locale /opt/vega-backend/locale

# 指定工作目录
WORKDIR /opt/vega-backend/

# Change user
USER $UID

# Expose port
EXPOSE 13014

# 指定启动命令
ENTRYPOINT [ "./vega-backend-server" ]
