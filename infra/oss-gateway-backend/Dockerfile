# Global build arguments must be declared before FROM.
ARG GO_BUILD_IMAGE=golang:1.26.9
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

# Root certificates and time zone data come from the builder rather than apt,
# so the build does not depend on reaching the Ubuntu mirrors.
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

WORKDIR /root/

COPY --from=builder /app/oss-gateway .

EXPOSE 8080

CMD ["./oss-gateway"]
