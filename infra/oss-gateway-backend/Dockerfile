# Global build arguments must be declared before FROM.
ARG GO_BUILD_IMAGE=golang:1.25.10
ARG BASE_IMAGE=ubuntu:24.04

# Build stage.
FROM ${GO_BUILD_IMAGE} AS builder
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o oss-gateway main.go

# Runtime stage; redeclare the build argument after FROM.
ARG BASE_IMAGE
FROM ${BASE_IMAGE}

# Install runtime dependencies with apt-get on Ubuntu.
RUN apt-get update && apt-get install -y ca-certificates tzdata && rm -rf /var/lib/apt/lists/*

WORKDIR /root/

COPY --from=builder /app/oss-gateway .
COPY --from=builder /app/locale ./locale

EXPOSE 8080

CMD ["./oss-gateway"]
