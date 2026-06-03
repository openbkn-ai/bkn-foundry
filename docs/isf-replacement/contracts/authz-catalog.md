# bkn-safe authz 目录 + 角色定义草案

> 2026-06-03。从 5 个服务的代码扒出的资源类型/操作全集 + 角色权限现状 + **角色定义建议(待 2026-06-04 拍板)**。
> 资源类型/操作已落 seed:`bkn-safe/server/internal/seed/data/catalog.json`。角色授权(grants.json)**只确认了应用管理员**,其余待定。

## 1. 资源类型 × 操作全集(事实,已 seed)

| 资源类型 | 来源服务 | 操作(精确 id) |
|---|---|---|
| `agent` | DA | use, publish, unpublish, unpublish_other_user_agent, publish_to_be_skill_agent, publish_to_be_web_sdk_agent, publish_to_be_api_agent, publish_to_be_data_flow_agent, create_system_agent, mgnt_built_in_agent, see_trajectory_analysis |
| `agent_tpl` | DA | publish, unpublish, unpublish_other_user_agent_tpl |
| `stream_data_pipeline` | pipeline-mgmt | view_detail, create, modify, delete, authorize, (data_query 定义未用) |
| `catalog` | vega | view_detail, create, modify, delete, authorize, task_manage |
| `resource` | vega | view_detail, create, modify, delete, authorize, task_manage |
| `connector_type` | vega | view_detail, create, modify, delete, authorize, task_manage |
| `knowledge_network` | bkn | view_detail, create, modify, delete, data_query, authorize, task_manage |
| `tool_box` | exec-factory | create, modify, delete, view, publish, unpublish, authorize, public_access, execute |
| `mcp` | exec-factory | 同 tool_box |
| `operator` | exec-factory(+flow-automation) | 同 tool_box(flow-automation 仅用 execute) |
| `skill` | exec-factory | 同 tool_box |
| `data_flow` | flow-automation | list, create, modify, delete, view, manual_exec, run_statistics, run_with_app, display(o11y 页) |

⚠️ **`view`(exec-factory/flow-automation)与 `view_detail`(vega/bkn/pipeline)是不同字符串,未归一化。**

## 2. 角色授权现状(代码里实际有的)

- **应用管理员 `1572fb82-526f-11f0-bde6-e674ec8dde71`** → `agent:*`(全 mgmt + use)+ `agent_tpl:*`(publish/unpublish/unpublish_other_user_agent_tpl)。**唯一在代码里 boot 授权的**(DA InitPermission)。已落 grants.json。
- **其余服务无 boot 角色授权** —— pipeline/vega/bkn/exec-factory 都是"创建者拿自己资源的 owner 权限"(per-object),不给业务角色静态授权。
- **内置组件**(exec-factory mcp_builtin/internal_*)给**根部门**(`00000000-...`,= 全体用户)`public_access`+`execute`。
- **数据管理员 `00990824-...`**:flow-automation 只有 `IsDataAdmin` **检查**(假设它拥有 `data_flow:*` + `dataflow_page:o11y:display`),**没有写入授权的代码** → 推断,待确认。
- **AI管理员 `3fb94948-...`**:**全仓 5 服务里找不到任何授权代码**,目前零权限。其 op 集按 grants.json 注释"在 data-lake/dataflow 模块",未在这 5 服务,需另扫 model-factory/data-lake 或产品定义。

## 3. 角色定义建议(草案 —— 明天拍)

依 landing-design 的角色描述 + 上面目录,建议把 3 个业务角色映射到资源类型(`*` = 整类,ops 先给全 mgmt,细分明天定):

| 业务角色 | landing-design 描述 | 建议管的资源类型 |
|---|---|---|
| **应用管理员** `1572fb82` | 创建系统智能体/自定义空间,发布智能体与模板,管理内置智能体 | `agent`、`agent_tpl`(已定)；**待议**:是否含 `skill`/`mcp`/`tool_box`(应用能力构建) |
| **数据管理员** `00990824` | 多模态数据湖、本体引擎、Dataflow 数据处理流 | `catalog`、`resource`、`connector_type`(数据湖/连接)、`knowledge_network`(本体引擎)、`stream_data_pipeline`、`data_flow`(数据流) |
| **AI管理员** `3fb94948` | Dataflow 逻辑与行动下所有资源,接入/训练模型 | `operator`(行动/算子)、`skill`、`mcp`、`tool_box`(能力)、**`model`**(模型工厂,目前不在目录,需补) |

**要明天拍的点:**
1. 上面映射对不对?有无重叠(某资源类型多角色共管)?
2. 每角色给**全 mgmt 权限**还是**细分**(如只读 vs 增删改)?
3. **AI管理员**到底管什么(代码里零授权)—— 含 `model`?需扫 model-factory / 产品定义补 `model` 资源类型 + 操作。
4. **数据管理员**对 `data_flow` 的授权是 ISF 外部建的还是要 seed 提供?(确认后写 grants.json)
5. `view` vs `view_detail` 双标准是否要统一(影响应用改造)。
6. 超级/系统/安全/审计/组织 等 **6 个 system 角色**的权限(本次没扒,多为全局管理)。

## 4. 下一步
- 角色映射拍板后 → 写全 `grants.json`(各业务角色 → 资源类型 → ops)。
- 补 `model` 资源类型(扫 model-factory)。
- grants 进 seed,影子比对验证判定与 ISF 一致。
