# Platform OpenSearch image: stock OpenSearch plus the IK Chinese analyzer.
#
# The stock opensearchproject/opensearch image ships no Chinese analyzer, so
# vega-backend's startup capability probe only ever finds `standard` and
# `english` (server/logics/local_index/local_index_manager.go). Chinese text
# then falls back to per-character tokenization, which costs recall and
# relevance. Baking analysis-ik in makes `ik_max_word` available out of the
# box — Studio's analyzer picker is driven by GET /index-capabilities, so no
# frontend change is needed.
#
# Baked in rather than installed through the chart's `plugins.installList`:
# that path wraps the plugin install into the main container command, so every
# pod start re-downloads the plugin and one network hiccup keeps OpenSearch
# down (`set -euo pipefail`). Offline installs cannot use it at all.
FROM opensearchproject/opensearch:2.19.4

# The plugin descriptor pins opensearch.version=2.19.4 and OpenSearch refuses
# to load a plugin whose version does not match the node exactly. Bump this
# and the base image tag together.
ARG IK_VERSION=2.19.4
ARG IK_SHA256=9f7a9af933698836dad2cdea6647313d38d73b9bf63a2dc58746681bca882877
ARG IK_URL=https://release.infinilabs.com/analysis-ik/stable/opensearch-analysis-ik-${IK_VERSION}.zip

USER root
RUN set -eux; \
    curl -fsSL -o /tmp/analysis-ik.zip "${IK_URL}"; \
    echo "${IK_SHA256}  /tmp/analysis-ik.zip" | sha256sum -c -; \
    chown opensearch:opensearch /tmp/analysis-ik.zip

USER opensearch
# -b accepts the plugin security policy (SocketPermission, required by IK's
# remote dictionary hot reload) instead of prompting.
RUN set -eux; \
    /usr/share/opensearch/bin/opensearch-plugin install -b file:///tmp/analysis-ik.zip; \
    rm -f /tmp/analysis-ik.zip; \
    /usr/share/opensearch/bin/opensearch-plugin list | grep -qx analysis-ik
