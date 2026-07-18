# Platform Redis image for the bundled sentinel-mode redis chart
# (deploy/charts/redis-1.11.2.tgz).
#
# The chart's initContainer runs /config-init.sh and its probes run
# /liveness-check.sh; neither is mounted from a ConfigMap, so both must be
# baked into the image. The upstream image
# (swr.cn-east-3.myhuaweicloud.com/openbkn-ai/redis:1.11.2-20251029.2.169ac3c0)
# was amd64-only, which breaks Apple Silicon / arm64 dev clusters — this
# rebuild is the same official redis base plus the same two scripts, published
# multi-arch (linux/amd64 + linux/arm64) by release-deploy-redis.yml.
FROM redis:7.4.6

COPY config-init.sh /config-init.sh
COPY liveness-check.sh /liveness-check.sh
RUN chmod +x /liveness-check.sh /config-init.sh
