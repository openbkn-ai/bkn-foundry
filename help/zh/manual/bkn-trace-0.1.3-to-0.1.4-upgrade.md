# BKN Trace：0.1.3 升级到 0.1.4

0.1.4 不迁移 0.1.3 的 BKN Trace 历史数据，也不提供旧 Trace 双读。升级时可使用随 `bkn-foundry` 提供的一次性脚本清理旧数据。

> 此操作会永久删除指定 MariaDB 数据库中的 BKN Trace 表，以及指定 OpenSearch 中的 Trace、Evidence 与 Projection 数据。只能在已完成备份、已阻断新 Trace 写入的升级窗口执行。

## 适用范围

- 从 BKN Trace 0.1.3 升级到 0.1.4；
- 允许业务服务继续对外提供业务能力；
- 运维侧能够确保升级窗口内不再有新的 Trace、Evidence 或 Projection 写入；
- 使用专用的 `bkn_trace` 数据库，且该数据库只含 BKN Trace 表。

本工具仅支持 MariaDB 与 OpenSearch 都以 Kubernetes Service 形式部署在
`BKN_TRACE_CLEANUP_DEPENDENCY_NAMESPACE`（默认 `resource`）中的环境；配置中的
host 必须是 `<service>.<namespace>.svc` 或
`<service>.<namespace>.svc.cluster.local`。外置数据库或 OpenSearch 会被脚本拒绝，
不能将其作为本工具的目标。

脚本不会自动停掉 OTEL Collector、业务服务或第三方 Trace 生产者。必须先从流量、采集和生产者侧完成停写；脚本的两次计数快照只用于发现遗漏的写入，不能替代停写。

## 前置条件

1. 已保存 MariaDB `bkn_trace` 数据库的可恢复备份，以及下列 OpenSearch 目标的快照或备份：

   - `OPENSEARCH_TRACE_INDEX`
   - `OPENSEARCH_EVIDENCE_INDEX`
   - `BKN_TRACE_PROJECTION_INDEX` 当前指向的物理索引

2. 已停止 Trace 写入，包括 OTEL Collector 到 Trace 索引的导出、Evidence 写入和 Projection 写入。
3. 操作账号具备：读取/缩容 `agent-observability` Deployment、读取 Pod 与 EndpointSlice、在 resource namespace 的 MariaDB 和 OpenSearch Pod 内执行命令的权限。
4. 本机已安装 `kubectl`、`jq` 和 `awk`，且 `kubectl` 指向目标集群；OpenSearch Pod 中需有 `curl` 与 `sed`。
5. 当前部署的 `agent-observability` 含有下列字面环境变量：

   - `OPENSEARCH_TRACE_INDEX`
   - `OPENSEARCH_EVIDENCE_INDEX`
   - `BKN_TRACE_PROJECTION_INDEX`

脚本会拒绝含有未知表的数据库、未准备好的依赖 Pod、非预期的别名结构或无法验证的 OpenSearch 请求。

## 升级流程

以下命令中的 `<context>` 必须是 `kubectl config current-context` 的准确输出。不要为读取默认 `/root/.openbkn-ai/config.yaml` 而盲目使用 `sudo`；应优先指定与当前 kubectl 用户匹配的实际配置文件。

### 1. 记录目标并检查集群上下文

```bash
kubectl config current-context
kubectl -n <应用namespace> get deployment agent-observability
```

确认 context、namespace 和 Deployment 都是本次升级目标。

### 2. 运行只读预览

从 `bkn-foundry` 根目录运行：

```bash
BKN_TRACE_CLEANUP_CONFIG="$HOME/.openbkn-ai/config.yaml" \
bash deploy/scripts/upgrades/0.1.4/cleanup_legacy_bkn_trace_data.sh
```

预览不会缩容或删除任何数据。人工复核输出中的以下字段：

- `kubectl_context`、`application_namespace`、`deployment`；
- MariaDB service、Pod 与数据库名必须是 Trace 专用目标；
- Trace、Evidence 与 Projection 索引；
- 每张表和每个索引的计数；
- Projection alias 只能指向一个物理索引；
- 不得有未知表、认证错误、TLS 错误或 OpenSearch 5xx。

### 3. 执行确认清理

在确认备份可恢复且 Trace 写入已经停止后执行：

```bash
BKN_TRACE_CLEANUP_CONFIG="$HOME/.openbkn-ai/config.yaml" \
BKN_TRACE_CLEANUP_QUIESCE_SECONDS=15 \
bash deploy/scripts/upgrades/0.1.4/cleanup_legacy_bkn_trace_data.sh \
  --confirm \
  --expected-context '<context>' \
  --backup-confirmed \
  --writes-quiesced
```

四个确认参数缺一不可：

- `--confirm`：允许执行删除；
- `--expected-context`：必须与实时 kube context 完全一致；
- `--backup-confirmed`：操作者确认已有可恢复备份；
- `--writes-quiesced`：操作者确认外部 Trace 写入已停。

脚本将 `agent-observability` 缩容至 0，并在 `BKN_TRACE_CLEANUP_QUIESCE_SECONDS` 指定的观察窗口前后各采集一次 MariaDB/OpenSearch 快照。任一表、索引计数或 Projection alias 在窗口内变化，脚本会拒绝删除并保持服务停止。

成功后脚本**不会**恢复旧服务，而是保留 `agent-observability` 为 0 副本，避免在 0.1.4 部署前重新写入旧存储。

### 4. 部署 0.1.4 并恢复 Trace 服务

按发布包的标准升级流程部署 0.1.4。新版 Deployment 的期望副本数会恢复 `agent-observability`。确认 rollout：

```bash
kubectl -n <应用namespace> rollout status deployment/agent-observability --timeout=300s
kubectl -n <应用namespace> get deployment agent-observability
```

随后恢复 OTEL Collector 和其他 Trace 生产者，发起一条新的测试 Interaction，并验证：

- 新的 Conversation、Interaction 和 Operation 可被查询；
- Trace 与 Evidence 索引出现新文档；
- Projection alias 指向 0.1.4 的预期物理索引；
- Trace 查询接口正常返回新会话的结果。

## HTTPS 与 OpenSearch 证书

默认会验证 HTTPS 证书。若 OpenSearch Pod 内存在可信 CA 文件，可指定其 **Pod 内路径**：

```bash
BKN_TRACE_CLEANUP_OPENSEARCH_CA_FILE=/usr/share/opensearch/config/root-ca.pem \
bash deploy/scripts/upgrades/0.1.4/cleanup_legacy_bkn_trace_data.sh
```

仅在受控环境中、且明确接受证书校验关闭风险时，才能设置：

```bash
BKN_TRACE_CLEANUP_OPENSEARCH_INSECURE=true
```

CA 文件与 insecure 模式不能同时使用。对于 HTTP OpenSearch，不要设置任一 TLS 变量。

## 可选配置

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `BKN_TRACE_CLEANUP_CONFIG` | `/root/.openbkn-ai/config.yaml` | 部署生成的配置文件 |
| `BKN_TRACE_CLEANUP_DEPLOYMENT` | `agent-observability` | Trace 服务 Deployment |
| `BKN_TRACE_CLEANUP_DEPENDENCY_NAMESPACE` | `resource` | MariaDB 与 OpenSearch 所在 namespace |
| `BKN_TRACE_CLEANUP_DB_NAME` | `bkn_trace` | 仅限 Trace 专用数据库 |
| `BKN_TRACE_CLEANUP_SCALE_TIMEOUT` | `180s` | 缩容和 rollout 等待上限 |
| `BKN_TRACE_CLEANUP_QUIESCE_SECONDS` | `10` | 停写观察窗口秒数 |
| `BKN_TRACE_CLEANUP_OPENSEARCH_CA_FILE` | 空 | OpenSearch Pod 内 CA 文件路径 |
| `BKN_TRACE_CLEANUP_OPENSEARCH_INSECURE` | `false` | 显式关闭 OpenSearch TLS 校验 |

## 失败处理与恢复

- 缩容之前失败：未改变服务副本数；修正问题后重新 preview。
- 缩容之后失败：脚本保持 `agent-observability` 为 0 副本，避免旧版本继续写入。先排查失败原因、修复停写或配置问题，然后重新运行 preview。
- 如需放弃升级并恢复旧服务，可使用 preview 输出中的原始副本数：

```bash
kubectl -n <应用namespace> scale deployment/agent-observability --replicas=<原副本数>
kubectl -n <应用namespace> rollout status deployment/agent-observability --timeout=300s
```

- 已删除的 MariaDB 或 OpenSearch 数据不能由脚本回滚；只能从升级前备份恢复。
- OpenSearch 的 401、403、5xx、网络异常或 TLS 异常不是“索引已删除”。脚本会将其视为失败，不能据此继续升级。
