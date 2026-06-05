# Copyright 2026 openbkn.ai
# Licensed under the Apache License, Version 2.0.
#
# Production image for bkn-safe (ISF replacement auth service). The dev image
# (dev/Dockerfile) wraps a host-cross-compiled binary; this one builds in-image.

########################### Stage 0 — build ########################
ARG BUILD_IMAGE=golang:1.25.10
ARG BASE_IMAGE=alpine:3.20

FROM ${BUILD_IMAGE} AS builder

COPY ./server /go/src

ARG GOPROXY_URL="https://proxy.golang.org,direct"
ENV GOPROXY=${GOPROXY_URL}
# CGO disabled: proton-rds driver + glebarez (pure-Go) sqlite need no cgo, so the
# binary is static and runs on a minimal base.
RUN set -ex; \
    cd /go/src; \
    go mod download -x; \
    CGO_ENABLED=0 go build -ldflags "-s -w" -o ./bin/bkn-safe .

########################### Stage 1 — runtime ######################
FROM ${BASE_IMAGE} AS prod

ARG UID=1001
ARG GID=1001

RUN apk add --no-cache ca-certificates curl \
    && addgroup -g $GID -S bkn && adduser -u $UID -S bkn -G bkn

COPY --from=builder --chown=$UID:$GID /go/src/bin/bkn-safe /opt/bkn-safe/bkn-safe

WORKDIR /opt/bkn-safe
USER $UID

EXPOSE 3000

ENTRYPOINT ["./bkn-safe"]
