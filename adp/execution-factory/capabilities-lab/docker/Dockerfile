ARG BUILD_IMAGE=golang:1.25.10
ARG baseImage=ubuntu:22.04

FROM ${BUILD_IMAGE} AS builder

ARG GOPROXY_URL="https://proxy.golang.org,direct"
ENV GOPROXY=${GOPROXY_URL}
ENV GOSUMDB=off

WORKDIR /src
COPY . .

RUN set -ex; \
    go mod tidy; \
    go test ./...; \
    go build -ldflags '-s -w' -o /out/capabilities-lab ./server/main.go

FROM ${baseImage} AS prod

WORKDIR /app/capabilities-lab
COPY --from=builder /out/capabilities-lab ./capabilities-lab

RUN useradd -m -r -s /bin/bash aishu \
    && chown -R aishu:aishu /app/capabilities-lab

USER aishu

ENV PORT=9002
ENV OPERATOR_INTEGRATION_URL=http://agent-operator-integration:9000

CMD ["./capabilities-lab"]
