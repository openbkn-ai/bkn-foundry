# 全局 ARG：在 FROM 之前声明，才能在 FROM 中使用
ARG GO_BUILD_IMAGE=golang:1.25.10
ARG BASE_IMAGE=ubuntu:24.04

# 构建阶段
FROM ${GO_BUILD_IMAGE} AS builder
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o oss-gateway main.go

# 运行阶段 - 需要重新声明 ARG
ARG BASE_IMAGE
FROM ${BASE_IMAGE}

# Ubuntu 使用 apt-get 安装依赖
RUN apt-get update && apt-get install -y ca-certificates tzdata && rm -rf /var/lib/apt/lists/*

WORKDIR /root/

COPY --from=builder /app/oss-gateway .

EXPOSE 8080

CMD ["./oss-gateway"]
