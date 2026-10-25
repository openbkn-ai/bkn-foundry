# Dev image for bkn-safe: wraps a prebuilt linux binary (cross-compiled with
# CGO_ENABLED=0). Not the production build — just the dev-stack runner.
#
# Its Docker build context is bkn-safe/. Locale resources are compiled into
# the binary, as in the production image.
FROM alpine:3.20
RUN apk add --no-cache ca-certificates curl
WORKDIR /app
COPY dev/bkn-safe /app/bkn-safe
EXPOSE 3000
ENTRYPOINT ["/app/bkn-safe"]
