# 多阶段：builder 内编译 Go（原 Azure pipeline 的独立编译阶段已内联），prod 仅留二进制。
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

# 创建非root用户
RUN useradd -m -r -s /bin/bash aishu \
    && chown -R aishu:aishu /app/operator-integration

# 切换到非root用户
USER aishu

CMD ["./operator-integration"]
