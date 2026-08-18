# model-factory-base — shared base image for mf-model-api and mf-model-manager.
# Rebuilds the legacy acr.aishu.cn/ad/model-factory-base:v2 from public sources; AR exporter/tlogging has been removed from service code.
# Pin Python to 3.11 because pydantic v1 (~=1.10.9) and llmadapter 1.0.3 do not support 3.12.
FROM python:3.11-bookworm

# librdkafka: native dependency for confluent-kafka; libgomp1: OpenMP runtime for numpy/scipy/transformers.
RUN apt-get update && apt-get install -y --no-install-recommends \
      librdkafka-dev libgomp1 \
 && rm -rf /var/lib/apt/lists/*

# Force libc getaddrinfo to prefer IPv4. On clusters where pods land with
# IPv6 enabled in their netns (k3s + flannel default — host-level IPv6
# disable does not propagate), the default precedence makes glibc try AAAA
# first and wait ~10s for that path to time out before falling back to A.
# This bites every short-timeout client (Kafka admin, Redis dial, DB
# connect retry budgets, requests.get with low timeout). Bumping the
# ::ffff:0:0/96 precedence to the top reorders the candidate list so
# getaddrinfo returns A records first and the IPv6 attempt is only made
# when A fails. Idempotent across rebuilds.
RUN printf 'precedence ::ffff:0:0/96  100\n' >> /etc/gai.conf

WORKDIR /opt/base
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Smoke check: confirm key internal modules can be resolved.
RUN python -c "import llmadapter, rdsdriver, dbutilsx.pooled_db; \
from llmadapter.llms.llm_factory import llm_factory; \
from dbutilsx.pooled_db import PooledDB, PooledDBInfo; \
print('model-factory-base OK')"
