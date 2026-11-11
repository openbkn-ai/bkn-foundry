# Copyright 2026 openbkn.ai
# Copyright The kweaver.ai Authors.
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.

########################### Stage 0 ########################
ARG BUILD_IMAGE=golang:1.26.9
ARG RUNTIME_IMAGE=ubuntu:24.04

FROM ${BUILD_IMAGE} AS builder

WORKDIR /go/src

COPY . .

ARG SERVER_VERSION=0.1.0
ARG GOPROXY_URL="https://proxy.golang.org,direct"

ENV GOPROXY=${GOPROXY_URL}

RUN set -ex; \
    go mod download -x; \
    export flags="-X 'github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/version.ServerVersion=${SERVER_VERSION}'"; \
    CGO_ENABLED=1 go build -ldflags "-s -w $flags" -o ./bin/vega-backend-server ./cmd/vega-backend;

########################### Stage 1 ########################
# Copy working directory to the actual release docker images
FROM ${RUNTIME_IMAGE} AS prod

ARG UID=1001
ARG GID=1001

RUN groupadd -r -g $GID openbkn \
    && useradd -r -u $UID -g $GID openbkn

# 根证书与时区数据取自 builder 镜像，不走 apt，构建不依赖能否连上 Ubuntu 软件源。
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

COPY --from=builder --chown=$UID:$GID /go/src/bin /opt/vega-backend

COPY config/vega-backend-config.yaml /opt/vega-backend/config/

# 预建滚动日志目录，确保非 root 运行用户可写。
RUN mkdir -p /opt/vega-backend/logs \
    && chown $UID:$GID /opt/vega-backend/logs \
    && chmod 0755 /opt/vega-backend/logs

# 指定工作目录
WORKDIR /opt/vega-backend/

# Change user
USER $UID

# Expose port
EXPOSE 13014

# 指定启动命令
ENTRYPOINT [ "./vega-backend-server" ]
