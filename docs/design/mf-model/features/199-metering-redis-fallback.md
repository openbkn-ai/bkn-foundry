# Token 计量双后端：优先 Kafka，无 Kafka 回退 Redis Stream

- Issue: [#199](https://github.com/openbkn-ai/bkn-foundry/issues/199)（Epic [#195](https://github.com/openbkn-ai/bkn-foundry/issues/195)）
- 分支: `feature/199-metering-redis-fallback`
- 涉及服务: `infra/mf-model-api`、`infra/mf-model-manager`

## 1. 背景

token 计量链路现状：

```
mf-model-api / mf-model-manager（每次模型调用）
    └─ model_audit_controller.add_llm_model_call_log
         └─ kafka_client.produce_async → topic tenant_a.dip.model_manager.quota_data
                                              │
mf-model-manager 独立消费子进程（kafka_consumer_process.py）
    └─ KafkaStreamsProcessor：消费 → 内存聚合（按 model_id+user_id+status）
         → 每 300s 批量 INSERT ON DUPLICATE KEY → ModelUsedAuditInfo
```

问题：

1. Kafka 是该链路唯一传输，不可用时消息**静默丢弃**（`produce_async` 队列满丢弃、异常吞掉），无兜底 → minimal 部署（无 Kafka）计量完全失效。
2. `ConnectUtil.py` 模块底部 `kafka_client = MyKafkaClient()` 在 **import 时**创建 AdminClient 并 `list_topics(timeout=10)`——无 Kafka 环境下每个进程启动都阻塞 10s 并刷错误日志。

## 2. 目标 / 非目标

**目标**

- 配置了 Kafka → 走 Kafka，行为与现状完全一致。
- 未配置 Kafka → 走 Redis Stream，计量数据正常聚合落库。
- 无 Kafka 环境下服务启动不再有 10s 阻塞与错误日志。

**非目标**

- 不改聚合口径、落库表结构、topic/stream 命名（`tenant_a` 前缀的多租户治理是独立问题）。
- 不追求 exactly-once（现状 Kafka 路径为 at-least-once + 落库幂等，Redis 路径保持同级语义）。
- 不动 helm 里 KAFKA* 的条件注入（属 #201 部署分档）。

## 3. 设计

### 3.1 后端选择

新增环境变量 `METERING_BACKEND=auto|kafka|redis`，默认 `auto`：

- `auto`：**原始环境变量** `KAFKAHOST` 已设置 → `kafka`，否则 `redis`。
  （必须查原始 env，不能查解析后的 config——`KAFKAHOSTDEFAULT` 是写死的开发 IP，永远非空。）
- 显式 `kafka` / `redis`：不做探活，按配置执行（确定性，避免启动竞态）。

解析函数 `resolve_metering_backend()` 放在 `app/core/config.py`（零依赖，避免与 ConnectUtil 循环导入）。两服务同步添加。

### 3.2 生产侧（两服务相同改法）

新增 `app/utils/metering_producer.py`：

```
async def produce_metering_record(value: bytes, key: bytes) -> bool
```

- backend=kafka：委托现有 `kafka_client.produce_async`（不动）。
- backend=redis：`XADD <stream> MAXLEN ~ <maxlen> {key, value}`，stream 名沿用 topic 名；
  连接复用现有 `RedisClient.connect_redis_async(db, "write")`（三种集群模式现成），惰性建连并缓存。
- 失败语义与现状一致：返回 False / 记日志，**不阻塞模型调用**。

`model_audit_controller.py` 改为调用该抽象；`ConnectUtil.py` 底部改为条件实例化：

```python
kafka_client = MyKafkaClient() if resolve_metering_backend() == "kafka" else None
```

`kafka_shutdown.py`（api）对 `None` 直接跳过。

配置项（两服务 `config.py`）：

| env | 默认 | 说明 |
| --- | --- | --- |
| `METERING_BACKEND` | `auto` | auto / kafka / redis |
| `METERING_REDIS_DB` | `1` | stream 所在 redis db |
| `METERING_STREAM_MAXLEN` | `100000` | XADD MAXLEN ~，防无消费者时膨胀 |

### 3.3 消费侧（mf-model-manager）

聚合逻辑从 `kafka_streams_processor.py` 抽出为传输无关的 `app/utils/quota_aggregator.py`：

- `QuotaAggregator`：`add_record(data)`（含计价：quota_config_cache_tree 查价、billing_type 分支）、
  定时线程（300s）、`_process_aggregated_data()` 批量落库、`stop()`。逻辑逐行搬移，不改行为。
- `KafkaStreamsProcessor` 保留消费循环 + 基于 topic/partition/offset 的幂等去重，聚合委托给 aggregator。

新增 `app/utils/redis_streams_processor.py`：

- 消费组沿用 `quota_data_group_new`，consumer name = `<hostname>-<pid>`。
- 启动：`XGROUP CREATE ... MKSTREAM`（BUSYGROUP 忽略）→ 先以 `id=0` 清自己的 pending（进程重启不丢未 ACK）→ `XREADGROUP > COUNT 500 BLOCK 200ms` 主循环。
- 每条消息 `aggregator.add_record` 后批量 `XACK`（at-least-once；落库侧 INSERT ON DUPLICATE KEY 幂等兜底）。
- 每 300s `XAUTOCLAIM min-idle 10min`，接管崩溃实例的 pending。
- 去重键 = stream entry ID（替代 kafka 的 topic_partition_offset）。

`kafka_consumer_process.py` 泛化为计量消费进程入口：按 `resolve_metering_backend()` 启动对应 processor；`main.py` 的子进程管理不变。

### 3.4 helm

两服务 chart：configmap 增加 `METERING_BACKEND: {{ .Values.metering.backend | default "auto" }}`，deployment 注入该 env；values 增加 `metering.backend: auto`。KAFKA* 注入保持现状（#201 处理条件化）。

## 4. 语义对比

| | Kafka 路径（现状） | Redis 路径（新增） |
| --- | --- | --- |
| 传输 | topic，3 分区 | stream，MAXLEN ~100k |
| 消费 | 消费组 auto-commit 5s | 消费组 XACK（批量），XAUTOCLAIM 兜底 |
| 投递语义 | at-least-once | at-least-once |
| 生产失败 | 丢弃 + warn | 丢弃 + warn |
| 落库幂等 | INSERT ON DUPLICATE KEY | 同 |
| 无消费者堆积 | retention 策略 | MAXLEN 上限截断 |

## 5. 测试计划

- 单测（model-factory-base:v2 镜像内）：
  - `resolve_metering_backend()` env 组合矩阵。
  - redis 生产侧：mock async client 断言 XADD 参数（stream/maxlen/字段）。
  - `QuotaAggregator`：add_record 计价/聚合正确性（mock quota_config_cache_tree）、flush 分批与异常路径——即现有 kafka 路径的回归。
  - redis 消费侧：mock sync client,断言 group 创建、pending 清理、ACK 批次。
- 端到端（VM）：`METERING_BACKEND=redis` 下模型调用 → stream → 聚合 → `ModelUsedAuditInfo` 落库；kafka 档回归。

## 6. 风险

- ConnectUtil 两服务是漂移副本,改动需逐文件核对而非复制粘贴。
- `quota_config_cache_tree` 对未知 model_id 的行为保持原样（异常由消费循环捕获），不在本次修改范围。
- redis db=1 与现有缓存共库,stream 有 MAXLEN 上限,空间风险可控;如需隔离可调 `METERING_REDIS_DB`。
