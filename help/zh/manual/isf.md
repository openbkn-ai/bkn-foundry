# 🔐 Info Security Fabric（ISF）

## 📖 概述

**Info Security Fabric** 是**横切的安全层**：在数据访问、模型输出与工具调用上提供统一的**身份**、**权限**、**策略**与**审计**。完整安装可能对接 OAuth2/OIDC（如 Hydra）与业务域服务。

使用 **`--minimum` 安装**时，多数认证组件关闭，便于实验环境快速上手，部分 API 可能无需 Token。生产环境请按随产品提供的部署与安全文档启用完整认证配置。

**相关模块：** 所有接受 `Authorization` 的子系统；主要消费者包括 [VEGA 引擎](vega.md)。

### 🛡️ 管理员工具：openbkn admin

ISF 在**完整安装**下（启用 `auth.enabled=true` 与 `businessDomain.enabled=true`）的日常**管理面**（用户、组织、角色、模型、审计）通过 `openbkn admin` 子命令组操作 — 与本页下文面向终端用户的 `openbkn` 命令同属 [`@openbkn/bkn-sdk`](https://github.com/openbkn-ai/bkn-sdk)，无需安装独立 CLI。

```bash
npm install -g @openbkn/bkn-sdk                     # Node.js 22+
openbkn auth login https://<访问地址> -k

openbkn admin org tree                              # 查看部门
openbkn admin user create --login alice            # 默认密码 123456，首次登录强制改密
openbkn admin user assign-role <userId> <roleId>
openbkn admin user reset-password -u alice         # 管理员重置
openbkn admin role list
openbkn admin audit list --user alice --start 2026-04-01 --end 2026-04-30
```

> 完整子命令组（`org` / `user` / `role` / `llm` / `small-model` / `audit` / `call`）见 `openbkn admin --help`；管理员 token 与普通用户 token 一并保存在 `~/.bkn/`（保持隔离）。与最小化安装的兼容说明详见 [安装与部署 — 完整安装后的管理员工具](../install.md#-完整安装后的管理员命令openbkn-admin)。
>
> 内置「三权分立」账号 `system / admin / security / audit` 不可随意删改；操作员请使用**个人账号**而非共享 `admin`，便于审计追溯。

### 💻 CLI

#### 登录

```bash
# 标准登录（跳过 TLS 证书验证）
openbkn auth login https://kweaver.example.com -k

# 登录并设置别名，方便多环境切换
openbkn auth login https://kweaver.example.com --alias prod -k

# 使用用户名密码直接登录（非交互式）
openbkn auth login https://kweaver.example.com \
  -u <用户名> -p '<密码>' -k

# 最小化安装时跳过认证
openbkn auth login https://localhost:30000 --no-auth -k

# 显式使用 HTTP 用户名密码登录（无需浏览器与 Node/Chromium）
openbkn auth login https://kweaver.example.com \
  -u <用户名> -p '<密码>' --http-signin -k

# 无浏览器交互登录：CLI 打印 OAuth URL，复制到任意带浏览器的设备登录后，
# 将地址栏完整回调 URL（或授权码）粘贴回终端
openbkn auth login https://kweaver.example.com --no-browser -k

# 首次登录强制改密（非交互一次性完成）：服务端要求重置初始密码时使用
openbkn auth login https://kweaver.example.com \
  -u <用户名> -p '<初始密码>' --new-password '<新密码>' -k
```

> 🔑 **首次登录可能要求修改密码**：当账号仍使用初始密码时，服务端会返回错误码 **`401001017`**，CLI 行为如下：
> - **TTY（交互终端）**：自动确认后引导你输入新密码（6–100 字符），改密成功后自动重试登录，无需手动重跑。
> - **非 TTY（脚本 / CI）**：不会弹提示，请改用上面的 `--new-password '<新密码>'` 一次性完成；或先用 [`openbkn auth change-password`](#-修改密码) 改密后再正常登录。
>
> 改密后旧的初始密码立即失效，请同步更新自动化脚本与 CI 中保存的密码。

#### 会话管理

```bash
# 列出所有已保存的登录会话
openbkn auth list

# 切换当前使用的会话（按别名）
openbkn auth use prod

# 列出当前会话下的用户列表
openbkn auth users

# 切换到不同用户
openbkn auth switch --user analyst@example.com

# 查看当前登录身份
openbkn auth whoami

# 查看当前会话的详细状态（Token 有效期、刷新状态等）
openbkn auth status
```

**`auth whoami` 与 no-auth**：`whoami` 需 OAuth 登录写入的 `id_token`。若会话为 **`auth login … --no-auth`** 或平台关闭鉴权，CLI 为 **no-auth**，`whoami` 会报错提示无 `id_token`，属正常；请用 `auth status` 确认模式，勿与登录失败混淆。

```bash
# 导出当前会话的 Token（用于脚本或 CI/CD）
openbkn auth export
```

在已登录会话下，REST 调用可直接使用 **`openbkn token`**（与 `openbkn auth export` 均可得到 Bearer 串；示例优先用前者）：

```bash
curl -sk "https://<访问地址>/api/vega-backend/v1/catalogs" \
  -H "Authorization: Bearer $(openbkn token)"
```

#### 登出与删除

```bash
# 登出当前会话（Token 失效，本地凭据保留）
openbkn auth logout

# 删除已保存的会话（同时清理本地凭据）
openbkn auth delete prod
```

#### 🔑 修改密码

通过 `openbkn auth change-password` 直接修改账号密码，**无需依赖**已保存的 OAuth Token，底层调用 EACP `POST /api/eacp/v1/auth1/modifypassword`，新密码长度 **6–100 字符**。

```bash
# 完整参数（非交互式，适合脚本/CI）
openbkn auth change-password https://kweaver.example.com \
  -u <用户名> -o '<旧密码>' -n '<新密码>' -k

# 省略 <url>：使用当前激活平台（openbkn auth use 设置的）
openbkn auth change-password -u <用户名> -o '<旧密码>' -n '<新密码>'

# 交互式：在 TTY 下省略 -o / -n，会以隐藏输入方式提示；
# 省略 -u 时会用当前激活账号（token.json 的 displayName），并先做 yes/no 确认
openbkn auth change-password prod
```

| 参数 | 说明 |
|------|------|
| `<url>` | 平台地址或别名；省略时使用当前激活平台。无激活平台则报错退出。 |
| `-u <account>` | 账号名。**TTY** 下省略时会确认是否使用当前激活账号；**非 TTY** 下必须显式提供（避免脚本误改账号）。 |
| `-o <old>` | 旧密码；TTY 下可省略以隐藏方式输入。 |
| `-n <new>` | 新密码（6–100 字符）；TTY 下可省略以隐藏方式输入。 |
| `--insecure` / `-k` | 跳过 TLS 校验；省略时沿用登录平台时保存的偏好。 |

> ⚠️ **初始密码（错误码 401001017）**：服务端要求重置初始密码时，普通的 `openbkn auth login -u … -p …` 会失败。
> - **TTY**：CLI 会确认后引导你输入新密码并自动重试登录。
> - **非 TTY**：请用 `openbkn auth login <url> -u <用户名> -p '<旧密码>' --new-password '<新密码>'` 在登录的同时一次性完成首次密码设置。

> 💡 交互式登录与改密属于 CLI 范畴（浏览器 PKCE / 无头 OAuth），请使用上面的 `openbkn auth` 子命令；TypeScript SDK 不处理登录与改密，而是显式接收 token（见下方「TypeScript SDK」一节）。
>
> 💡 失败时报错信息会附带 `(account="<用户名>")`，方便快速定位是哪个账号失败。

#### 多账户工作流

```bash
# 1. 登录生产环境
openbkn auth login https://prod.kweaver.example.com --alias prod -k

# 2. 登录开发环境
openbkn auth login https://dev.kweaver.example.com --alias dev -k

# 3. 查看所有会话
openbkn auth list

# 4. 切换到生产环境
openbkn auth use prod

# 5. 确认身份
openbkn auth whoami

# 6. 在生产环境操作
openbkn agent list --limit 5

# 7. 切换到开发环境
openbkn auth use dev

# 8. 在开发环境操作
openbkn agent list --limit 5
```

#### 配置与业务域

```bash
# 显示当前完整配置
openbkn config show

# 列出所有已配置的业务域
openbkn config list-bd

# 设置当前业务域
openbkn config set-bd bd_sales
```

**`config list-bd` / `config set-bd` 与最小化安装**：**`--minimum` / 最小化安装** **不包含**这两条子命令依赖的**业务域管理服务**（未随最小化部署），`list-bd` 常 **404** 等，属部署裁剪，不是 CLI 故障。平台仍有默认业务域，请用 `config show` 查看。**完整安装**下再用 `list-bd` / `set-bd` 枚举或切换域；若仍失败，再查网关或相关服务。

**业务域优先级说明**：当设置了业务域后，所有 API 调用会在请求头中携带 `X-Business-Domain` 字段。平台根据此字段进行数据隔离与权限控制。优先级为：命令行 `--bd` 参数 > `openbkn config set-bd` 配置 > 默认业务域。

```bash
# 命令级覆盖业务域
openbkn agent list --bd bd_finance

# 查看当前生效的业务域配置
openbkn config show | grep business_domain
```

#### 端到端流程

```bash
# 1. 首次登录
openbkn auth login https://kweaver.example.com --alias prod -k -u <用户名> -p <密码>

# 2. 确认身份
openbkn auth whoami

# 3. 设置业务域
openbkn config set-bd bd_sales

# 4. 开始使用平台功能
openbkn bkn list --limit 5
openbkn agent list --limit 5

# 5. 会话结束后登出
openbkn auth logout
```

---

### TypeScript SDK

交互式登录（浏览器 PKCE / 无头 OAuth）属于 CLI 范畴 —— 请先运行 `openbkn auth login`。SDK 显式解析凭据：要么向 `createClient` 传入 token，要么让它读取 `~/.bkn/` 里的 CLI 会话。会话状态通过独立的 `auth` 命名空间访问。

```typescript
import { createClient, auth } from '@openbkn/bkn-sdk';

const bkn = createClient({ baseUrl: 'https://<访问地址>', token: process.env.BKN_TOKEN });

// 查看当前会话（来自 ~/.bkn/ 或传入的 token）
const status = auth.status();
console.log('平台:', status.baseUrl, '是否有 token:', status.hasToken, '是否过期:', status.expired);

const me = auth.whoami();
console.log(me.userId, me.username);

// 列出可用业务域（无 typed 方法 —— 用通用 passthrough）
const domains = await bkn.call('/api/bkn-backend/v1/business-domains', { method: 'GET' });
console.log('业务域:', domains);

// 将后续请求限定到某个业务域
const scoped = createClient({
  baseUrl: 'https://<访问地址>',
  token: process.env.BKN_TOKEN,
  businessDomain: 'bd_sales',
});
const catalogs = await scoped.call('/api/vega-backend/v1/catalogs', { method: 'GET' });
console.log('catalogs:', catalogs);
```

---

### curl

```bash
# OAuth2 Token 获取（密码模式，适用于启用完整认证的环境）
curl -sk -X POST "https://<访问地址>/oauth2/token" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "grant_type=password&username=admin&password=MySecurePassword&client_id=openbkn-sdk&scope=openid"

# 使用 Token 访问受保护资源
curl -sk "https://<访问地址>/api/vega-backend/v1/catalogs" \
  -H "Authorization: Bearer $(openbkn token)"

# 查看当前用户信息
curl -sk "https://<访问地址>/api/isf/v1/userinfo" \
  -H "Authorization: Bearer $(openbkn token)"

# 发现 OpenID 配置
curl -sk "https://<访问地址>/.well-known/openid-configuration"

# Token 内省（检查 Token 有效性）
curl -sk -X POST "https://<访问地址>/oauth2/introspect" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "token=<access-token>&client_id=openbkn-sdk"

# 刷新 Token
curl -sk -X POST "https://<访问地址>/oauth2/token" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "grant_type=refresh_token&refresh_token=<refresh-token>&client_id=openbkn-sdk"

# 最小化安装 — 无需 Token 直接访问
curl -sk "https://localhost:30000/api/vega-backend/v1/catalogs"
```
