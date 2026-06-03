# bkn-safe 实现计划 (2026-06-03)

> 分支 `feat/isf-replacement`。综合:本会话全部决策 + `isf-excision-migration-plan`(ADR)+ `isf-replacement-landing-design` §9 阶段 + 契约冻结。
> 目标:用 **上游 hydra + bkn-safe** 剔除 ISF 服务系列(authentication/authorization/user-management/eacp/sharemgnt/oauth2-ui/policy-management/audit-log/isfweb/authentication-jwt/hydra-fork),**简化**(11→2)。

## 总览:bkn-safe 三职责 + 一个 hydra

```
                 ┌── hydra (上游 v26.2.0, 不改) ──┐  token 签发/内省 (admin :4445 内网, public :4444)
 9 应用 ─────────┤                                  │  + device flow (RFC8628)
                 └── bkn-safe (自研, GORM+proton) ──┘
                       ① 认证: login/consent/device 验证页 + 自有用户库验密码(bcrypt) + 注 ext claims
                       ② 鉴权: Casbin (gorm-adapter) + 集中 seed(角色/资源类型/操作/权限)
                       ③ 用户管理: 目录(users/depts/groups/roles) + LDAP 连接器(轻)
```

迁移按层(爆炸半径):**introspect 保兼容(8 服务)** / **authz 重做(5 服务)** / **user-mgmt 重做或保(~4 服务)** / **eacp 切断**。逐服务切 + 影子比对 + 角色 UUID 保号。

---

## 阶段

### Phase 0 — 契约冻结 ✅ 完成
冻结 introspect/authz/user-mgmt 契约 + 角色 UUID,可执行 contract test(introspect 过真实 lib v1.0.5、Casbin 等价、抓到 keyMatch2 越权 bug)、ISF 真实 golden 取证。
产物:`docs/isf-replacement/contracts/`、`bkn-safe/contract/`。

### Phase 1 — 标准 hydra 就位 ✅ 完成
上游 hydra v26.2.0 + PostgreSQL(VM 持久 dev 栈)+ bkn-safe on MariaDB,seed client(ci-runner/kweaver-cli),smoke PASS(client_credentials introspect + device/auth)。
产物:`bkn-safe/dev/`。

### Phase 2 — bkn-safe 服务骨架
- Go module `bkn-safe`(顶层),go1.25,**GORM + proton-rds driver**(信创免费),**零 kweaver-go-lib**。
- 布局:仿 bkn-backend(config/driveradapters/drivenadapters/domain/...),health 端点,configmap/chart。
- 接 hydra:`ory/hydra-client-go/v2`(admin client,login/consent/device accept)。
- DB migration(GORM)建表:users/credentials/departments/groups/memberships/roles + casbin policy 表。
- **验收**:服务起得来,连 MySQL,/health/ready 通,能调通 hydra admin。
- **失败条件**:proton driver 接 MySQL/达梦不通;pop 误入(禁)。

### Phase 3 — 认证(login/consent provider + device 验证页)
- 3 个页面:登录(`login_challenge`→验密码→accept login)、授权(`consent_challenge`→展示 scope→accept/reject)、设备验证(`/device`→输 user_code→登录→授权)。
- **自有用户库验密码**(bcrypt),**不调 eacp**。
- consent accept 时**注入 ext claims**:`visitor_type`(realname/app/anonymous)、`login_ip`、`udid`(可空)、`account_type`、`client_type` —— 满足 §1 introspect 契约(否则 lib panic)。
- grant:device_code(人)、client_credentials(CI)、password fallback。
- **验收**:`kweaver` CLI 对**标准 hydra**走 device flow + password 登录拿到 user token;introspect 该 token,字段被 `bkn-safe/contract` 的 introspect test 判为合规(visitor_type=realname 等)。
- **失败条件**:introspect 缺 ext 字段致 lib panic;consent 注入与契约不符。

### Phase 4 — 鉴权(Casbin + 集中 seed + 干净 API)
- Casbin model(RBAC + 资源实例,**keyMatch 非 keyMatch2**,effect only allow)+ **gorm-adapter**(policy 存共享 DB)。
- **集中 seed**(取代 ISF 的"服务启动 seed + 各模块 HTTP 注册 + DA InitPermission"散点):bkn-safe 启动一次性灌
  - **角色**:role.json 9 个(**保 UUID**:数据 `00990824-`/AI `3fb94948-`/应用 `1572fb82-` + 6 system)。
  - **资源类型 + 操作目录**:agent(use/mgnt_built_in_agent/publish…)、pipeline、catalog/connector… —— 从 DA/pipeline-mgmt/vega 现注册代码扒成内置 seed。
  - **角色权限**:DA `GrantAgentUsePmsForAppAdmin`/`GrantMgmtPmsForAppAdmin` 等迁成 Casbin policy。
- **干净 authz API**(重设计,弃 ISF 怪癖:GET-in-body/数组-map/policy-delete 双形态/public-private 割裂):operation-check、resource-operation、policy CRUD、resource-list/filter。
- **验收**:`bkn-safe/contract` 的 Casbin 等价 test 全绿;集中 seed 后判定与 ISF golden 逐条一致(影子比对)。
- **失败条件**:任一判定偏差;角色 UUID 漂移致硬编码引用断;keyMatch 越权。

### Phase 5 — 用户管理目录 + LDAP
- 表 + 查询 API:users/apps/names(v1/v2)/emails/departments/internal-groups/group-members/search-org(读为主,写仅 apps/internal-groups/group-members)。
- **LDAP 连接器**(go-ldap,登录时联邦,"轻");重度 IAM 延后。
- 决策:**全新接口 vs 保 ISF 13 端点契约**(OPEN #1,倾向重设计求简)。
- **验收**:调用方(bkn/vega/flow/DA)能拿到所需用户/组织信息;若保契约则比对冻结的 user-management.md golden。
- **失败条件**:目录数据缺失致应用功能断。

### Phase 6 — 逐服务迁移 + 影子比对
顺序(耦合面小→大):
1. **authz**:exec-factory → DA → dataflow → vega → bkn。每服务替换 drivenadapter 适配 → 影子比对(bkn-safe Casbin vs ISF authorization,同请求 diff)→ flip。
2. **user-mgmt**:bkn/vega/flow/DA 适配层替换 + 比对。
3. **introspect**:各服务 endpoint 指向新 hydra/bkn-safe(保兼容,仅改配置)。**OPEN #2**:是否顺手现代化为 JWT 本地验签(改 8 服务,减依赖+去往返)。
4. **eacp/anyshare**:切断密码登录依赖;flow-automation 文档 ACL 按 **OPEN #3** 决策。
- **验收**:每服务切后影子比对零差;9 应用编译/测试通过。
- **失败/回滚**:endpoint 配置切回 ISF(并行期保留)。

### Phase 7 — 退役 ISF
对账无差 → 下线 ISF 11 服务 + hydra-fork。
- **验收**:全栈仅剩 hydra(上游)+ bkn-safe;鉴权/认证/目录全由新栈承接。

---

## 横切关注

- **角色 UUID 保号**:贯穿全程(role.json 9 个;DA `inner_role.go` 1572fb82、flow-automation `perm_policy.go` 00990824 硬编码),seed 沿用,免改硬编码 + 数据迁移。
- **信创**:bkn-safe 走 proton driver(免费);hydra 用独立 PostgreSQL,信创延后且隔离(OPEN #4)。
- **kweaver-go-lib**:bkn-safe 零依赖;应用保留(logger/audit/rest);introspect 现代化 = OPEN #2。
- **审计**:应用经 `kweaver-go-lib/audit` 发 Kafka;替换 = 换消费者或保 audit-log,应用零改(§6 契约)。

## OPEN(需拍板)

1. user-mgmt 全新接口 vs 保 13 端点契约(倾向全新)。
2. introspect 保兼容(默认)vs JWT 本地验签现代化(改 8 服务)。
3. anyshare(eacp 文档 ACL,仅 flow-automation)去留。
4. 信创是否强制 hydra DB(默认否,hydra 用独立 PostgreSQL)。
5. 重度 IAM/多协议联邦(以后再说)。
6. bkn-safe 归属:Core 服务 vs 独立可选组件(建议后者,沿 ISF 定位)。

## 当前状态
Phase 0/1 完成并推 origin(commit 链至 `5d833f66`)。ISF 全栈 + 标准 hydra dev 栈均在 VM 跑。下一步:Phase 2 骨架。
