# 移除整条 dataflow 产品线 —— 范围分析(2026-06-05)

> 决策(用户):**移除整条 dataflow 产品线 + 同步清理引用**。仅分析,未动代码。
> 这取代了 ISF 替换里的 D4 / F(flow-automation 目录切换 + anyshare 剔除)——服务都删了,无需再切。

## 0. 关键结论

- **仓库内无任何非-dataflow 的 Go 代码 import** flow-automation / flow-stream-data-pipeline /
  coderunner / doc-convert 模块 → **删目录不会断其他服务的编译**。
- 唯一**运行时调用链**:`execution-factory` 的 impex(组件导入/导出)调 flow-automation 的
  `Import`/`Export`(DAG 导入导出)。这条要在 exec-factory 里一并摘掉。
- 其余都是**部署/CI/配置**引用(deploy.sh、charts、SQL、release-manifest、GitHub workflows、
  data-migrator yaml)。

## 1. 删除(代码 + 资产)

| 路径 | 说明 |
|---|---|
| `adp/dataflow/flow-automation/` | dataflow 引擎(含 anyshare、代用户 token、B3/D4/F 的改动) |
| `adp/dataflow/flow-stream-data-pipeline/` | pipeline-mgmt(含 B1 适配器) |
| `adp/dataflow/coderunner/` | 代码执行 |
| `adp/dataflow/doc-convert/` | 文档转换 |
| `adp/dataflow/charts/{dataflow,coderunner,doc-convert}/` | 3 个 chart |
| `adp/dataflow/Dockerfile.dataflow`、`README*.md`、`VERSION` | 构建/文档 |

→ 实际即 **删除整个 `adp/dataflow/` 目录**。

## 2. 同步清理引用(否则悬挂)

### 2.1 execution-factory(运行时调用方,代码要改)
- `operator-integration/server/drivenadapters/flow_automation.go` —— 删(flowAutomationClient)。
- `operator-integration/server/interfaces/drivenadapters.go` —— 删 `FlowAutomation` 接口 +
  `FlowAutomationImportReq` / `FlowAutomationExportResp`。
- `operator-integration/server/mocks/drivenadapters.go` —— 删 `MockFlowAutomation`。
- `operator-integration/server/infra/config/config.go:47` —— 删 `FlowAutomation` 配置字段。
- **业务调用点**(必须摘掉 dataflow 导入/导出分支):
  - `logics/impex/index.go:38,54,156-160`(`m.FlowAutomation.Import`)
  - `logics/operator/impex.go:566-567`(`m.FlowAutomation.Export`)
  - `logics/operator/index.go:38,66`(manager 字段 + 构造)
  - **决策点**:operator 导入/导出里"dataflow 类型"整支删除(其余 operator 类型保留)。需读 impex 看 dataflow 是独立分支还是混在通用流程里。

### 2.2 部署
- `deploy/deploy.sh:1043,1058` —— `install_flowautomation` / `uninstall_flowautomation`(及函数定义)。
- `deploy/scripts/services/core.sh:23` —— 服务清单里 `"flowautomation"`。
- `deploy/scripts/sql/0.4.0/.../flowautomation/`、`0.5.0/.../flowautomation/` —— 建库 SQL。
- `deploy/release-manifests/0.1.0/bkn-foundry.yaml:53-60` —— `dataflow` / `coderunner` /
  `doc-convert` 三个组件条目。
- `data-migrator/config.monorepo.yaml` —— coderunner/doc-convert 引用。
- `deploy/release-manifests/archive/*` —— **历史归档,保留不动**(过去版本快照)。

### 2.3 CI
- `.github/workflows/release-adp-dataflow.yml`、`release-adp-coderunner.yml`、
  `release-adp-doc-convert.yml` —— 删。
- `.github/CODEOWNERS`、`.github/workflows/README.md` —— 删 dataflow 相关行。

## 3. 作废的 ISF 工作(随服务删除自然消失,无需单独回退)

- B1 的 pipeline-mgmt 全适配器(`9a9bdff0` 中 pipeline 部分)
- B3 flow-automation 全适配器(`fd00a313`)
- D4(flow-automation 目录切换)、F(anyshare 剔除)—— 不再需要。

> vega/bkn 的 B1 部分、DA(B2)、mf-model(B4/D3)、exec-factory(authz)等**不在 dataflow 内,保留**。

## 4. 保留 / 待定

- bkn-safe seed 里的 `data_flow` 资源类型 + 相关 authz 授权:dataflow 没了后是死类型。
  **可后续从 seed 清掉**(catalog.json / grants.json),非本次必须;低优先。
- `@subflow/call/dataflow`、pipeline 的 `ManagerDeployName="dataflow"` 等随服务删除一起走。

## 5. 验证清单(执行后)

- `execution-factory` 去掉 FlowAutomation 后 `go build ./...` + `go vet` 通过(impex 分支摘干净)。
- 全仓库无残留 import / 配置指向已删模块:`grep -rn 'adp/dataflow\|flow-automation\|flowautomation'`
  仅剩 archive 历史快照 + bkn-safe `data_flow` 资源类型字符串。
- deploy.sh / core.sh / release-manifest 不再引用 dataflow 组件。
- **examples 无影响**:`./examples`(01-db-to-qa…06-world-cup)与 `help/*/examples` 均不调
  `/api/automation`/flow-automation;只用 vega-backend / agent-operator-integration(exec-factory)。
  `03-action-lifecycle` 的"行动+调度"是 bkn 的 `action_schedule`,非 dataflow。移除无需改 examples。

## 5.5 代码量(2026-06-05 实测)

**删除 —— `adp/dataflow/`**:**914 个文件**。
- 一线源码 ≈ **116k 行**:flow-automation go **102.6k** + py 4.3k;pipeline go **8.1k**;coderunner py **5.2k**;doc-convert ≈ 0(3 文件)。
- 另含:测试 go ~19.6k、生成 mock ~4.3k、vendored `libs/go` 5.7k、schema 116 个 json、前端/构建产物(flow-automation 目录磁盘 ~144M,含 node_modules 等)。
- 原始全文件行数 ~1.08M(含三方/生成/前端,非有效清理量)。
- charts:32 文件(dataflow/coderunner/doc-convert)。

**修改(非删) —— execution-factory 摘 FlowAutomation**:**8 个 .go 文件**
(`drivenadapters/flow_automation.go` 整删 + `interfaces/drivenadapters.go`、`mocks/drivenadapters.go`、
`infra/config/config.go`、`logics/impex/index.go`、`logics/operator/{index,impex,impex_test}.go` 摘引用)。

**修改 —— deploy/CI**:`deploy.sh`、`scripts/services/core.sh`、`release-manifests/0.1.0/bkn-foundry.yaml`、
SQL 建库目录、3 个 `.github/workflows/release-adp-{dataflow,coderunner,doc-convert}.yml`、CODEOWNERS、
`data-migrator/config.monorepo.yaml`。

**独立/可选 —— vega anyshare 连接器**(§见正文,不在 dataflow 内):4 文件、**1944 行 go**。

> 净清理(本次范围):删 ~914 文件(一线源码 ~116k 行)+ 改 ~14 个文件(exec-factory 8 + deploy/CI ~6)。

## 6. 建议执行顺序

1. 先摘 **execution-factory** 的 FlowAutomation(代码,独立编译验证)。
2. `git rm -r adp/dataflow/`。
3. 清 deploy(deploy.sh/core.sh/SQL/release-manifest 0.1.0)+ CI workflows + CODEOWNERS + data-migrator yaml。
4. 全仓 grep 回归 + exec-factory build/vet。
5. (可选,低优先)bkn-safe seed 去 `data_flow` 资源类型。
