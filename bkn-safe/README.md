# bkn-safe

bkn-safe 是 OpenBKN 的认证、鉴权和用户目录服务。它配合**上游 ORY Hydra**
工作：Hydra 签发 token，bkn-safe 提供 login/consent/device 流程，并承担权限判定
与用户目录管理。

**文档**：
- 领域知识网络权限调用合同：[`docs/api/knowledge-network-authorization.md`](../docs/api/knowledge-network-authorization.md)
- 自助授权范围 OpenAPI：[`docs/api/bkn-safe/self-service.yaml`](../docs/api/bkn-safe/self-service.yaml)
- 受管 KN 代理内部 OpenAPI：[`docs/api/bkn-safe/managed-proxy.yaml`](../docs/api/bkn-safe/managed-proxy.yaml)
- 全局设计文档：[bkn-docs `docs/foundry/`](https://github.com/openbkn-ai/bkn-docs/tree/main/docs/foundry)

## 三职责

1. **认证** —— hydra 的 login/consent/device 验证页；用**自有用户库 + bcrypt** 验密码
   （不调 eacp/anyshare）；在 consent 时把 introspect 的 `ext` claims 注入 token session。
2. **鉴权** —— Casbin（RBAC + 资源实例，`keyMatch`，只 allow），policy 存 GORM（gorm-adapter）。
3. **用户管理** —— 自建目录（users/departments/groups/roles）+ 名称解析 + LDAP 连接器（轻）。

## 目录

```
bkn-safe/
  server/                 服务本体 (module bkn-safe, go 1.25)
    main.go               装配: db -> migrate -> authz -> seed -> http
    config/               环境变量配置
    internal/
      model/              GORM 领域模型
      database/           openbkn-rds + GORM 连接 + migrate
      authz/              Casbin 引擎 (Check/AllowedOps/grant/role-binding)
      seed/               集中 seed (角色/资源类型/操作/权限, 内置 JSON)
      auth/               用户库(bcrypt) + hydra 客户端 + login/consent 编排 + LDAP
      directory/          用户目录查询服务
      httpapi/            gin 路由: health + authz API + provider 页 + directory API
  contract/               契约测试 (introspect 过真实 lib + Casbin 等价)
  dev/                    本地/VM dev 栈 (hydra/PostgreSQL + bkn-safe/MariaDB)
```

## 构建 / 测试

```bash
# 用 gvm go1.25.6 (见 memory: go-env-gvm)
cd server && go build ./... && go test ./...
```

## 跑起来（dev 栈，在 VM 上）

dev 栈把上游 hydra(PostgreSQL) + bkn-safe(MariaDB) 一起拉起，bkn-safe 接成 hydra 的 login/consent provider。

```bash
# 交叉编译 (CGO 关) 给 VM 的 linux/arm64
cd server && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o ../dev/bkn-safe .
# 在 VM (parallels@10.211.55.4) 上:
cd dev && docker compose up -d --build      # postgres + mariadb + hydra v26.2.0 + safe
./seed-clients.sh                            # 注册 OAuth client
./validate-e2e.sh                            # 端到端: 登录->授权->token->introspect 验 ext
```

`validate-e2e.sh` 已验证：authcode 全流程产出的 token，introspect 的 `ext` =
`{visitor_type:realname, login_ip, udid:"", account_type:other, client_type:web}`，
逐字匹配冻结的兼容契约夹具。

## 配置

支持 **YAML 配置文件**、**环境变量**（覆盖文件），以及 `-config` 启动参数。

### 配置文件（本地调试推荐）

```bash
cd server
cp config/config.local.yaml.example config/config.local.yaml
# 编辑 config/config.local.yaml（数据库、Hydra 等；该文件已 gitignore）

go run . -config config/config.local.yaml
# 或
set SAFE_CONFIG=config/config.local.yaml   # PowerShell: $env:SAFE_CONFIG=...
go run .
```

VS Code / Cursor：打开 `bkn-safe` 根目录，选 **Run and Debug → bkn-safe (config.local.yaml)**（见 `.vscode/launch.json`）。

解析顺序：**默认值 → YAML 文件 → `SAFE_*` 环境变量**（环境变量优先级最高）。

### 环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `SAFE_CONFIG` | （空） | YAML 配置文件路径（与 `-config` 等效；命令行 `-config` 优先） |
| `SAFE_HTTP_ADDR` | `:3000` | 监听地址 |
| `SAFE_DB_TYPE` | `MySQL` | MySQL/DM8/KDB9（都走 openbkn-rds driver） |
| `SAFE_DB_HOST/PORT/USER/PASSWORD/NAME` | 127.0.0.1/3306/safe/secret/safe | 数据库 |
| `SAFE_HYDRA_ADMIN_URL` | `http://127.0.0.1:4445` | hydra admin（内网） |
| `SAFE_HYDRA_PUBLIC_URL` | `http://127.0.0.1:4444` | hydra public |
| `SAFE_LDAP_URL` | （空=禁用） | LDAP 联邦；配了则 local→LDAP 链式认证 |
| `SAFE_SEED_ON_START` | `true` | 启动时灌角色/资源类型/操作/权限 |

## HTTP 接口（节选）

- 认证（hydra 重定向到这里）：`GET/POST /login`、`GET /consent`、`GET/POST /device`
- 鉴权 `/api/safe/v1/authz`：`POST /check`、`POST /operations`、`POST /resource-filter`、
  `POST|DELETE /policies`、`POST /role-bindings`
- 受管 KN 代理（ClusterIP 内部面）`/api/safe/in/v1/managed-proxy-accounts`：创建、查询、
  禁用和归档一对一 proxy app；账号禁止登录、AppKey、普通用户管理与通用 Policy 授权
- 目录 `/api/safe/v1/directory`：`GET /users/:id`、`POST /names`、`GET /departments`、
  `GET /groups/:id/members`、`POST /search-org`、`POST /users`、`PUT /users/:id/password`
- 健康：`GET /health/ready`、`/health/alive`

### `POST /api/safe/v1/authz/resource-filter`（列表页批量判定）

一次请求判完一页资源：既回**哪些可见**，也回**每个资源上持有哪些操作**。业务服务的
列表/详情响应据此填 `operations` 字段，无需按资源、按操作逐次调 `/check`。

```json
{
  "accessor_id": "u-1",
  "resources": [{ "type": "knowledge_network", "id": "kn-1" }],
  "visibility_operations": ["view_detail"],
  "candidate_operations": ["view_detail", "create", "modify", "delete"]
}
```

```json
{
  "resources": [
    {
      "resource_type": "knowledge_network",
      "resource_id": "kn-1",
      "operations": ["view_detail", "modify", "delete"]
    }
  ]
}
```

**两个操作列表是彼此独立的两个维度，这正是本端点存在的理由：**

| 字段 | 作用 | 留空时 |
| --- | --- | --- |
| `visibility_operations` | 过滤：资源需**全部**持有这些操作才返回 | 不过滤，请求的资源全部返回（各自带操作集，可能为空） |
| `candidate_operations` | 投影：返回的 `operations` 从该候选集中取子集，与资源因何可见无关 | 回落到该资源类型的操作目录（与 `POST /operations` 一致） |

资源可用 `resources: [{type,id}]` 给出，也可用 `resource_type` + `resource_ids`
的单类型形式；两者可同时出现，一次请求内允许混合资源类型。重复资源和操作会按首次
出现位置去重，响应顺序稳定。

本端点不设置固定的资源数、operation 数或矩阵单元 hard cap，也不承诺固定规模
超限返回 413。调用方可以按实时负载动态分块；任一分块超时、失败或返回不完整时，
上层业务必须整体失败，不能返回部分列表或部分查询结果。

判定语义与单条 `POST /check` 完全一致：直接授权、角色继承（含角色间传递）、
根部门公共授权、超管通配、`act` 通配一视同仁。内部不逐 (资源 × 操作) 调用引擎，
而是先解析该访问者的授权集再在内存中投影，故耗时与资源数近似线性；两条路径由
`TestFilterResourceOpsMatchesCheck` 钉住一致性。

账号不存在或已禁用时正常返回空集合；请求体不合法或缺 `accessor_id` 返回 `400`；
给了 `resource_ids` 却没给 `resource_type` 返回 `400`；账号状态存储不可用返回
`503`；其他引擎失败返回 `500`。**空资源列表不是错误**，返回
`{"resources": []}`，分页调用方无需特判。

## 授权档位（付费能力门控）

付费能力按**档位**放行，不按证书里的 feature key —— 唯一判定入口是
`entitlement.AtLeast(min)`，`community ⊂ professional ⊂ enterprise ⊂ industry`。
证书里的 `features[]` 照签，但**只供展示与审计核对，任何代码路径不得据此放行**。

档位不够时端点**伪装不存在**（404，与社区镜像逐字节一致），不是 403 —— 403 等于
告诉探测者"这里有个付费端点"。升级引导走 `GET /api/safe/v1/capabilities`。

### `-tags ee_dev` 与 `OPENBKN_EDITION` 对 bkn-safe 无效

别的服务能用这两个东西在验证集群上切档位，**bkn-safe 不行**。它是集群的持证方，
不是消费方：`app.Boot` 把 gate 直接接到自己的 `licSvc`（`license.Gate`），依赖图里
根本没有那个环境变量桩可供覆盖。

所以 bkn-safe 的档位只有两条路：

| 场景 | 怎么切 |
|---|---|
| 集群 / 验证环境 | 导入**真证书**（`POST /api/safe/v1/admin/license/import`） |
| 单元测试 | `entitlement.SetGateForTest(license.Gate(svc))`，或直接给一个假 gate |

写在这里是因为它必然被踩：设了 `OPENBKN_EDITION=enterprise` 而档位纹丝不动，
不知道这条的人会先去查环境变量有没有传进容器、再查 gate 有没有装，半天过去了。

设计：bkn-docs `docs/shared/licensing/ee-design.md`、
`docs/foundry/bkn-safe/design/issue-unknown-bkn-safe-edition-gating.md`。

## 注意

- **绝不用 gobuffalo/pop** —— pop 按方言名分发，是 hydra 信创 fork 的坑根；GORM 吃 driver 层，openbkn 透明。
- **角色 UUID 保号**（`internal/seed/data/roles.json`，9 个；DA/flow-automation 硬编码业务 3 个）。
- introspect 的 user-type `ext` 5 字段必须齐全，否则旧 lib 解析 panic（无 nil 检查）——
  `ExtClaims` 保证这一点，`contract/` 用真实 lib 守护。
