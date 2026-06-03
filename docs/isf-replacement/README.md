# ISF 替换 / bkn-safe —— 总览（同事入口）

> 状态：设计已定，bkn-safe 本体（Phase 2-5）已实现并端到端验证通过；尚未迁移生产服务。
> 跟踪：GitHub epic [#21](https://github.com/openbkn-ai/bkn-foundry/issues/21)。分支 `feat/isf-replacement`。

## 一句话

用 **上游 ORY Hydra（不 fork）+ 一个自研服务 bkn-safe** 替换整个 ISF 服务系列，把 **11 个服务简化成 2 个**，应用对「token 内省」零改，权限/用户管理推倒重设计成干净接口。

## 为什么

ISF（authentication / authorization / user-management / eacp / sharemgnt / oauth2-ui / policy-management / audit-log / isfweb / authentication-jwt / hydra-fork）又重又缠着 anyshare，且 hydra 是信创 fork，跟不上上游。目标是**脱离这套重组件、用上游开源、简化架构**。

## 架构

```
                 ┌── hydra (上游 v26.2.0, 原封不动) ──┐  签发/内省 token (admin :4445 内网,
 9 个 Kowell 应用 ┤                                     │  public :4444) + device flow (RFC 8628)
                 └── bkn-safe (自研, Go + GORM + proton)┘
                       ① 认证: hydra 的 login/consent/device 验证页 + 自有用户库验密码(bcrypt)
                                + 在 consent 时注入 introspect 的 ext claims
                       ② 鉴权: Casbin (gorm-adapter) + 启动集中 seed(角色/资源类型/操作/权限)
                       ③ 用户管理: 自建目录(users/depts/groups/roles) + LDAP 连接器(轻)
```

- **hydra 只签发 token**，bkn-safe 不是 token 引擎。
- **bkn-safe 三职责**：认证、鉴权、用户管理。
- **信创**：bkn-safe 走 `proton-rds` driver（达梦/金仓/MySQL 透明，零方言代码），跑现有 **MariaDB**；hydra 单独用 **PostgreSQL**（见下，MariaDB 装不了上游 hydra），信创延后且隔离。

## 关键决策（2026-06-03 定）

| 主题 | 决策 |
|---|---|
| token 引擎 | 上游 hydra **v26.2.0**（CalVer；device flow 在 v26.x，**不是 v2.3.0**），**不 fork** |
| hydra DB | **PostgreSQL**（独立小库）。MariaDB **任何版本都装不了上游 hydra**：migration 用 MySQL 专有 `CAST(... AS JSON)`，MariaDB 永不支持（MDEV-26448）。PG 是 hydra 一等后端，且金仓 KingbaseES 基于 PG → 未来信创更顺。信创延后 |
| bkn-safe DB | **GORM + proton-rds driver**，跑现有 **MariaDB**；绝不用 gobuffalo/pop（pop 才是 hydra 信创 fork 的坑根） |
| 鉴权 | **Casbin 推倒重做**（不复刻 ISF authz 契约）；`keyMatch` 非 keyMatch2；只 allow |
| 用户管理 | **bkn-safe 自建（GORM）**，不上 IAM 产品；外部对接 **LDAP（轻）**，重度 IAM 延后 |
| 认证 | bkn-safe **自有用户库验密码（bcrypt）**，切断 eacp/anyshare 依赖 |
| introspect | **保兼容**（8 服务耦合深，应用零改）；JWT 本地验签现代化为可选项（OPEN） |
| 角色 UUID | **保号**（role.json 9 个；DA/flow-automation 硬编码业务 3 个） |
| 初始化 | **集中 seed**：取代 ISF「服务启动 seed + 各模块 HTTP 注册 + DA InitPermission」散点，bkn-safe 启动一处灌 |

## 按层迁移（剔除 ISF 的策略）

剔 ISF 的爆炸半径按层不均，分层处理（详见 `isf-excision-migration-plan`）：

| 层 | 调用方 | 策略 |
|---|---|---|
| introspect/token | 8 服务 | 🔴 保兼容，不重设计 |
| authz | 5 服务 | 🟡 Casbin 重做，改适配层 + 迁 policy |
| user-mgmt | ~4 服务 | 🟡 全新接口，改适配层 |
| eacp/anyshare | 仅 flow-automation | 切断密码登录耦合；文档 ACL 随 anyshare 决策 |

逐服务切 + 影子比对（冻结契约 + golden 作回归预言机）+ 角色 UUID 保号 + 失败回滚（配置切回 ISF）。

## 进度

- ✅ **Phase 0** 契约冻结 + 可执行 contract test + ISF 真实 golden 取证（`contracts/`、`../../bkn-safe/contract/`）
- ✅ **Phase 1** 标准 hydra v26.2.0 + MySQL dev 栈（`../../bkn-safe/dev/`，smoke PASS）
- ✅ **Phase 2-5** bkn-safe 本体：骨架 + Casbin 鉴权 + 集中 seed + 认证(login/consent/device) + 用户目录 + LDAP；单测全绿；**端到端验证通过**（authcode 全流程 → introspect ext 逐字匹配契约）
- ⏳ **Phase 6** 逐服务迁移（未开始，触生产代码，需影子比对）
- ⏳ **Phase 7** 退役 ISF

## 文档地图

| 文件 | 内容 |
|---|---|
| `bkn-safe-implementation-plan-2026-06-03.md` | 实现计划（Phase 0-7，验收/失败条件） |
| `isf-excision-migration-plan-2026-06-03.md` | 剔除 ISF 迁移 ADR（按层 + 逐服务） |
| `isf-replacement-landing-design-2026-05-25.md` | 落地设计（含 §11 已决/待决） |
| `isf-contract-freeze-2026-05-25.md` | 契约冻结 spec |
| `isf-interface-inventory-compat-2026-05-25.md` | ISF 接口清单 + 兼容评估 |
| `contracts/` | 冻结的权威契约：role.json、authz JSON schema、introspect golden、user-management 调用侧契约、live golden |
| `../../bkn-safe/` | bkn-safe 服务代码 + contract test + dev 栈（见 `bkn-safe/README.md`） |

## 待决策（OPEN）

1. user-mgmt 全新接口（已倾向）vs 保 ISF 13 端点契约
2. introspect 保兼容（默认）vs JWT 本地验签现代化（改 8 服务）
3. anyshare（eacp 文档 ACL，仅 flow-automation）去留
4. 信创是否强制 hydra DB（默认否）
5. 重度 IAM / 多协议联邦（以后再说）
6. bkn-safe 归属：Core 服务 vs 独立可选组件（建议后者）
