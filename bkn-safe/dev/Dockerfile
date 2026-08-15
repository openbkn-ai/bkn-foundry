# Dev image for bkn-safe: wraps a prebuilt linux binary (cross-compiled with
# CGO_ENABLED=0). Not the production build — just the dev-stack runner.
#
# Its Docker build context is bkn-safe/ so the runtime uses the same locale
# resources as the production image, without maintaining a second copy.
FROM alpine:3.20
RUN apk add --no-cache ca-certificates curl
WORKDIR /app
COPY dev/bkn-safe /app/bkn-safe
COPY server/locale /app/locale
EXPOSE 3000
ENTRYPOINT ["/app/bkn-safe"]
