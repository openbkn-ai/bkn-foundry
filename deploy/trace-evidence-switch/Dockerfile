FROM golang:1.25.10 AS builder

WORKDIR /workspace/bkn-trace/agent-observability
COPY bkn-trace/agent-observability/go.mod bkn-trace/agent-observability/go.sum ./
RUN go mod download
COPY bkn-trace/agent-observability/ ./
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -o /out/trace-evidence-release-controller ./cmd/trace-evidence-release-controller

FROM alpine/helm:3.17.3
USER root
RUN addgroup -S openbkn && adduser -S -G openbkn -u 65532 openbkn \
    && mkdir -p /charts/adp/bkn/bkn-backend/helm /charts/adp/bkn/ontology-query/helm \
      /charts/adp/vega/vega-backend/helm /charts/adp/context-loader/agent-retrieval/helm \
      /var/lib/openbkn/trace-evidence \
    && chown -R openbkn:openbkn /var/lib/openbkn
COPY --from=builder /out/trace-evidence-release-controller /usr/local/bin/trace-evidence-release-controller
COPY adp/bkn/bkn-backend/helm/bkn-backend /charts/adp/bkn/bkn-backend/helm/bkn-backend
COPY adp/bkn/ontology-query/helm/ontology-query /charts/adp/bkn/ontology-query/helm/ontology-query
COPY adp/vega/vega-backend/helm/vega-backend /charts/adp/vega/vega-backend/helm/vega-backend
COPY adp/context-loader/agent-retrieval/helm/agent-retrieval /charts/adp/context-loader/agent-retrieval/helm/agent-retrieval
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/trace-evidence-release-controller"]
