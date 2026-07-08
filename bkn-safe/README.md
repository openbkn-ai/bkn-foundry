# bkn-safe

ISF 替换的自研认证/鉴权/用户管理服务（代码内代号 `safe`）。配合**上游 ORY Hydra**
工作：hydra 签发 token，bkn-safe 是 hydra 的 login/consent/device 提供方，并承担鉴权
与用户目录。

**文档**：
- [`docs/DESIGN.md`](docs/DESIGN.md) — 设计文档（架构/组件/数据模型/鉴权模型/认证流程/seed/部署）
- [`docs/API.md`](docs/API.md) — HTTP API 参照（authz / directory / user-write / provider 页）
- 替换全局背景：[`../docs/isf-replacement/README.md`](../docs/isf-replacement/README.md)

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
      database/           proton-rds + GORM 连接 + migrate
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
逐字匹配 §1 契约与真实 ISF golden。

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
| `SAFE_DB_TYPE` | `MySQL` | MySQL/DM8/KDB9（都走 proton-rds driver） |
| `SAFE_DB_HOST/PORT/USER/PASSWORD/NAME` | 127.0.0.1/3306/safe/secret/safe | 数据库 |
| `SAFE_HYDRA_ADMIN_URL` | `http://127.0.0.1:4445` | hydra admin（内网） |
| `SAFE_HYDRA_PUBLIC_URL` | `http://127.0.0.1:4444` | hydra public |
| `SAFE_LDAP_URL` | （空=禁用） | LDAP 联邦；配了则 local→LDAP 链式认证 |
| `SAFE_SEED_ON_START` | `true` | 启动时灌角色/资源类型/操作/权限 |

## HTTP 接口（节选）

- 认证（hydra 重定向到这里）：`GET/POST /login`、`GET /consent`、`GET/POST /device`
- 鉴权 `/api/safe/v1/authz`：`POST /check`、`POST /operations`、`POST|DELETE /policies`、`POST /role-bindings`
- 目录 `/api/safe/v1/directory`：`GET /users/:id`、`POST /names`、`GET /departments`、
  `GET /groups/:id/members`、`POST /search-org`、`POST /users`、`PUT /users/:id/password`
- 健康：`GET /health/ready`、`/health/alive`

## 注意

- **绝不用 gobuffalo/pop** —— pop 按方言名分发，是 hydra 信创 fork 的坑根；GORM 吃 driver 层，proton 透明。
- **角色 UUID 保号**（`internal/seed/data/roles.json`，9 个；DA/flow-automation 硬编码业务 3 个）。
- introspect 的 user-type `ext` 5 字段必须齐全，否则旧 lib 解析 panic（无 nil 检查）——
  `ExtClaims` 保证这一点，`contract/` 用真实 lib 守护。
