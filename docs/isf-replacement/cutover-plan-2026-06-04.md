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
- [ ] **A1. mf-model-manager + mf-model-api(Python)authz shadow**:`app/utils/permission_manager.py` 包一层,`AUTHZ_PROVIDER=shadow` + `BKN_SAFE_URL` 时并调 bkn-safe `/api/safe/v1/authz/check`,记 diff,ISF 权威。env 可回退。

## 阶段 B —— 补齐 bkn-safe 全适配器(翻"权威"前置)
当前只 shadow(ISF 权威);翻 `bkn-safe` 权威需实现各服务驱动接口的全部方法对接 bkn-safe。
- [ ] **B1. vega/bkn/pipeline-mgmt**:`PermissionAccess` 全 5 方法(CheckPermission/FilterResources/GetResourcesOperations/CreateResources/DeleteResources)→ bkn-safe。三家同构,**一份适配器复用**。
- [ ] **B2. DA**:`AuthZHttpAcc`(~20 方法,含 Grant*/ListPolicy/ResourceOperation)→ bkn-safe。
- [ ] **B3. flow-automation**:`PermPolicyHandler`(5 方法)→ bkn-safe。
- [ ] **B4. mf-model(Py)**:permission_manager 全方法 → bkn-safe。
- exec-factory:✅ 已具备(`AUTHZ_PROVIDER=bkn-safe`)。

## 阶段 C —— bkn-safe 补缺端点(切重度调用方前)
- [ ] **C1. directory**:`apps`(应用账户)、`emails`、`internal-groups`(写)—— flow-automation/mf-model 用。
- [ ] **C2. authz**:`resource-operation`(列资源可做操作,DA 用)、按需 `resource-list`(注:全局枚举语义见 §数据策略,多数场景用 ResourceFilter 代替)。

## 阶段 D —— user-mgmt 目录调用方切换(→ bkn-safe directory)
顺序按规模:
- [ ] **D1. vega + bkn**(仅 `/v2/names`)→ `directory/names`。最小,做样板。
- [ ] **D2. DA**(`/v1/users`、`/v1/search-org`)→ `directory/users/:id`、`directory/search-org`。
- [ ] **D3. mf-model(Py)**(names/users 等)。
- [ ] **D4. flow-automation**(8 端点)—— 随 anyshare 缩减一并处理(很多端点服务 anyshare 功能)。

## 阶段 E —— 部署 + 影子比对 + 翻权威(逐服务)
- [ ] **E1.** bkn-safe + hydra(PG)进生产 ns(helm,`bundledDeps=false`,指真实 hydra/PG + 现有 MariaDB);seed 角色/资源/权限;建用户 + 分角色(重定义,不迁 ISF 表)。
- [ ] **E2.** 各服务 deploy 设 `AUTHZ_PROVIDER=shadow` + `BKN_SAFE_URL`,跑真实流量,收 `[authz-shadow] DIFF` 日志。
- [ ] **E3.** diff 清(符合重定义预期)→ 逐服务翻 `AUTHZ_PROVIDER=bkn-safe`(需 B 的全适配器)。失败回滚 = 翻回 env。
- [ ] **E4.** introspect:各服务 hydra-admin endpoint 配置指向新 hydra(纯配置,保兼容)。
- [ ] **E5.** user-mgmt 调用方切 bkn-safe directory(D 完成后)。

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
