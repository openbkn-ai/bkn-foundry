# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

########################### Stage 0 ########################
ARG BUILD_IMAGE=golang:1.26.9
ARG BASE_IMAGE=ubuntu:24.04

FROM ${BUILD_IMAGE} AS builder

COPY adp/bkn/bkn-project/server /workspace/adp/bkn/bkn-project/server

ARG SERVER_VERSION=0.1.0
ARG GOPROXY_URL="https://proxy.golang.org,direct"

ENV GOPROXY=${GOPROXY_URL}

RUN set -ex; \
    cd /workspace/adp/bkn/bkn-project/server; \
    go mod download -x; \
    export flags="-X 'github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/version.ServerVersion=${SERVER_VERSION}'"; \
    CGO_ENABLED=1 go build -ldflags "-s -w $flags" -o ./bin/bkn-project-server;

########################### Stage 1 ########################
# Copy working directory to the actual release docker images
FROM ${BASE_IMAGE} AS prod

ARG UID=1001
ARG GID=1001

RUN groupadd -r -g $GID openbkn \
    && useradd -r -u $UID -g $GID openbkn

COPY --from=builder --chown=$UID:$GID /workspace/adp/bkn/bkn-project/server/bin /opt/bkn-project

ADD adp/bkn/bkn-project/server/config/bkn-project-config.yaml /opt/bkn-project/config/

# The git store lives on a persistent volume mounted here.
RUN mkdir -p /data/git && chown $UID:$GID /data/git

# Set the working directory.
WORKDIR /opt/bkn-project/

# Change user
USER $UID

# Expose port
EXPOSE 13016

# Set the startup command.
ENTRYPOINT [ "./bkn-project-server" ]
