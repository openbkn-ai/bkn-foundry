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
  - **精确措辞(已核实)**:只 **impex** 碰 dataflow —— composite 算子的 register/edit/execute **不碰**(grep 空)。
    所以**不删 composite 算子类型本身**,只摘 impex 的 composite DAG 分支:
    - 导出 `getCompositeOperatorDependencies`(impex.go:539-)的 `dagIDs` 收集 + `FlowAutomation.Export` 整段 → 删,`compositeConfigs` 留空。
    - 导入 `impex/index.go:153-162` 的 `if data.Operator.CompositeConfigs>0 { FlowAutomation.Import }` 分支 → 删。
    - 保留 composite 枚举;如需可在注册校验**禁新建 composite**(产品定)。

### 2.2 部署
- `deploy/deploy.sh:1043,1058` —— `install_flowautomation` / `uninstall_flowautomation`(及函数定义)。
- `deploy/scripts/services/core.sh:23` —— 服务清单里 `"flowautomation"`。
- `deploy/scripts/sql/0.4.0/.../flowautomation/`、`0.5.0/.../flowautomation/` —— 建库 SQL。
- `deploy/release-manifests/0.1.0/bkn-foundry.yaml:53-60` —— `dataflow` / `coderunner` /
  `doc-convert` 三个组件条目。
- `data-migrator/config.monorepo.yaml` —— coderunner/doc-convert 引用。
- `deploy/conf/config.yaml:17` —— `flowAutomation:` 块(S3 配置 ~16-28 行),删。
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

## 4. 保留 / 后续清理(非本轮"删 dataflow 产品线"必须;均无编译依赖)

> 本轮只删 dataflow 产品线 + exec-factory/deploy/CI 引用。以下是 dataflow 没了之后变成
> **死枚举/死配置/死类型**的软引用,跨服务,**不影响编译**,可opportunistic 跟随清理或单列后续项。

- **decision-agent "发布为 Dataflow Agent" 残留**(已核实,纯枚举/标志/DB 列):
  - `src/domain/enum/cdapmsenum/operator.go` —— `AgentPublishToBeDataFlowAgent Operator = "publish_to_be_data_flow_agent"`(在 EnumCheck/GetAll 列表里)。
  - `src/infra/persistence/dapo/release.go` —— `IsDataFlowAgent` DB 列 `f_is_data_flow_agent` + `PublishToBeDataFlowAgent` 发布分支。
  - `src/domain/enum/chat_enum/chat_scenario.go` —— `ChatScenarioADPDataFlow = "ADP_data_flow"`。
  - `permissionsvc/{init_resource_type,get_user_status}.go` —— 注册/读 `AgentPublishToBeDataFlowAgent` 权限。
  - `rdto/.../management.go`、`agent_config_models.go` —— `PublishToBeDataFlowAgent` / `IsDataFlowSetEnabled` 出参。
  - 性质:删 dataflow 后 UI 留"发布为 Dataflow Agent"死选项;清理 = 摘枚举 + DB 列 + 聊天场景 + 发布分支(中等改动,独立后续项)。
- **bkn-safe** seed 的 `data_flow` 资源类型 + authz 授权(catalog.json / grants.json):死类型,低优先。
- `@subflow/call/dataflow`、pipeline `ManagerDeployName="dataflow"` 等随服务删除一起走。

## 5. 验证清单(执行后)

- `execution-factory` 去掉 FlowAutomation 后 `go build ./...` + `go vet` 通过(impex 分支摘干净)。
- 全仓库无残留 import / 配置指向已删模块:`grep -rn 'adp/dataflow\|flow-automation\|flowautomation'`
  仅剩 archive 历史快照 + bkn-safe `data_flow` 资源类型字符串。
- deploy.sh / core.sh / release-manifest 不再引用 dataflow 组件。
- **examples 无影响**:`./examples`(01-db-to-qa…06-world-cup)与 `help/*/examples` 均不调
  `/api/automation`/flow-automation;只用 vega-backend / agent-operator-integration(exec-factory)。
  `03-action-lifecycle` 的"行动+调度"是 bkn 的 `action_schedule`,非 dataflow。移除无需改 examples。

### 删前必查(仓外硬前置,代码 grep 看不到)
1. **线上 `operator_type='composite'` 存量行**:migrations 不发 composite 种子,全是运行时建行。
   非 0 → 这些组合算子的 `dag_id` 在 dataflow 删除后悬挂(导出会少 DAG 依赖)。删前确认存量/影响。
2. **仓外前端/网关是否直连 `/api/automation`**:ingress 可能对外暴露该路径给 dataflow UI(前端在别 repo)。
   需确认前端/网关侧同步下线,避免对外 404。

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

**vega anyshare 连接器 —— 保留(用户已定:anyshare 作为 vega 数据源毋庸置疑)**。
不在删除范围。它是 vega 当前唯一的 fileset 数据源连接器
(`logics/connectors/local/fileset/anyshare/`,读/查文档库)。
注意区分两种 anyshare 耦合:**数据源(vega,读,保留)** vs **dataflow 动作(文档增删改/ACL,删)**。

> 净清理(本次范围):删 ~914 文件(一线源码 ~116k 行)+ 改 ~14 个文件(exec-factory 8 + deploy/CI ~6)。

## 6. 建议执行顺序

1. 先摘 **execution-factory** 的 FlowAutomation(代码,独立编译验证)。
2. `git rm -r adp/dataflow/`。
3. 清 deploy(deploy.sh/core.sh/SQL/release-manifest 0.1.0)+ CI workflows + CODEOWNERS + data-migrator yaml。
4. 全仓 grep 回归 + exec-factory build/vet。
5. (可选,低优先)bkn-safe seed 去 `data_flow` 资源类型。
