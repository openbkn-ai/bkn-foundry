# 阶段 F —— anyshare 剔除:爆炸半径分析(2026-06-05)

> 仅分析,未动代码。结论:F 不是"删 33 个动作 + 5 个适配器"一刀切 —— 它劈成
> **可机械删除的 doc 动作层(F-A)** 和 **牵动鉴权/引导的 ISF-authentication 层(F-B)**。
> F-B 含一个架构决策(代用户取 token),需先定。

## 0. 足迹

- `@anyshare/*` 引用 **91 处**,跨 ~30 文件。
- 涉及目录:`common/actionMap.go`、`common/trigger.go`、`pkg/actions/anyshare_*.go`+`ocr.go`、
  `pkg/mod/{vm_extfunc,token_strategy,token_mgnt}.go`、`logics/mgnt/*`、`logics/executor/executor.go`、
  `module/initial/initial.go`、`drivenadapters/{anyshare,doc,doc_share,eacp,authentication}.go`。

## 1. 五个适配器,两类

| 适配器 | 职责 | 打谁 | 归类 |
|---|---|---|---|
| `anyshare.go` | `ClusterAccess()` 取 AnyShare 集群地址 → `config.AccessAddress` | anyshare | **F-A** |
| `doc.go` | 文档库/配额/属性 | anyshare EACP | **F-A** |
| `doc_share.go` | 文档权限(perm2) | anyshare EACP | **F-A** |
| `eacp.go` | 文档 ACL/visitor/share | anyshare EACP | **F-A** |
| `authentication.go` | `GetAssertion`(JWT 断言)、`ConfigAuthPerm`(配应用账户代取 token 权限) | **ISF authentication 服务** `/api/authentication/v1/*` | **F-B(非 anyshare!)** |

> 计划里写"删 authentication.go",但它不是 anyshare —— 是正在退役的 ISF authentication。
> 它撑着代用户取 token,删法是架构问题,不是机械删除。

## 2. F-A:doc 动作层(机械、低风险)

**删什么**:
- `pkg/actions/anyshare_{file,folder,doc,doclib}.go`(+ `ocr.go`?见 §4 边界)及其在
  executor/registry 的注册。
- `common/actionMap.go` 中 `@anyshare/{file,folder,doc,doclib,ocr}/*` 的常量 + ActionMap/
  DataSourceActionMap 条目 + anyshare 事件触发器条目。
- `drivenadapters/{anyshare,doc,doc_share,eacp}.go`(+ 各 `_test.go`)。
- `logics/mgnt`、`validate.go`、`security_policy.go`、`trigger.go` 中对上述动作/触发器的引用。

**连带可删**:`config.AccessAddress` —— 仅被 `initial.go` 的 anyshare 发现循环自我消费
(`grep` 确认无其他消费方)。anyshare 走了,`initInternalAccount` 里
`NewAnyshare().ClusterAccess()` 那段(450-479)整段删,AccessAddress 字段可留可删。

**不碰**(非 anyshare / 共享):`@internal/*`、`@llm/*`、`@content*`、`@opensearch/*`、
`@dataset/*`、`@sandbox/*`、`@control/*`、`@subflow/*`、`@operator/*`、`@cognitive-assistant/*`、
`@docinfo/*`、`@audio/*`、`@anydata/*`、`@ecoconfig/*`、通用 cron/webhook/form 触发器。

## 3. F-B:代用户取 token(架构决策,先定再动)

当前链路(`pkg/mod/token_mgnt.go` `RefreshToken` 154-171):
```
appTokenMgnt.GetAppToken()                       // 应用自身 token(hydra)
  → authentication.GetAssertion(userid, appToken) // ISF authentication 签发该用户的 JWT 断言
  → hydra.RequestTokenWithAsserts(cid, secret, assertion) // hydra 用断言换该 user 的 access token
```
`ConfigAuthPerm`(`initial.go` 355)= 一次性给应用账户"可代任意用户取 token"的授权。

**这是 ISF 退役的硬骨头**:ISF authentication 一走,谁签那张 hydra 认的用户断言?

| 方案 | 说明 | 代价 |
|---|---|---|
| **B1. bkn-safe 签断言** | bkn-safe 作为 hydra 信任的 JWT 断言签发方(RFC 7523 jwt-bearer),复刻 `GetAssertion` 语义 | bkn-safe 要做"token 引擎"边角(DESIGN 说不做 token);要在 hydra 注册可信 issuer + 配密钥 |
| **B2. hydra token exchange** | 直接用 hydra RFC 8693(app token → user token),不经 ISF 断言 | 需 hydra 开 token-exchange + 配 subject 来源;bkn-safe 提供 user 主体校验 |
| **B3. 砍掉代用户** | flow-automation 一律以应用账户身份跑,不冒充触发用户 | 改行为:工作流不再以触发者身份执行权限/审计;影响最小代码,但语义变化大 |

> 建议:B1 或 B2 才能保住"以触发用户身份执行"的语义(权限/审计正确)。B3 最省事但改语义,
> 需产品确认 dataflow 是否还需"代用户"。**此项 = 决定后再写代码。**

## 4. 待澄清边界

1. **OCR**(`@anyshare/ocr/{general,eleinvoice,idcard,new}` + `ocr.go`):是 anyshare 专属还是通用
   OCR?若 OCR 经 anyshare 文档系统 → 删;若接通用 OCR 服务 → 留。需看 `ocr.go` 实现走向。
2. **datasource**(`AnyshareDataListFiles/Folders/...`):dataflow 选数据源用,源是 anyshare 文档库
   → 随 anyshare 删;但要确认无其他数据源类型复用该 schema。
3. **anyshare 事件触发器**(文件上传/移动/删除等 ~20 个):产品确认文档库无人用 → 删;
   但 `DataflowDocTrigger/UserTrigger/DeptTrigger/TagTrigger` 是否经 bkn-safe 目录事件保留?

## 5. 建议执行顺序(决定后)

1. 定 §3 代用户 token 方案(B1/B2/B3)+ §4 三个边界。
2. **F-A** 先行(机械、可独立编译验证):删 doc 动作 + 4 适配器 + actionMap + AccessAddress。
3. **F-B** 按 §3 决策实施;`authentication.go` 与 ISF authentication 一并退役。
4. 回归:`go build ./...` + `go vet` + 现有非 gomonkey 测试;executor 动作注册表无悬挂 id。

## 状态

仅分析。等 §3/§4 决策后进入 F-A。D4(flow-automation 目录切换)与 F-A 同期做更省。
