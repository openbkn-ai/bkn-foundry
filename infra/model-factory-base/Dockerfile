# model-factory-base — mf-model-api / mf-model-manager 共用基础镜像。
# 从公网重建 legacy acr.aishu.cn/ad/model-factory-base:v2（AR exporter/tlogging 已从服务代码移除）。
# Python 锁 3.11：pydantic v1(~=1.10.9) 与 llmadapter 1.0.3 不支持 3.12。
FROM python:3.11-bookworm

# librdkafka: confluent-kafka 原生依赖；libgomp1: numpy/scipy/transformers 的 OpenMP 运行时。
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

# 冒烟：确认关键内部模块全部可解析
RUN python -c "import llmadapter, rdsdriver, dbutilsx.pooled_db; \
from llmadapter.llms.llm_factory import llm_factory; \
from dbutilsx.pooled_db import PooledDB, PooledDBInfo; \
print('model-factory-base OK')"
