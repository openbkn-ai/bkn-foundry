# model-factory-base — mf-model-api / mf-model-manager 共用基础镜像。
# 从公网重建 legacy acr.aishu.cn/ad/model-factory-base:v2（AR exporter/tlogging 已从服务代码移除）。
# Python 锁 3.11：pydantic v1(~=1.10.9) 与 llmadapter 1.0.3 不支持 3.12。
FROM python:3.11-bookworm

# librdkafka: confluent-kafka 原生依赖；libgomp1: numpy/scipy/transformers 的 OpenMP 运行时。
RUN apt-get update && apt-get install -y --no-install-recommends \
      librdkafka-dev libgomp1 \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /opt/base
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# 冒烟：确认关键内部模块全部可解析
RUN python -c "import llmadapter, rdsdriver, dbutilsx.pooled_db; \
from llmadapter.llms.llm_factory import llm_factory; \
from dbutilsx.pooled_db import PooledDB, PooledDBInfo; \
print('model-factory-base OK')"
