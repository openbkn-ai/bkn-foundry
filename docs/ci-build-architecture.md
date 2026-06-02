# 微服务构建机制设计：清单随服务走（Manifest-per-Service）

> 状态：设计稿（待评审）
> 适用范围：bkn-foundry monorepo 的镜像 / Helm Chart 构建与发布
> 目标读者：维护 CI 的工程师、新增微服务的开发者

## 1. 背景与问题

仓库是一个 monorepo，已识别出约 17 个可部署单元，横跨 Go / Python / Node 多种技术栈：

| 域 | 构建单元（示例） |
|----|------------------|
| `adp` | bkn-backend、ontology-query、agent-retrieval、dataflow（coderunner/dataflow/doc-convert）、operator-integration、vega-backend、kafka-connect |
| `decision-agent` | agent-factory(go)、agent-executor(py)、agent-memory(py) |
| `infra` | mf-model-api、mf-model-manager、oss-gateway-backend、sandbox（control-plane/web） |
| `trace-ai` | agent-observability、otelcol-contrib（chart-only） |

当前 CI 现状（`.github/workflows/`）：

- 真正的发布流水线只有 2 条：`release-agent-observability.yml`、`release-otelcol-chart.yaml`。
- 两条流水线 **~90% 重复**：相同的 `resolve-version` bash、checkout、setup-go、test、buildx、login、build-push、helm package/push。
- 随着服务增多，这种"一服务一份完整 workflow"的模式会带来：
  - **N 份重复逻辑**：改一处构建规则要改 N 个文件。
  - **路径硬编码**：每个 workflow 把服务路径、镜像名、chart 路径写死，服务移动 / 重命名即失效。
  - **集中式心智负担**：所有 workflow 挤在 `.github/workflows/`，与服务代码分离，谁拥有哪条流水线不清晰。

**诉求：能否让"流水线文件跟着微服务走"？**

## 2. 关键约束

> GitHub Actions **只**从仓库根的 `.github/workflows/` 平铺目录加载 workflow。放在 `adp/vega/…` 下的 `.yml` 永远不会被触发。

因此"把可运行的 workflow 文件放进服务目录"在 GitHub Actions 上**不可行**。

但诉求的*本质*可以满足——把流水线拆成两部分：

```
规格 SPEC（随服务走，放服务目录）          逻辑 LOGIC（集中，写一次）
adp/vega/vega-backend/ci.yaml        ──▶   .github/workflows/reusable-build-service.yml
  service: vega-backend                    .github/workflows/ci-build.yml      (派发器)
  build: [...]                             .github/workflows/release-build.yml (派发器)
  charts: [...]
```

- 服务移动 / 重命名 / 删除 → 它的 `ci.yaml` 跟着走。
- 新增服务 → 丢一个 `ci.yaml`，**零中心改动**（自动发现）。
- 构建逻辑只存在于一个文件，从现有 2 份副本去重而来。

## 3. 设计目标

1. **规格随服务**：每个服务的构建意图声明在自己目录下的 manifest 里。
2. **逻辑唯一**：所有通用构建步骤（版本解析、test/lint、镜像、chart）只写一份。
3. **自动发现**：派发器运行时扫描全部 manifest，无需为新服务编辑中心文件。
4. **按需构建**：只构建本次变更涉及的服务（基于 `git diff` 路径匹配）。
5. **建模真实复杂度**：一个服务可含多个镜像、多个 chart、chart-only、共享 VERSION 文件。

## 4. 架构总览

三层：

```
┌──────────────────────────────────────────────────────────────┐
│ 1. 派发器 ci-build.yml / release-build.yml  (.github/workflows)│
│    on: push / pull_request                                     │
│    job discover: 扫描 **/ci.yaml + git diff → 输出变更服务矩阵  │
│    job build (matrix):  uses reusable-build-service.yml         │
└───────────────────────────────┬────────────────────────────────┘
                                 │ with: manifest=<path>
┌────────────────────────────────▼───────────────────────────────┐
│ 2. reusable-build-service.yml  (workflow_call, 写一次)          │
│    parse manifest (yq) → resolve-version → test/lint            │
│    → build & push images → package & push charts                │
└────────────────────────────────┬───────────────────────────────┘
                                 │ reads
┌────────────────────────────────▼───────────────────────────────┐
│ 3. <service>/ci.yaml  (规格，随服务走)                          │
└──────────────────────────────────────────────────────────────┘
```

## 5. 服务清单 `ci.yaml`（规格层）

放在每个服务根目录，例如 `adp/vega/vega-backend/ci.yaml`。所有路径相对 **manifest 所在目录**，除非显式标注相对仓库根。

### 5.1 Schema

```yaml
apiVersion: openbkn-ci/v1
service: vega-backend            # 唯一名，用于矩阵 / 日志 / 镜像默认名

# 版本来源；相对仓库根。缺省回退到服务目录内 VERSION，再回退根 VERSION。
versionFile: trace-ai/VERSION

# 触发该服务构建的额外路径（manifest 目录默认已包含）。用于共享 VERSION 等。
triggerPaths:
  - trace-ai/VERSION

# 构建单元列表：一个服务可产出 0..N 个镜像。chart-only 服务留空。
build:
  - id: vega-backend
    stack: go                    # go | python | node | docker（docker=纯 Dockerfile，无语言测试）
    goModFile: server/go.mod     # stack=go 时，setup-go / 测试用
    test: go test ./...          # 可选；缺省按 stack 推断
    lint: golangci-lint          # 可选
    image:
      name: vega-backend         # 缺省 = service
      context: .
      dockerfile: docker/Dockerfile
      platforms: [linux/amd64, linux/arm64]

# Chart 列表：一个服务可发布 0..N 个 chart。
charts:
  - path: helm/vega-backend
  - path: helm/kafka-connect
```

### 5.2 真实场景如何落到 schema

清单必须建模仓库的真实复杂度，而非"一服务一镜像"的简化假设：

- **多镜像服务** `decision-agent/agent-backend`：`build:` 列三项——`agent-factory`(go)、`agent-executor`(python)、`agent-memory`(python)。
- **多 chart 服务** `vega`（vega-backend + kafka-connect）、`dataflow`（coderunner + dataflow + doc-convert）：`charts:` 列多项。
- **Chart-only** `trace-ai/otelcol-contribute-chart`：`build:` 空，仅 `charts:`。
- **共享 VERSION** 多个 trace-ai 服务共用 `trace-ai/VERSION`：用 `versionFile` + `triggerPaths` 指向它。
- **第三方镜像** `doc-convert` 的 gotenberg / tika：`stack: docker`，跳过语言级 test/lint。
- **go.mod 位置不一**（`server/go.mod` vs 根）：用 `goModFile` 显式指定。

## 6. 复用工作流 `reusable-build-service.yml`（逻辑层）

`workflow_call` 入口，是现有 2 条 release workflow 去重后的唯一实现。

### 6.1 输入

只接收一个字符串 `manifest`（manifest 文件相对仓库根路径）。其余字段由复用工作流内部用 `yq` 解析——避免 reusable workflow 只能传 string/bool/number 导致的几十个入参。

```yaml
on:
  workflow_call:
    inputs:
      manifest: { type: string, required: true }   # 如 adp/vega/vega-backend/ci.yaml
      publish:  { type: boolean, default: false }   # false=仅构建验证；true=推送
    secrets:
      SWR_USERNAME: { required: false }
      SWR_PASSWORD: { required: false }
```

### 6.2 Jobs（保留现有逻辑，去重）

1. **parse**：`yq` 读取 manifest → 输出 service、versionFile、images JSON、charts JSON。
2. **resolve-version**：搬用现有 bash（`release/*` 用基础版本；否则 `BASE-<分支>.sha<short>`）。`BASE_VERSION` 从 manifest 的 `versionFile` 读取。
3. **test-and-lint**：按每个 build 单元的 `stack` 分派（go→setup-go+go test+golangci-lint；python→ruff/pytest；docker→跳过）。
4. **build-and-push-image**：对 `images` 矩阵跑 buildx 多架构构建；`publish=true` 时登录 SWR 并 push。镜像默认仓库 / 组织来自仓库级 vars（`SWR_REGISTRY`、`SWR_ORGANIZATION`），manifest 仅在需要时覆盖。
5. **package-and-push-chart**：对 `charts` 矩阵跑 helm lint/package，注入版本，`publish=true` 时 push 到 GHCR OCI。

> 集中后，"改构建规则"= 改这一个文件，立即对所有服务生效。

## 7. 派发器 `ci-build.yml` / `release-build.yml`（编排层）

### 7.1 自动发现 + 变更过滤

```yaml
on:
  pull_request:        # ci-build：PR 上仅验证（publish=false）
  push:
    branches: ['**']   # release-build：push 上构建并推送（publish=true）

jobs:
  discover:
    runs-on: ubuntu-latest
    outputs:
      matrix: ${{ steps.set.outputs.matrix }}
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }       # diff 需要历史
      - id: set
        run: |
          set -euo pipefail
          # 1) 找出全部 manifest
          mapfile -t MANIFESTS < <(find . -name ci.yaml -path '*/ci.yaml' | sed 's#^\./##' | sort)
          # 2) 算出本次变更文件（PR 用 base..head；push 用 before..sha）
          CHANGED="$(git diff --name-only "$BASE_SHA" "$HEAD_SHA")"
          # 3) 每个 manifest：其目录 + triggerPaths 命中变更则入选
          SELECTED=()
          for m in "${MANIFESTS[@]}"; do
            dir="$(dirname "$m")"
            paths="$dir"
            paths+=" $(yq -r '.triggerPaths[]? ' "$m")"
            for p in $paths; do
              if grep -q "^$p" <<< "$CHANGED"; then SELECTED+=("$m"); break; fi
            done
          done
          # 4) 输出 JSON 矩阵
          printf '%s\n' "${SELECTED[@]}" | jq -R . | jq -cs '{manifest: .}' \
            | sed 's/^/matrix=/' >> "$GITHUB_OUTPUT"

  build:
    needs: discover
    if: ${{ fromJson(needs.discover.outputs.matrix).manifest[0] != null }}
    strategy:
      fail-fast: false
      matrix: ${{ fromJson(needs.discover.outputs.matrix) }}
    uses: ./.github/workflows/reusable-build-service.yml
    with:
      manifest: ${{ matrix.manifest }}
      publish: ${{ github.event_name == 'push' }}
    secrets: inherit
```

要点：
- 从矩阵调用 reusable workflow（`uses:` + `strategy.matrix`）自 2022 起受支持。
- 矩阵传 manifest 路径，解析放进 reusable workflow，避免巨量入参。
- `fail-fast: false`：一个服务失败不连累其它服务。

### 7.2 命名约定对齐

仓库 `.github/workflows/README.md` 已保留前缀：`ci-`、`release-`、`reusable-`。本设计直接落入：
- `ci-build.yml`（PR 验证，`publish=false`）
- `release-build.yml`（push 构建并推送，`publish=true`）
- `reusable-build-service.yml`（`workflow_call` 入口）

无需扩展 `lint-workflow-files.yml` 白名单。

## 8. 迁移计划

分阶段，零停机：

1. **阶段 0 — 落地骨架**：新增 `reusable-build-service.yml` + `ci-build.yml` + `release-build.yml`，但 discover 暂只匹配 1 个试点服务。
2. **阶段 1 — 试点**：为 `trace-ai/agent-observability` 写 `ci.yaml`，与旧 `release-agent-observability.yml` **并行**跑，比对产物（镜像 tag、chart 版本）一致。
3. **阶段 2 — 收编 chart-only**：为 `otelcol-contribute-chart` 写 `ci.yaml`（`build:` 空），验证 chart-only 路径。
4. **阶段 3 — 删除旧 workflow**：两条旧 release workflow 删除，逻辑已收敛到 reusable。
5. **阶段 4 — 全量铺开**：为其余服务逐个补 `ci.yaml`。每补一个，自动进入构建矩阵，无中心改动。

## 9. 维护收益对比

| 维度 | 现状（一服务一 workflow） | 本设计 |
|------|--------------------------|--------|
| 新增服务 | 复制 ~120 行 workflow，改路径 | 丢一个 `ci.yaml`（~15 行） |
| 改构建规则 | 改 N 个文件 | 改 1 个 reusable workflow |
| 服务移动/改名 | 手动同步 workflow 路径 | manifest 跟着目录走，自动 |
| 流水线归属 | 集中在 `.github/`，与代码分离 | 规格与服务代码同目录 |
| 仅构建变更服务 | 每个 workflow 各写 `paths:` | discover 统一处理 |

## 10. 边界与权衡

- **矩阵上限**：GitHub 单次最多 256 个矩阵 job。17 服务远未触顶；若极端增长，按域分批派发。
- **Secrets**：`secrets: inherit` 传递 SWR 凭据；PR（fork）默认拿不到 secrets，故 `ci-build` 用 `publish=false` 仅验证。
- **diff 边界**：push 用 `before..sha`，首推 / force-push 时 `before` 可能为全零，需回退到"全量构建"或与默认分支比对。
- **`paths:` 触发 vs 内部 diff**：派发器本身不能用 workflow 级 `paths:`（否则改 manifest 之外无法触发 discover）；改为运行 discover 再内部过滤，多一次轻量 job 启动开销。
- **第三方镜像版本**：gotenberg/tika 等固定上游版本，`stack: docker` 跳过测试，但仍走统一推送，便于私有仓库镜像缓存。
- **yq 依赖**：runner 默认无 `yq`，需在 reusable workflow 里 `setup` 一步安装（或用 mikefarah/yq action）。

## 11. 未决问题

1. 镜像默认仓库 / 组织放仓库级 **Variables** 还是留在 manifest？（建议 Variables，manifest 仅覆盖特例）
2. PR 阶段是否需要把镜像 push 到临时 tag 供预览部署？还是纯验证即可？
3. 是否需要"全量重建"手动入口（`workflow_dispatch` 输入 `all=true`）用于基础镜像 / CI 逻辑变更后的统一刷新？
4. Python 服务的 test/lint 标准命令（pytest? ruff? mypy?）需与各组确认后写进 `stack: python` 默认分派。
