# Republishing backend images for the bkn-safe authz cutover

The released `*:0.1.0` images were built **before** the bkn-safe authz cutover
(PR #25, commit `a7af985d`). Their binaries still call the retired ISF
`authorization-private` service, so on a fresh deploy:

- `bkn-backend` / `vega-backend` BKN push + `FilterResources` fail
  (`CheckPermissionFailed` / `FilterResourcesFailed`), and `bkn-backend`
  CrashLoops once a catalog exists (startup `GetCatalogByID` → vega → ISF).
- `mf-model-api` public authz routes 403 (hits `authorization-private`).

**The source is already correct** — every service applies the `AUTHZ_PROVIDER`
switch in-tree (vega/bkn `main.go` → `MaybeShadow`; agent-factory
`chttpinject/authz.go`; operator-integration `NewAuthorization` → `selectAuthz`;
mf-model-api `permission_manager.py`). The fix is to **rebuild + republish the
images from current source** and ensure the deploy env sets the switch.

## 1. Build + push images (needs `docker login ghcr.io`)

```bash
REG=ghcr.io/openbkn-ai
TAG=0.1.1            # bump (recommended) or reuse 0.1.0 to overwrite in place
GOPROXY_URL=https://goproxy.cn,direct          # drop outside CN
BUILD_IMAGE=docker.m.daocloud.io/library/golang:1.25.10   # or golang:1.25.10
BASE_UBUNTU=docker.m.daocloud.io/library/ubuntu:24.04      # or ubuntu:24.04

# --- bkn-backend (Go, CGO) — plain Dockerfile build ---
( cd adp/bkn/bkn-backend && docker build -f docker/Dockerfile \
    --build-arg BUILD_IMAGE="$BUILD_IMAGE" --build-arg BASE_IMAGE="$BASE_UBUNTU" \
    --build-arg GOPROXY_URL="$GOPROXY_URL" --build-arg SERVER_VERSION="$TAG" \
    -t "$REG/bkn-backend:$TAG" . )

# --- vega-backend (Go, CGO) — its prod stage pip-installs sqlglot (slow/blocked
#     in CN), so build the binary then OVERLAY onto the existing base image ---
( cd adp/vega/vega-backend
  docker run --rm -v "$PWD/server:/go/src" -w /go/src \
    -e GOPROXY="$GOPROXY_URL" -e CGO_ENABLED=1 -e GOFLAGS=-mod=mod "$BUILD_IMAGE" \
    bash -c "go mod download && go build -ldflags '-s -w -X vega-backend/version.ServerVersion=$TAG' -o ./bin/vega-backend-server"
  printf 'FROM %s/vega-backend:0.1.0\nCOPY bin/vega-backend-server /opt/vega-backend/vega-backend-server\n' "$REG" > /tmp/vega.Dockerfile
  docker build -f /tmp/vega.Dockerfile -t "$REG/vega-backend:$TAG" server )

# --- agent-backend (agent-factory) and mf-model-api ---
# Rebuild from their own docker/Dockerfile the same way (Go: plain build;
# mf-model-api is Python/pyinstaller — overlay onto :0.1.0 if the build is slow).

docker push "$REG/bkn-backend:$TAG"
docker push "$REG/vega-backend:$TAG"
# docker push "$REG/agent-backend:$TAG"
# docker push "$REG/mf-model-api:$TAG"
```

> Air-gapped / k3s nodes: instead of `docker push`, import straight into the
> node's containerd: `docker save "$REG/<svc>:$TAG" | ctr -n k8s.io images import -`.

## 2. Point the deployments at the new tag

If you bumped to `0.1.1`, set the image tag in the chart values (or directly):

```bash
NS=openbkn
for s in bkn-backend vega-backend; do
  kubectl -n "$NS" set image deploy/$s $s=ghcr.io/openbkn-ai/$s:0.1.1
  kubectl -n "$NS" rollout status deploy/$s --timeout=200s
done
# verify each pod logs:  [authz] provider=bkn-safe (authoritative) at http://bkn-safe:3000
```

## 3. Fill the missing deploy env (config, not code)

`mf-model-api` ships without the authz switch env, so its public routes still
hit ISF. Add it (chart values `env:` or a quick patch):

```bash
kubectl -n openbkn set env deploy/mf-model-api \
  AUTHZ_PROVIDER=bkn-safe BKN_SAFE_URL=http://bkn-safe:3000
kubectl -n openbkn rollout status deploy/mf-model-api --timeout=180s
```

> **Confirmed on 118: the env alone is NOT enough for `mf-model-api`.** Its
> `0.1.0` image predates the `permission_manager.py` bkn-safe switch, so the old
> binary ignores `AUTHZ_PROVIDER` and still calls `authorization-private`
> (`Name or service not known` → public routes 403). `mf-model-api` must be
> **rebuilt** (step 1) for the env to take effect. This only affects its *public*
> authz routes; the private S2S embedding route bkn-backend uses has no authz and
> works regardless (BKN vectorization succeeds without this fix).

(`ontology-query` similarly lacks the env; add the same pair after rebuilding if
its authz path is exercised.)

## 4. Drop the temporary 118 overlay tags

On 118 the live `bkn-backend` / `vega-backend` run a locally-built
`:0.1.0-authzfix` overlay. Once `:0.1.1` (or rebuilt `:0.1.0`) is published,
re-point those deployments at the official tag so nothing depends on a
node-local image.
