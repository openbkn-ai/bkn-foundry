# 构建迁移设计：Azure DevOps Pipeline → GitHub Actions

> 状态：设计稿（待评审）
> 适用范围：kowell-core monorepo 中、原 Azure DevOps Pipeline（`ref/kweaver-github-build`）所覆盖、且落在本仓库的可部署单元
> 目标读者：维护 CI 的工程师、各服务 owner
> 关联文档：[`ci-build-architecture.md`](./ci-build-architecture.md)（目标架构愿景：manifest-per-service）。本文记录的是**当前已落地实现**（per-service `release-*.yml` + `reusable-*`，产物推 GHCR）下的**具体迁移决策与验收口径**。

## 1. 背景

旧构建是一套 Azure DevOps Pipeline（`ref/kweaver-github-build 2/`）：

- 用内部 `ghh` 工具从 GitHub 拉代码（无 Webhook，**手动触发**）。
- 镜像分 `commercial`（Harbor `acr.aishu.cn`）/ `opensource`（Huawei SWR `swr.cn-east-3.myhuaweicloud.com/kweaver-ai`）两套。
- 多架构靠 `amd64` / `arm64` 两个自建 pool + `make-manifest` 合并 manifest。
- Chart 用容器内 `yq` 改写 `values.yaml`（registry/repository/tag）后 `curl` 推到 Harbor chartrepo。
- 阶段：`CodeCheck`(UT/coverage/SonarQube) → `BuildImage`(+Trivy) → `MakeManifest` → `ChartPush`。

**迁移目标（用户三要求）：**

1. 符合当前已修改的架构（per-service `release-*.yml` 调 `reusable-test`/`reusable-build`/`reusable-chart`）。
2. 需要 ARM 包。
3. 镜像与 Chart 都推 GHCR。

## 2. 范围界定

旧 pipeline 横跨多个产品，但**只有落在本仓库 `kowell-core` 的服务在迁移范围内**。判定依据：旧 pipeline 的 `SOURCE_CODE_REPO` 是否为 `kweaver-ai/kweaver-core`，以及本仓库是否存在对应 Dockerfile / Chart。

### 2.1 旧 pipeline → 本仓库服务映射

| 旧 pipeline | 本仓库路径 | 镜像 | Chart | 状态 |
|---|---|---|---|---|
| data-agent/agent-backend | `decision-agent/agent-backend` | agent-backend（统一镜像） | agent-backend | ✅ 已迁移 |
| —（trace-ai） | `trace-ai/agent-observability` | agent-observability | agent-observability | ✅ 已迁移 |
| bkn/bkn-backend | `adp/bkn/bkn-backend` | bkn-backend (go) | bkn-backend | 待迁移 |
| bkn/ontology-query | `adp/bkn/ontology-query` | ontology-query (go) | ontology-query | 待迁移 |
| context-loader/agent-retrieval | `adp/context-loader/agent-retrieval` | agent-retrieval (py) | agent-retrieval | 待迁移 |
| autoflow/dataflow | `adp/dataflow` | dataflow (py) | dataflow | 待迁移 |
| autoflow/coderunner | `adp/dataflow/coderunner` | coderunner + dataflowtools | coderunner | 待迁移 |
| autoflow/doc-convert | `adp/dataflow/doc-convert` | gotenberg + tika | doc-convert | 待迁移 |
| execution-factory/operator-integration | `adp/execution-factory/operator-integration` | operator-integration (py) | agent-operator-integration | 待迁移 |
| vega/vega-backend | `adp/vega/vega-backend` | vega-backend (go) + kafka-connect | vega-backend + kafka-connect | 待迁移 |
| studio/model-api | `infra/mf-model-api` | mf-model-api (py) | (chart) | 待迁移（依赖 base） |
| studio/model-manager | `infra/mf-model-manager` | mf-model-manager (py) | (chart) | 待迁移（依赖 base） |
| studio/oss-gateway | `infra/oss-gateway-backend` | oss-gateway (go) | (chart) | 待迁移 |
| —（deploy） | `deploy/charts/proton-mariadb` | — | proton-mariadb（chart-only） | 待迁移 |
| —（trace-ai） | `trace-ai/otelcol-contribute-chart` | — | otelcol-contrib（chart-only） | 待迁移 |

### 2.2 不在范围

- **其它仓库的产品**：studio/web 前端、data-migrator、autoflow 前端等（旧 pipeline 的 `SOURCE_CODE_REPO` 指向 `kweaver-ai/kweaver` 或独立仓库）。
- **sandbox（`infra/sandbox`）本轮延后**：多镜像（control-plane / web / runtime / 2 个 base / template）+ 2 个 chart + 自定义内部 base，复杂度高，单独 PR 处理。

## 3. 与当前架构对齐

沿用分支 `chore/ci-restructure` 已落地的三件套，不引入新编排模型：

- `reusable-test.yml`：版本解析 + `go test` / `pytest` / `helm lint`。
- `reusable-build.yml`：`docker/build-push-action` 多架构构建并推 GHCR。
- `reusable-chart.yml`：`helm package` + 推 `oci://ghcr.io/<owner>/charts`。
- 每个服务一个 `release-<svc>.yml` 调用方，`paths:` 过滤本服务目录 + reusable 文件。

**ARM（要求 2）已满足**：`reusable-build.yml` 的 `platforms` 默认 `linux/amd64,linux/arm64`，由 buildx + QEMU 一次产出多架构镜像，**无需**旧 pipeline 那套双 pool + manifest 合并。

**GHCR（要求 3）已满足**：镜像 `ghcr.io/<owner>/<name>:<version>`；Chart `oci://ghcr.io/<owner>/charts`。`<owner>` = `kowell-ai`。

## 4. 复用工作流需要的改动

| 文件 | 改动 | 原因 |
|---|---|---|
| `reusable-build.yml` | 新增 `build-args`（多行 string）、`target`（可选）入参，透传给 `docker/build-push-action` | 旧 pipeline 用 `--build-arg` 覆盖 base/编译镜像；GitHub runner 上需把内部 base 覆盖成公网镜像（见 §5） |
| `reusable-chart.yml` | `helm package` 前对 `values.yaml` 做 `__VERSION__` 占位替换为解析出的版本 | 现实现只改 `Chart.yaml` 的 `version`/`appVersion`，**不改 `values.yaml` 的镜像引用**，导致 Chart 仍指向旧仓库（见 §7） |
| `reusable-test.yml` | 不变。`chart-only` 服务用 `stack: chart-only` | 旧 Sonar/Trivy 暂不迁（按决策去掉不适用的检查），后续如需以独立 `security-`/`ci-` workflow 增补 |

## 5. 基础镜像策略

旧 pipeline 分 acr / swr 两套。**统一参考 swr（opensource）变体**；凡内部仓库（`acr.aishu.cn` / `swr.cn-east-3...`）在 GitHub 托管 runner 上不可达者，覆盖为**公网等价镜像**。不修改 Dockerfile 内部默认值（保留内部构建能力），而是由调用方通过 `build-args` 覆盖；仅当 `FROM` 写死、无 `ARG` 时才补一个可覆盖的 `ARG`。

| 服务 / 镜像 | Dockerfile 原 base | 处置 | 公网目标 |
|---|---|---|---|
| bkn-backend / ontology-query / vega-backend | `golang:1.25.10` + `ubuntu:24.04` | 默认已公网，**免改** | — |
| agent-retrieval | `acr.aishu.cn/public/ubuntu:22.04.*` | build-arg 覆盖 | `ubuntu:22.04` |
| dataflow | `acr.aishu.cn/public/ubuntu:22.04.*` | build-arg 覆盖 | `ubuntu:22.04` |
| dataflowtools | `acr.aishu.cn/public/ubuntu:22.04.*` | build-arg 覆盖 | `ubuntu:22.04` |
| operator-integration | `acr.aishu.cn/public/ubuntu:22.04.*` | build-arg 覆盖 | `ubuntu:22.04` |
| oss-gateway | `swr.../golang:1.24.11` + `swr.../ubuntu:24.04` | build-arg 覆盖 | `golang:1.24.11` + `ubuntu:24.04` |
| kafka-connect | `swr.../bitnami/kafka:3.9.0-debian-12-r10`（写死 `FROM`） | 补 `ARG BASE_IMAGE` + 覆盖 | `bitnami/kafka:3.9.0-debian-12-r10` |
| doc-convert / gotenberg | `acr.aishu.cn/dip/gotenberg:8-*` | build-arg 覆盖 | `gotenberg/gotenberg:8`（官方上游） |
| doc-convert / tika | `acr.aishu.cn/dip/tika:3.3.0.0-*` | build-arg 覆盖 | `apache/tika:3.3.0.0-full`（**版本待确认**） |
| coderunner | `acr.aishu.cn/dip/python:3.9.13-ubuntu22.04.*` | 见 §6.1（升级 3.12） | `python:3.12-bookworm` |
| mf-model-api / mf-model-manager | `acr.aishu.cn/ad/model-factory-base:v2`（自定义、无源、写死 `FROM`） | 见 §6.2（重建 base） | `ghcr.io/kowell-ai/model-factory-base:<ver>` |

> **判定依据说明**：`acr.aishu.cn/public/*` 命名空间是内部对 Docker Hub 官方镜像的代理，可直接对应官方镜像；tag 中的日期戳（如 `.20251014`）是镜像快照，公网用滚动 tag 会拉到更新补丁版（需更严可 pin digest）。`acr.aishu.cn/dip/*` 为内部自建，需逐个判断上游（gotenberg/tika/kafka 均有官方上游）。

## 6. 特殊服务决策

### 6.1 coderunner — Python 升级到 3.12

**现状**：base `dip/python:3.9.13-ubuntu22.04`。从 Dockerfile 内部路径 `/usr/local/lib/python3.9/site-packages` 与 `cp -r /usr/local /usr/local.bk` 判定，等价于**官方 `python` 镜像布局**（`/usr/local`），而非 ubuntu apt 安装的 python（`/usr/lib/python3/dist-packages`）。故公网对应 = 官方 `python` 镜像。

**决策：升级到 Python 3.12**（3.9 已 EOL 2025-10）。连带改动：

- `requirements.txt`：当前 `numpy==1.24.4`、`pandas==2.0.3` **无 3.12 wheel**，需 bump（`numpy>=1.26`、`pandas>=2.1`）。其余（tornado 6.5、RestrictedPython 8.0、pydantic 2.5.3、opencv-python 4.9、PyMuPDF 1.24.2 等）3.12 兼容。
- `Dockerfile.coderunner`：第 28–30 行的 `find /usr/local/lib/python3.9/site-packages ...` 与 `init_site_packages.sh` 中写死的 `python3.9` 路径，需改为 `python3.12`，否则清理/备份逻辑失效。
- base 改 `python:3.12-bookworm`。

> coderunner 是**代码执行器**，运行时 Python 版本对用户可见 —— 升级属产品决策，已确认采用 3.12。**升级后必须实际构建 + 跑通 coderunner 自身用例**。

### 6.2 mf-model-api / mf-model-manager — 重建 model-factory-base

**问题**：两者都 `FROM acr.aishu.cn/ad/model-factory-base:v2`，且 Dockerfile **不执行 `pip install`** —— 全部 Python 依赖烤进 base。mf-model-api 仓库内**无任何 requirements/pyproject**；mf-model-manager 有 `requirements.txt` 但不在构建期安装。base 内部不可达、仓库无源 → 必须重建。

**依赖闭包来源（实证）：**

- 全量 import 扫描（144 个 .py）确认：**代码不直接 import torch/transformers/spacy/datasets/nltk/sklearn** —— ML 重型栈非运行时直接依赖。
- 大量内部顶层模块（`llmadapter`、`exporter`、`tlogging`、`rdsdriver`、`dbutilsx`、`t_small_model`、`t_llm_model` 等）非本地目录、非公网包。
- mf-model-manager README：base 依赖 = `requirements.txt` + `llmadapter-1.0.3-py3-none-any.whl`；mf-model-api README：与 manager 共用同一 base。
- **关键发现**：`llmadapter==1.0.3` **公开发布在 PyPI**（`py3-none-any`，`requires_python <4.0,>=3.8.1`，PyPI 仅构建 3.9–3.11）。它把上述内部顶层模块作为**自带模块**打包，故 README 只需装它一个。→ **无需 vendor wheel，公网 `pip install` 即可。**

`llmadapter==1.0.3` 的 `requires_dist`：

```
PyYAML>=5.4.1 ; SQLAlchemy<3,>=1.4 ; dataclasses-json<0.6.0,>=0.5.7 ; gptcache>=0.1.7
langchainplus-sdk>=0.0.9 ; openai<1,>=0 ; pandas<3.0.0,>=2.0.1 ; pydantic<2,>=1
redis<5,>=4 ; requests<3,>=2 ; spacy<4,>=3 ; tenacity<9.0.0,>=8.1.0
tiktoken<0.4.0,>=0.3.2 ; python>=3.9
```

**base 重建配方**（新增 `infra/model-factory-base/`，构建后推 `ghcr.io/kowell-ai/model-factory-base:v2`）：

```dockerfile
FROM python:3.11-bookworm
RUN apt-get update && apt-get install -y --no-install-recommends \
      librdkafka-dev libgomp1 && rm -rf /var/lib/apt/lists/*
WORKDIR /opt/base
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt \
 && pip install --no-cache-dir llmadapter==1.0.3 \
 && pip install --no-cache-dir confluent-kafka aiohttp sse-starlette \
      opentelemetry-sdk opentelemetry-api
# 验证: python -c "import llmadapter,exporter,rdsdriver,dbutilsx,tlogging"
```

两个 service Dockerfile 补 `ARG BASE_IMAGE=ghcr.io/kowell-ai/model-factory-base:v2` 并 `FROM ${BASE_IMAGE}`。

**约束与待验证（build 时实跑确认）：**

1. **Python 锁 3.11**：`pydantic~=1.10.9`（v1，1.10.9 不支持 3.12）+ llmadapter PyPI 仅构建到 3.11。**不可升 3.12。**
2. **tiktoken 冲突** ⚠️：llmadapter 要 `<0.4.0`，manager `requirements.txt` 钉 `tiktoken==0.5.1`。pip 会冲突，需实测定夺（放宽到满足 llmadapter，或确认 0.5.1 可用）。
3. **requirements 漏声明、但代码实际 import**：`confluent_kafka`（需 `librdkafka` 系统库）、`aiohttp`、`sse_starlette`、`opentelemetry-*` —— 已在配方中补；最终清单以 build 跑通为准。
4. spacy 是否需下载语言模型（如 `en_core_web_sm`）待确认。

## 7. Chart 镜像引用标准化

**问题**：各 Chart 的 `values.yaml` 写死内部仓库（`registry: acr.aishu.cn`、`repository: dip/...`、`tag: tag`/`__VERSION__`/历史值）。旧 pipeline 在 `ChartPush` 阶段用 `yq` 改写这些字段；当前 `reusable-chart.yml` **不做此改写**，故即便 Chart 推到 GHCR，部署时仍拉旧仓库镜像。**已合并的 `decision-agent`、`agent-observability` 同样存在此隐患。**

**决策：标准化 + `__VERSION__` 占位（统一口径）**

- 每个 Chart 的 `values.yaml` 镜像块改为：

  ```yaml
  image:
    registry: ghcr.io/kowell-ai
    repository: <image-name>
    tag: "__VERSION__"
  ```

- 多镜像 Chart（coderunner、doc-convert、vega-backend/kafka-connect）对每个镜像 key 同样处理。
- `reusable-chart.yml` 在打包前把 `values.yaml` 中的 `__VERSION__` 替换为解析出的版本。
- 顺带修正 `decision-agent`、`agent-observability` 两个已迁移 Chart。

## 8. 版本来源

沿用 `reusable-test.yml` 的 `resolve-version`：单一真源 = **仓库根 `VERSION`**。**全部服务统一从 `0.1.0` 起**（已将根 `VERSION` 由 `0.8.0` 重置为 `0.1.0`）。`release/*` 分支用基础版本；其余分支 `BASE-<sanitized-branch>.sha<short7>`。镜像 tag 与 Chart `version`/`appVersion` 同源，且替换进 `values.yaml` 的 `__VERSION__`，保证镜像 tag 与 Chart 引用一致。

## 9. Chart 一致性验证（验收项）

**要求：构建产出的 Chart 与旧 pipeline 产出逐个比对，验证一致性。**

两套 Chart **不会字节相同**——registry（`acr.aishu.cn` → `ghcr.io/kowell-ai`）、tag 命名、`_componentMeta.json` 处理是**有意差异**。一致性指：**模板与结构等价，差异仅限于上述有意变更**；任何 templates / 依赖 / 非镜像 values 的差异都属**意外**，需排查。

### 9.1 比对方法（每个 Chart 独立执行）

1. **取旧产物**：从旧 pipeline 输出或 Harbor chartrepo 拉对应 `<chart>-<tag>.tgz`；若不可得，则在本地**复现旧变换**——对同一 Chart 源目录跑 `ref/.../chart-push.yml` 中的 `yq` 改写（registry/repository/tag、Chart.yaml version/appVersion、`_componentMeta.json` version）后 `helm package`。
2. **取新产物**：`reusable-chart.yml` 的 `helm package` 输出（`values.yaml` 已标准化 + `__VERSION__` 替换）。
3. **解包 + 规范化 diff**：

   ```bash
   mkdir old new && tar -xzf old.tgz -C old && tar -xzf new.tgz -C new
   # 规范化：把双方 registry/repository/tag/version 归一为占位符后再 diff
   diff -ru old/<chart> new/<chart>
   ```

4. **渲染对比**（更强）：用同一组 values `helm template` 两份 Chart，归一镜像 registry 后 diff 渲染出的 K8s 清单，确认资源对象逐字段等价。

### 9.2 逐 Chart 检查清单

对每个 Chart 核对：

- [ ] `templates/**` 完全一致（无意外增删改）。
- [ ] `Chart.yaml`：`name` 一致；`version`/`appVersion` 符合新版本口径。
- [ ] `values.yaml`：除 `image.registry/repository/tag` 的有意变更外，其余字段一致。
- [ ] `charts/`（子 chart 依赖）一致。
- [ ] 多镜像 Chart：每个镜像 key 的 registry/repository/tag 均正确指向 GHCR。
- [ ] `helm template` 渲染清单（归一镜像后）等价。

### 9.3 待评审口径

- 旧 Chart 含 `_componentMeta.json`（version 字段）。新流程是否保留该文件 / 是否需同步其 version？（建议保留并同步，否则属意外结构差异。）

## 10. 工作流清单

新增 `release-*.yml`（13 个，sandbox 延后）+ 1 个 base 构建 + 2 个已存在：

| 工作流 | service-path | test stack | 镜像（build-arg base 覆盖） | Chart |
|---|---|---|---|---|
| `release-adp-bkn-backend` | adp/bkn/bkn-backend | go | bkn-backend（免覆盖） | bkn-backend |
| `release-adp-ontology-query` | adp/bkn/ontology-query | go | ontology-query（免） | ontology-query |
| `release-adp-agent-retrieval` | adp/context-loader/agent-retrieval | python | → ubuntu:22.04 | agent-retrieval |
| `release-adp-dataflow` | adp/dataflow | python | → ubuntu:22.04 | dataflow |
| `release-adp-coderunner` | adp/dataflow/coderunner | python | coderunner→python:3.12；dataflowtools→ubuntu:22.04 | coderunner |
| `release-adp-doc-convert` | adp/dataflow/doc-convert | chart-only | gotenberg→gotenberg/gotenberg:8；tika→apache/tika:3.3.0.0-full | doc-convert |
| `release-adp-operator-integration` | adp/execution-factory/operator-integration | python | → ubuntu:22.04 | agent-operator-integration |
| `release-adp-vega-backend` | adp/vega/vega-backend | go | vega-backend（免）；kafka-connect→bitnami/kafka:3.9.0-debian-12-r10 | vega-backend + kafka-connect |
| `release-infra-mf-model-api` | infra/mf-model-api | python | → ghcr base | (chart) |
| `release-infra-mf-model-manager` | infra/mf-model-manager | python | → ghcr base | (chart) |
| `release-infra-oss-gateway` | infra/oss-gateway-backend | go | → golang:1.24.11 + ubuntu:24.04 | (chart) |
| `release-proton-mariadb` | deploy/charts/proton-mariadb | chart-only | — | proton-mariadb |
| `release-trace-ai-otelcol` | trace-ai/otelcol-contribute-chart | chart-only | — | otelcol-contrib |
| `release-infra-model-factory-base` | infra/model-factory-base | docker | python:3.11-bookworm（见 §6.2） | — |

> 每新增一个 `release-*.yml`，需在 `.github/workflows/README.md` 的索引表补一行（既有约定）。

## 11. 迁移阶段 / PR 拆分

1. **PR1 — 基建 + Chart 标准化**：`reusable-build.yml` 加 `build-args`/`target`；`reusable-chart.yml` 加 `__VERSION__` 替换；标准化全部 `values.yaml`（含修 decision-agent / agent-observability）。
2. **PR2 — model-factory-base**：新增 `infra/model-factory-base/` + `release-infra-model-factory-base.yml`，实跑确认依赖闭包（tiktoken 冲突、spacy 模型、补充包），推 GHCR。
3. **PR3 — adp 服务**：8 个 `release-adp-*.yml`（coderunner 连带 Python 3.12 改动）。
4. **PR4 — infra + chart-only**：oss-gateway + mf-model-api/manager（依赖 PR2）+ proton-mariadb + otelcol。
5. **后续 — sandbox**：单独评估迁移。

每个 Chart 在其服务 PR 内按 §9 完成一致性比对。

## 12. 安全

`ref/kweaver-github-build 2/vega/vega-backend/` 下提交了私钥：`rsa_private_key.pem`、`rsa_private_key_pkcs8.pem`。**建议从仓库移除并轮换密钥。** 与本次迁移无关，但需处理。

## 13. 未决问题 / 待验证

1. **tiktoken 版本冲突**（§6.2）：llmadapter `<0.4.0` vs manager `==0.5.1`，build 实测定夺。
2. **tika 上游 tag** `apache/tika:3.3.0.0-full` 是否对应内部 `dip/tika:3.3.0.0`，待确认。
3. **spacy 语言模型**是否需在 base 内预下载。
4. **coderunner numpy/pandas bump** 后需回归 coderunner 用例。
5. **旧 Chart 产物可得性**：能否从 Harbor / 旧 pipeline 取到 `.tgz` 用于 §9 直接比对；不可得则用"复现旧变换"路径。
6. `_componentMeta.json` 是否保留 / 同步 version（§9.3）。
