# Multi-stage: Go is compiled within the builder (the independent compilation stage of the original Azure pipeline has been inlined), leaving only the binary for prod.
########################### Stage 0: builder ########################
ARG BUILD_IMAGE=golang:1.25.10
ARG baseImage=ubuntu:22.04

FROM ${BUILD_IMAGE} AS builder

ARG GOPROXY_URL="https://proxy.golang.org,direct"
ENV GOPROXY=${GOPROXY_URL}

WORKDIR /src
COPY . .

RUN set -ex; \
    go mod download; \
    go build -ldflags '-s -w -checklinkname=0' -o /out/operator-integration ./server/main.go

########################### Stage 1: prod ########################
FROM ${baseImage} AS prod

WORKDIR /app/operator-integration

COPY --from=builder /out/operator-integration ./operator-integration

# The Go binary does not come with a root certificate, nor is it included in the base image. Any outbound HTTPS requests will end with.
# "certificate signed by unknown authority" fails (e.g. querying PyPI version).
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Create non-root user.
RUN useradd -m -r -s /bin/bash aishu \
    && chown -R aishu:aishu /app/operator-integration

# Switch to non-root user.
USER aishu

CMD ["./operator-integration"]
