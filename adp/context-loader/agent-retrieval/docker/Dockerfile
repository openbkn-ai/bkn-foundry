# Multi-stage build: compile Go in builder (the original separate Azure pipeline compile stage is now inline); prod keeps only the binary.
########################### Stage 0: builder ########################
ARG BUILD_IMAGE=golang:1.25.14
ARG baseImage=ubuntu:22.04

FROM ${BUILD_IMAGE} AS builder

ARG GOPROXY_URL="https://proxy.golang.org,direct"
ENV GOPROXY=${GOPROXY_URL}

WORKDIR /src
COPY . .

RUN set -ex; \
    go mod download; \
    go build -ldflags '-s -w -checklinkname=0' -o /out/agent-retrieval ./server/main.go

########################### Stage 1: prod ########################
FROM ${baseImage} AS prod

WORKDIR /app/agent-retrieval

COPY --from=builder /out/agent-retrieval ./agent-retrieval

# Create a non-root user.
RUN useradd -m -r -s /bin/bash aishu \
    && chown -R aishu:aishu /app/agent-retrieval

# Switch to the non-root user.
USER aishu

CMD ["./agent-retrieval"]
