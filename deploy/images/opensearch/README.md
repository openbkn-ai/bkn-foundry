# Platform OpenSearch image

Stock OpenSearch plus the IK Chinese analyzer, installed by
`deploy.sh opensearch install`.

## Why this exists

vega-backend probes a fixed candidate list of full-text analyzers once at
startup (`standard`, `english`, `ik_max_word`, `hanlp_index`) and caches
whatever the cluster actually answers to; the result is served by
`GET /api/vega-backend/v1/index-capabilities` and drives Studio's analyzer
picker. The stock `opensearchproject/opensearch` image carries no Chinese
analyzer, so only `standard` and `english` ever show up — Chinese text is
tokenized per character, and picking `ik_max_word` fails validation with
`analyzer "ik_max_word" ... is unavailable` (HTTP 400).

This image bakes `analysis-ik` in, so `ik_max_word` is available on a fresh
install with no frontend change and no per-environment setup.

The OpenSearch chart's own `plugins.installList` is deliberately **not** used:
it wraps the plugin install into the main container command, so every pod start
re-downloads the plugin from the internet, and any failure there keeps
OpenSearch from starting at all. Offline installs cannot use it.

## Provenance

- Base: `opensearchproject/opensearch:2.19.4`.
- Plugin: `opensearch-analysis-ik-2.19.4.zip` from
  `https://release.infinilabs.com/analysis-ik/stable/`, pinned by SHA-256 in
  the Dockerfile.

The plugin descriptor pins `opensearch.version=2.19.4`. OpenSearch refuses to
load a plugin whose version does not match the node exactly, so the base image
tag and `IK_VERSION` must be bumped together.

`hanlp_index` stays in vega-backend's candidate list but is not shipped.
OpenSearch has no equivalent of IK's maintained release line for HanLP: every
implementation is a personal port of KennFalcon's Elasticsearch plugin, and
the most active one (`Canva/opensearch-hanlp-plugin`) publishes releases only
for 2.10.0 / 2.19.1 / 2.19.2 — none of which loads on a 2.19.4 node, since the
descriptor version must match exactly. Shipping it would mean building from
source against every OpenSearch bump plus distributing the HanLP dictionaries.
The probe simply drops it, same as today.

## Build & publish

CI (`.github/workflows/release-deploy-opensearch.yml`) builds
`linux/amd64,linux/arm64` on any push touching this directory and publishes to
GHCR plus the Huawei SWR mirror as
`opensearch:2.19.4-<branch>.<committime>.sha<short>` (base `2.19.4` = the
OpenSearch version, not the repo VERSION).

This image is the installer default. `deploy/scripts/lib/common.sh` pins
`OPENSEARCH_IMAGE_REPOSITORY=opensearch` and
`OPENSEARCH_IMAGE_TAG=2.19.4-main.20260818163046.shaaeb5d56`; the offline
image list in `deploy/scripts/sync-k8s-images.sh` carries the same tag. Publishing
a rebuild means bumping both, so a cluster always names the exact build it runs.

Pinning a different image for one environment:

```bash
OPENSEARCH_IMAGE_REPOSITORY=opensearch \
OPENSEARCH_IMAGE_TAG=<tag> \
  ./deploy.sh opensearch install
```

Local one-off:

```bash
docker buildx build --platform linux/amd64,linux/arm64 \
  -t <registry>/openbkn-ai/opensearch:<tag> --push deploy/images/opensearch
```

## Verifying

After the pod is up:

```bash
curl -s -XPOST "http://<opensearch>:9200/_analyze" -H 'Content-Type: application/json' \
  -d '{"analyzer":"ik_max_word","text":"知识网络"}'
```

Word-level tokens (not single characters) means the plugin loaded.

## Upgrading an existing environment

1. Point OpenSearch at this image and roll the StatefulSet.
2. **Restart vega-backend.** The analyzer capability table is probed once per
   process (`sync.Once`) and cached; without a restart the API keeps reporting
   `ik_max_word` as unavailable and Studio keeps hiding it.
3. Rebuild indexes that should use it. The analyzer is fixed at index creation
   time, so existing indexes keep their old tokenization until the object type
   is re-pushed and the build task re-run.
