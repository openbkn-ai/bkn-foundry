# Multi-stage: Go is compiled within the builder (the independent compilation stage of the original Azure pipeline has been inlined), leaving only the binary for prod.
########################### Stage 0: builder ########################
ARG BUILD_IMAGE=golang:1.26.9
ARG baseImage=ubuntu:22.04

FROM ${BUILD_IMAGE} AS builder

ARG GOPROXY_URL="https://proxy.golang.org,direct"
ENV GOPROXY=${GOPROXY_URL}

COPY adp/execution-factory/operator-integration /workspace/adp/execution-factory/operator-integration
WORKDIR /workspace/adp/execution-factory/operator-integration

RUN set -ex; \
    go mod download; \
    go build -ldflags '-s -w -checklinkname=0' -o /out/operator-integration ./server/main.go

########################### Stage 1: prod ########################
FROM ${baseImage} AS prod

WORKDIR /app/operator-integration

COPY --from=builder /out/operator-integration ./operator-integration

# The Go binary does not include root certificates, and the base image does not include them either.
# Without them, outbound HTTPS requests fail with "certificate signed by unknown authority" (for example, querying PyPI versions).
# The bundle comes from the builder rather than apt, so the build does not depend on reaching the Ubuntu mirrors.
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

# Create non-root user.
RUN useradd -m -r -s /bin/bash openbkn \
    && chown -R openbkn:openbkn /app/operator-integration

# Switch to non-root user.
USER openbkn

CMD ["./operator-integration"]
