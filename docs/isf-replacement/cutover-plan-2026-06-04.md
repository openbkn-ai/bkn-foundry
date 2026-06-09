# ISF 替换 —— 收尾切换计划 (2026-06-04)

> 基于完整调用方清单(含 infra 的 mf-model)。上游决策见 `isf-excision-migration-plan-2026-06-03.md`。
> 已完成:bkn-safe 本体(认证/鉴权/目录/LDAP/seed)+ 6 个 Go authz 服务 shadow + exec-factory 全适配器 + k3s 部署 + 全测试 + 文档。

## 调用方全景(修正后)

| 层 | 调用方 | 数 |
|---|---|---|
| **introspect**(token 校验,保兼容) | exec-factory,DA,vega,bkn,pipeline-mgmt,flow-automation,context-loader,ontology-query(Go)+ mf-model-manager,mf-model-api(Py) | ~10 |
| **authz**(鉴权,Casbin 重做) | exec-factory,DA,vega,bkn,pipeline-mgmt,flow-automation(Go)+ **mf-model-manager,mf-model-api(Py)** | **8** |
| **user-mgmt**(目录,全新接口) | vega,bkn,DA,flow-automation(Go)+ **mf-model-manager,mf-model-api(Py)** | 6 |

> mf-model(Python)三层都涉及,之前漏了 —— 本计划补上。

---

## 阶段 A —— 补齐 authz shadow 覆盖(→ 8/8)
- [x] **A1. mf-model-manager + mf-model-api(Python)authz shadow**:`app/utils/permission_manager.py` 包一层,`AUTHZ_PROVIDER=shadow` + `BKN_SAFE_URL` 时并调 bkn-safe `/api/safe/v1/authz/check`,记 diff,ISF 权威。env 可回退。commit `99226276`。

## 阶段 B —— 补齐 bkn-safe 全适配器(翻"权威"前置)✅ 全部完成
当前只 shadow(ISF 权威);翻 `bkn-safe` 权威需实现各服务驱动接口的全部方法对接 bkn-safe。
- [x] **B1. vega/bkn/pipeline-mgmt**:`PermissionAccess` 全方法 → bkn-safe。三家一份适配器复用(pipeline 多 GetResourcesOperations,vega/bkn 多出无害)。commit `9a9bdff0`。
- [x] **B2. DA**:`AuthZHttpAcc`(22 方法)→ bkn-safe。决策/写/查映射齐;ISF-only 面(全局枚举/init/deny)显式降级(日志,无静默)。commit `7d035d5d`(+ bkn-safe `GET /policies` `d86b711c`)。
- [x] **B3. flow-automation**:`PermPolicyHandler`(13 方法)→ bkn-safe。组合复刻 ISF;ListResource 经新 `GET /resources`,资源 id 原样回环。commit `fd00a313`。
- [x] **B4. mf-model(Py)**:permission_manager 全 4 方法 → bkn-safe。commit `b5be3590`。
- exec-factory:✅ 早已具备(`AUTHZ_PROVIDER=bkn-safe`)。

> 翻权威前置(B + C-authz)已就绪:8/8 服务均可 `AUTHZ_PROVIDER=bkn-safe` 翻权威、随时翻回。

## 阶段 C —— bkn-safe 补缺端点(切重度调用方前)
- [ ] **C1. directory**:`apps`(应用账户)、`emails`、`internal-groups`(写)—— flow-automation/mf-model 用。(用户管理切换 D 前置,待做)
- [x] **C2. authz**:`GET /policies`(列某资源各访问者授权,DA ListPolicy(All))+ `GET /resources`(列访问者对某类型某操作的实例,flow-automation ListResource)。`resource-operation` 用现有 `POST /operations` 覆盖;全局枚举用 `GET /resources`。commit `d86b711c` / `f20bc7ec`。

## 阶段 D —— user-mgmt 目录调用方切换(→ bkn-safe directory)
顺序按规模。统一开关 `DIRECTORY_PROVIDER=bkn-safe` + `BKN_SAFE_URL`,默认/未设=ISF,随时翻 env 回退。
- [x] **D1. vega + bkn**(`/v2/names`,含 app 名)→ `directory/names`。commit `9115a7df`(前置 bkn-safe `/names` 扩 app/contactor `2122a381`)。
- [x] **D2. DA umcmp 全 12 方法** → bkn-safe directory。前置 bkn-safe 新建层级读面(部门祖先链/传递部门 id/批量 user-detail 含 parent_deps+groups+roles/group 成员拆分/子树 search-org)commit `f354404b`;umcmp flip commit `03d83b25`。语义:**传递子树** + **groups 含部门继承**。app/contactor 建模为 User 行(account_type)。
  > 注:DA 另有 usermanagementacc / umhttpaccess 两个 client,本次按你指定只切 umcmp。
- [x] **D3. mf-model(Py)**(`get_username_by_ids` → `directory/names`,manager+api 各一份 + unittest)。commit `f4fdf952`。
- [ ] **D4. flow-automation**(8 端点)—— 随 anyshare 缩减一并处理(很多端点服务 anyshare 功能)。

## 阶段 E —— 部署 + 影子比对 + 翻权威(逐服务)
- [~] **E1. 部署产物已备**(代码/配置就绪,待 CI 发布 + VM 部署):hydra 生产 chart(`bkn-safe/charts/hydra`,v26.2.0/PG/migrate/serve,commit `35d30ebc`)+ bkn-safe 生产 Dockerfile + `release-adp-bkn-safe.yml` CI(发镜像 + bkn-safe & hydra chart 到 OCI)+ bkn-foundry manifest 声明 hydra/bkn-safe(commit `349b9491`)。**待办(需 OCI/VM)**:PG 实例供给(hydra DSN)、CI 跑发布、VM 部署 + seed 角色/资源/权限 + 建用户分角色。
- [~] **E2/E3 开关已备**:各服务 chart 加 `bknSafe.{authzProvider,directoryProvider,url}` → 渲染 `AUTHZ_PROVIDER/DIRECTORY_PROVIDER/BKN_SAFE_URL`,默认空=ISF、env 可回退(commit `9dd2f1de`,vega/bkn/DA/mf-model×2 + exec-factory)。**待 VM**:设 shadow 收 diff → 逐服务翻 bkn-safe。
- [ ] **E4.** introspect:各服务 hydra-admin endpoint 指向新 hydra(per-cluster values,deploy 时改)。
- [x] **E5.** user-mgmt 调用方切 bkn-safe directory —— 代码已备(D1/D2/D3 + `DIRECTORY_PROVIDER` 开关),随 E3 一起翻。

## 阶段 F —— anyshare 剔除(产品已确认文档库无人用)
- [ ] flow-automation 删 33 个 `@anyshare/*` dataflow 动作 + `drivenadapters/{eacp,anyshare,doc,doc_share,authentication}.go` + actionMap;bkn-safe 不实现 eacp/jwt。

## 阶段 G —— 退役 ISF
- [ ] 三层全切 + 对账无差 → 下线 ISF 11 服务 + hydra-fork。

---

## 依赖与顺序
```
A(mf-model shadow)──┐
                     ├─→ E2 收 diff ─→ E3 翻权威(需 B)
B(全适配器)─────────┘
C(补端点)──→ D(user-mgmt 切)──→ E5
E1(部署)前置一切线上动作
F(anyshare)与 D4 一起；G 最后
```

## 建议起步
**A1(mf-model shadow,补齐 8/8)** + **B1(vega/bkn/pipeline 全适配器,一份复用)** —— 都纯代码、可回退、不动线上。然后 E1 部署 + E2 收 diff。

## 状态基线(已完成)
bkn-safe 本体 + 6 Go authz shadow + exec-factory 全适配器 + helm/k3s 部署 + 集群内 19/19 + 端到端登录流(显式 consent)+ §4/矩阵等价 + DESIGN.md/API.md。commit 链至 `795c38ce`。
