这是一个用于部署 OpenTelemetry Collector Contrib 的 Helm Chart，当前默认能力如下：

- 以 **Deployment 模式**在 Kubernetes 部署 Collector；
- 内置 **OTLP In / OpenSearch Out**（接收 OTLP、写入 OpenSearch）；
- 通过 GitHub Actions 将 chart 以 OCI 形式发布到 **GHCR**。

---

## 1. 项目信息

Chart 名称：`otelcol-contrib`

推荐 GHCR 包名（OCI Chart）：

```text
ghcr.io/<github_org_or_user>/charts/otelcol-contrib
```

当前版本：

- Chart version: `0.1.0`
- App version: `0.148.0`

---

## 2. 项目结构

```text
.
├── charts/
│   └── otelcol-contrib/
│       ├── Chart.yaml
│       ├── values.yaml
│       └── templates/
│           ├── _helpers.tpl
│           ├── configmap.yaml
│           ├── deployment.yaml
│           ├── networkpolicy.yaml
│           ├── prometheusrule.yaml
│           ├── service.yaml
│           ├── serviceaccount.yaml
│           └── NOTES.txt
├── .github/
│   └── workflows/
│       ├── chart-ci.yaml
│       └── chart-release.yaml
└── scripts/
    ├── create-nodeport-service.sh
    ├── test_4c8g_baseline.sh
    └── validate_collector_config.sh
```

---

## 3. 当前能力

### Deployment 模式

`values.yaml` 中通过 `mode: deployment` 控制，当前仅支持 deployment。

### OTLP In / OpenSearch Out

默认配置：

- Receiver: `otlp`（gRPC: `4317` / HTTP: `4318`）
- Exporter: `opensearch`
- Pipelines: `traces` / `logs`

可通过 `values.yaml` 的 `opensearchExporter` 段配置 OpenSearch exporter，也可直接覆盖 `config` 段自定义 collector 配置。

默认 collector 镜像：

```text
docker.io/otel/opentelemetry-collector-contrib:0.148.0
```

默认会渲染以下 exporter 结构：

```yaml
exporters:
  opensearch:
    http:
      endpoint: http://opensearch-cluster-master.observability.svc.cluster.local:9200
      tls:
        insecure: true
    dataset: default
    namespace: namespace
    bulk_action: create
    sending_queue:
      enabled: true
      num_consumers: 4
      queue_size: 512
    retry_on_failure:
      enabled: true
      initial_interval: 1s
      max_interval: 10s
      max_elapsed_time: 5m
    mapping:
      mode: ss4o
```

如果开启 `opensearchExporter.auth.enabled=true`，chart 会自动增加 `basicauth/client` extension，并通过 `opensearch.http.auth.authenticator` 将其绑定到 exporter。

注意：官方 `opensearchexporter` 当前支持 `traces` 和 `logs`，默认不为 `metrics` 创建 OpenSearch 导出 pipeline。

### 4C8G 最小部署基线

默认单副本 Collector 限制为 `500m CPU / 768MiB`，适配 OpenBKN `4C8G` 最小环境：

- `memory_limiter`：`512MiB` 上限、`128MiB` 突发余量，位于 `batch` 之前；容器保留 `256MiB` 余量，避免硬限与 cgroup 上限重合；
- `batch`：512 条目标批次、1024 条硬上限、1 秒刷新；
- OpenSearch 发送队列：512 批、4 个消费者；
- OpenSearch 失败重试：指数退避，最大单次间隔 10 秒、最长 5 分钟；
- 健康端点：`:13133/`；Collector 指标：`:8888/metrics`。

容器内存上限高于 `memory_limiter`，为 Go 运行时和非堆内存保留余量，避免 cgroup OOM 在 Collector 拒收遥测前终止进程。

已有部署若通过 `helm upgrade --reuse-values` 升级，需显式更新上述参数；Helm 会保留旧的覆盖值，不会以新的 Chart 默认值替换它们。

`monitoring.prometheusRule.enabled=true` 时渲染队列接近容量、日志导出失败和遥测拒绝告警。`networkPolicy.enabled=true` 时只允许带 `openbkn.ai/otel-producer=true` 标签的同命名空间 Pod 写入 OTLP，并允许指定监控命名空间抓取指标。两项默认关闭，启用前必须核对目标集群的 CRD、命名空间和工作负载标签。

使用 `scripts/run_otlp_capacity_probe.sh` 做本地 Collector 验收。脚本默认只发送 5 条无敏感内容的 OTLP 日志；显式设置 `RATE_PER_SECOND=100 DURATION_SECONDS=300` 可执行稳态门，设置为 `300 / 300` 可执行突发门。脚本按 Collector 指标校验接收、导出、拒收和失败计数，并在结束时清理临时端口转发。

---

## 4. Pipeline 设计

### CI

> **TODO**: 尚未创建独立的 CI workflow（如 `ci-otelcol-chart.yml`）来在 PR 阶段执行 `helm lint` 和 `helm template` 校验。目前仅有发布流程。

### 发布（`.github/workflows/release-otelcol-chart.yaml`）

触发：

- 手动触发（`workflow_dispatch`）
- push（`bkn-trace/otelcol-contribute-chart/**`、workflow 自身或 `reusable-*.yml` 变更）

步骤：

1. 登录 GHCR（使用 `GITHUB_TOKEN`）
2. `helm package` 生成 `.tgz`
3. `helm push` 到 `oci://ghcr.io/<owner>/charts`

---

## 5. 发布 Chart

### 步骤 1：确认仓库权限

仓库 Actions 需要 `packages: write`（workflow 已配置）。

### 步骤 2：打 tag 并推送

```bash
git tag chart-v<version>
git push origin chart-v<version>
```

### 步骤 3：验证 chart

发布成功后可拉取：

```bash
helm pull oci://ghcr.io/<github_org_or_user>/charts/otelcol-contrib --version <version>
```

---

## 6. 本地快速验证

```bash
helm lint charts/otelcol-contrib
helm template otelcol-contrib charts/otelcol-contrib
bash scripts/test_4c8g_baseline.sh charts/otelcol-contrib
./scripts/validate_collector_config.sh charts/otelcol-contrib
```

最后一条命令使用 chart 锁定版本的真实 Collector 二进制执行离线 `validate`，需要本机 Docker 可用。

安装示例：

```bash
helm upgrade --install otelcol-contrib charts/otelcol-contrib -n observability --create-namespace
```

如果你在本地开发阶段使用 Docker 启动的 OpenSearch，可覆盖为宿主机地址，例如：

```bash
helm upgrade --install otelcol-contrib charts/otelcol-contrib \
  -n observability \
  --create-namespace \
  --set opensearchExporter.http.endpoint=http://192.168.139.3:9200
```

覆盖 OpenSearch endpoint 示例：

```bash
helm upgrade --install otelcol-contrib charts/otelcol-contrib \
  -n observability \
  --create-namespace \
  --set opensearchExporter.http.endpoint=http://opensearch-cluster-master.observability.svc.cluster.local:9200
```

开启 Basic Auth 示例：

```bash
helm upgrade --install otelcol-contrib charts/otelcol-contrib \
  -n observability \
  --create-namespace \
  --set opensearchExporter.auth.enabled=true \
  --set opensearchExporter.auth.username=admin \
  --set opensearchExporter.auth.password=admin
```

## 7. 使用 telemetrygen 生成测试 Trace

本地开发阶段可以使用 `telemetrygen` 生成测试 trace，验证以下链路是否打通：

- `telemetrygen -> otelcol-contrib -> OpenSearch`

这一节参考 OpenTelemetry Collector 官方 Quick Start 中关于 `telemetrygen` 的安装和发送方式，并结合当前 chart 的部署方式调整为 NodePort 验证流程：

- 官方文档：<https://opentelemetry.io/docs/collector/quick-start/>

### 7.1 安装 telemetrygen

官方 Quick Start 要求本机安装 Go，并设置 `GOBIN`。如果 `GOBIN` 未设置，可先执行：

```bash
export GOBIN=${GOBIN:-$(go env GOPATH)/bin}
```

安装 `telemetrygen`：

```bash
go install github.com/open-telemetry/opentelemetry-collector-contrib/cmd/telemetrygen@latest
```

验证是否安装成功：

```bash
$GOBIN/telemetrygen traces --help
```

### 7.2 暴露 Collector NodePort

如果当前 chart 安装的是默认 `ClusterIP` Service，可先创建一个 NodePort Service 供本机访问：

```bash
./scripts/create-nodeport-service.sh otelcol-contrib observability
```

默认端口：

- OTLP gRPC: `30417`
- OTLP HTTP: `30418`

假设 Kubernetes 节点 IP 为 `192.168.139.66`，则 OTLP gRPC 地址为：

```text
192.168.139.66:30417
```

### 7.3 发送测试 Trace

使用 OTLP/gRPC 发送 3 条 trace：

```bash
$GOBIN/telemetrygen traces \
  --otlp-insecure \
  --otlp-endpoint 192.168.139.66:30417 \
  --traces 3
```

如果命令执行成功，通常会看到类似输出：

```text
INFO traces/worker.go:180 traces generated {"worker": 0, "traces": 3}
```

### 7.4 查看 Collector 日志

查看 collector 是否已接收到 trace：

```bash
kubectl -n observability logs deploy/otelcol-contrib -f
```

如果启用了 `debugExporter.enabled=true`，日志中会直接打印 span 内容，便于确认 receiver 已收到数据。

### 7.5 查看 OpenSearch 是否已写入

当前 chart 默认将 trace 写入 OpenSearch `ss4o` 风格索引。可通过以下命令验证：

```bash
curl 'http://<opensearch-host>:9200/_cat/indices?v'
```

如果写入成功，通常会看到类似索引：

```text
ss4o_traces-default-namespace
```

也可以进一步查询文档：

```bash
curl 'http://<opensearch-host>:9200/ss4o_traces-default-namespace/_search?pretty'
```

### 7.6 常见问题

- `rpc error: code = Unavailable desc = no children to pick from`
  这通常不是 `telemetrygen` 参数错误，而是 collector 的 exporter 目标不可达，需检查 OpenSearch endpoint 是否可访问。

- 修改 Helm values 后日志仍显示旧 exporter
  当前 chart 默认不会因 ConfigMap 更新自动触发 Pod 滚动重启。修改配置后请手动执行：

```bash
kubectl -n observability rollout restart deploy/otelcol-contrib
kubectl -n observability rollout status deploy/otelcol-contrib
```
