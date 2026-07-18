# Platform Redis image

Companion image for the bundled sentinel-mode redis chart
(`deploy/charts/redis-1.11.2.tgz`, installed by `deploy.sh redis install`).

## Why this exists

The chart expects two scripts **inside the image** (nothing mounts them):

- `/config-init.sh` — initContainer: renders `redis.conf` / `sentinel.conf` and
  `users.acl` (root / monitor / sentinel users) onto the data PVC.
- `/liveness-check.sh` — liveness/readiness probe helper.

The original image (`swr.cn-east-3.myhuaweicloud.com/openbkn-ai/redis:1.11.2-20251029.2.169ac3c0`)
shipped **amd64 only** — its `…-arm64` tag also contains an amd64 build — so
Redis CrashLoops with `exec format error` on arm64 clusters (Apple Silicon
kind, arm64 k3s). This directory rebuilds the identical image multi-arch.

## Provenance

- Base: official `redis:7.4.6` (Debian bookworm) — matches the original's base
  layers (`REDIS_VERSION=7.4.6`).
- `config-init.sh` / `liveness-check.sh`: extracted byte-identical from the
  original amd64 image above.

## Build & publish

CI (`.github/workflows/release-deploy-redis.yml`) builds
`linux/amd64,linux/arm64` on any push touching this directory and publishes to
GHCR plus the Huawei SWR mirror as
`redis:1.11.2-<branch>.<committime>.sha<short>` (base `1.11.2` = chart
version, not the repo VERSION).

Consumed via `REDIS_IMAGE_TAG` (default in `deploy/scripts/lib/common.sh`).

Local one-off:

```bash
docker buildx build --platform linux/amd64,linux/arm64 \
  -t <registry>/openbkn-ai/redis:<tag> --push deploy/images/redis
```
