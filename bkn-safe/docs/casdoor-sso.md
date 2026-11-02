# Casdoor SSO 接入

本实现保留现有 Studio → Hydra 授权码 + PKCE 流程。Hydra 把浏览器带到 bkn-safe `/login` 后，配置为默认登录源时，bkn-safe 再跳转 Casdoor。Casdoor 回调地址必须是 **bkn-safe** 的 `GET /sso/callback/casdoor`，不是 `/studio/`。bkn-safe 验证 ID Token（RS256 签名、`iss`、`aud`、`nonce`、时间）、access token introspection 与 UserInfo 的同一 `sub`，同步本地用户和部门，再接受 Hydra login challenge。后续 consent、Studio callback 和 `/oauth2/token` 与原流程一致；Studio 最终使用的 token 仍由 Hydra 签发。

无浏览器场景调用 `POST /api/safe/v1/auth/token-exchange?provider_id=casdoor`，`Authorization: Bearer <Casdoor access token>`。bkn-safe 在 Casdoor introspection 中核对 `active`、`iss`、`exp`、`aud`、`scope`、`client_id`，用 UserInfo 核对 `sub` 并同步用户，随后用一分钟有效的签名断言请求 Hydra JWT bearer grant。Hydra token hook 填充 OpenBKN 的 `ext` 字段。bkn-safe 再 introspect 新 token，核对 `sub`、`client_id`、`aud=bkn-safe` 及必要 `ext` 字段后才返回它。调用方不接触 Hydra 客户端密钥和断言私钥。这个接口不提供 refresh token；外部 access token 过期后调用方需从 Casdoor 重新取得令牌。

## 配置顺序

1. 在 Casdoor 注册机密客户端，精确登记 `https://<openbkn-host>/sso/callback/casdoor` 为回调；允许 authorization code、PKCE S256 和所需 scopes（至少 `openid profile email`，部门 claim 按实际环境配置）。Casdoor 必须提供 discovery、JWKS、UserInfo 和 introspection，ID Token 签名算法为 RS256。若部门不在 UserInfo 中，须由客户提供 Casdoor API/字段映射后扩展适配器；当前同步只读取 UserInfo 顶层的字符串或字符串数组 claim。
2. 在 bkn-safe 容器通过 Kubernetes Secret 环境变量提供 Casdoor client secret，例如 `CASDOOR_CLIENT_SECRET`。在 bkn-safe 数据库 `sso_providers` 表创建配置。示例 SQL 仅说明字段；应按实际 issuer、域名、受众和外部应用客户端 ID 填写：

   ```sql
   INSERT INTO sso_providers
     (id, display_name, protocol, adapter, issuer, discovery_url,
      client_id, client_secret_env, redirect_uri, scopes, department_claim,
      allowed_client_ids, exchange_audience, external_required_scope, hydra_issue_scope,
      login_enabled, exchange_enabled, enabled, default_for_login,
      allow_insecure_http, created_at, updated_at)
   VALUES
     ('casdoor', 'Casdoor', 'oidc', 'casdoor',
      'https://casdoor.example', 'https://casdoor.example/.well-known/openid-configuration',
      'openbkn-safe', 'CASDOOR_CLIENT_SECRET',
      'https://openbkn.example/sso/callback/casdoor',
      'openid profile email', 'departments',
      'trusted-api-client', 'openbkn-safe', 'read', 'openid',
      true, true, true, true, false, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);
   ```

   `allowed_client_ids` 用空格分隔，约束可换发的 Casdoor 客户端。`exchange_audience` 也可用空格分隔多个允许的 audience；令牌至少包含其中一个值。`external_required_scope` 只约束 Casdoor token，多个 scope 用空格分隔且必须全部出现在 introspection 响应中；`hydra_issue_scope` 是向 Hydra 申请的新 token scope，必须被 Hydra 专用客户端及可信 issuer 允许。两者独立，例如外部 `read` 可以换发 Hydra `openid`。请先用实际令牌验证这些字段。`issuer` 必须与 discovery 和 introspection 的值逐字一致。开发环境 HTTP 地址需显式设置 `allow_insecure_http=true`，生产使用 HTTPS。数据库配置可在不同安装中独立维护，但新客户端密钥仍需作为 Secret 供给。

   升级时 bkn-safe 自动增加两个 scope 字段。某个新字段为空时，仍沿用旧字段 `exchange_scope` 对应的值，因此原有安装不必立即修改。显式配置时两个新字段都建议填写；空白字符不代表取消 scope 校验。当前测试环境的设置为：

   ```sql
   UPDATE sso_providers
   SET external_required_scope = 'read', hydra_issue_scope = 'openid',
       updated_at = CURRENT_TIMESTAMP
   WHERE id = 'casdoor';
   ```
3. 若启用无浏览器换发，在 bkn-safe 容器配置 `SAFE_SSO_HYDRA_CLIENT_ID`、`SAFE_SSO_HYDRA_CLIENT_SECRET`、`SAFE_SSO_ASSERTION_ISSUER`、`SAFE_SSO_ASSERTION_KEY_ID`、`SAFE_SSO_ASSERTION_PRIVATE_KEY_PEM`、`SAFE_SSO_HOOK_KEY`。`KEY_ID` 与登记到 Hydra 的公钥 JWK `kid` 相同。私钥使用至少 2048 位 RSA PKCS#8 或 PKCS#1 PEM；hook key 至少 32 字节。所有密钥通过 Secret 注入，避免存入数据库或 Helm values。Chart 的 `extraEnv` 可引用已有 Secret。任何一项缺失时启动会失败；不配置换发专用变量时，浏览器登录仍可使用。
4. 在 Hydra 创建**专用机密客户端**，客户端 ID/密钥与上一步一致；允许 `urn:ietf:params:oauth:grant-type:jwt-bearer`、`hydra_issue_scope` 中的 scope，以及 `bkn-safe` audience。把 bkn-safe 断言公钥登记到 Hydra `POST /admin/trust/grants/jwt-bearer/issuers`，`issuer` 与 `SAFE_SSO_ASSERTION_ISSUER` 一致，`allow_any_subject=true`，scope 允许 `hydra_issue_scope` 中的值，并设置明确的 `expires_at`。这是高信任授权，公钥和信任到期需要轮换；操作前由模块 Owner 审核。Hydra v26.2.0 要求断言 `aud` 包含浏览器公开的 token endpoint（`SAFE_HYDRA_BROWSER_PUBLIC_URL/oauth2/token`），而实际请求可走集群内部 token endpoint。断言还包含 `bkn-safe`，用于签发后的业务受众校验；版本升级时需要重新做受众回归验证。
5. 在 Hydra 启用 token hook，URL 指向集群内 `http://<bkn-safe-service>:3000/internal/sso/token-hook`，`auth.type=api_key`、`auth.config.in=header`、`auth.config.name=X-SSO-Hook-Key`、`auth.config.value` 等于 bkn-safe 的 hook key。独立 Hydra Chart 用 `config.ssoTokenHook`，bkn-safe bundled Hydra 用 `bundledDeps.ssoTokenHook`，默认都关闭，key 从指定的 Kubernetes Secret 读取。`/internal/sso/token-hook` 只经 ClusterIP 可达，不加入 ingress；Hydra 的所有 token 签发都会调用它，其他客户端收到 204 并沿用原行为。

## 当前验证环境的部门同步

当前 Casdoor UserInfo 已返回顶层 `groups` 字符串数组，例如
`["organization_openbkn/department-1"]`。本环境 `sso_providers.department_claim`
已设置为 `groups`，无需额外申请 scope。`adapter=casdoor` 且
`department_claim=groups` 时，适配器使用现有客户端凭据通过 HTTP Basic
认证读取同一 issuer 下的 `/api/get-group` 和 `/api/get-organization`，同步
显示名称和 `parentId` 指定的父子关系；该应用必须具有相应的读取权限。

`organization_openbkn/department-1-1` 是组织/组标识，不能直接按 `/` 推断
整条祖先路径。适配器沿 `parentId` 查找祖先，以组织作为根部门，保留完整
组标识作为稳定外部键。已同步的平级部门保留本地 ID，原地更新名称和父部门。
只为用户实际所属组建立直接成员关系，不将用户自动加入所有祖先部门。

组查询限定在同组织内，每次解析最多 128 个节点、32 层祖先和 8 秒总时限。
元数据缺失、查询失败、返回错误对象或父子循环会终止本次同步，保留原有
成员关系；客户端凭据不会随 HTTP 重定向转发。其他适配器或 claim 仍按原有
顶层字符串/字符串数组映射为平级部门。

同步在 Casdoor SSO 回调或令牌换发中执行。只刷新 Studio 页面，或 Hydra 直接
复用已有登录会话，不会再次读取 Casdoor UserInfo；修改分组或映射后需要重新走
一次 Casdoor SSO 登录。缺失 `groups` 不清空现有关系，显式空数组只清空该登录源
管理的部门成员关系，本地手动分配的部门不受影响。

删除本地用户时，同一事务内清理其 SSO 身份映射。下次 SSO 登录创建新用户 ID，
原用户权限不自动恢复。需要阻止外部用户再次登录时应禁用账号，禁用账号不会
因重新登录而自动启用。

删除空部门时也会在同一事务内清理对应 SSO 部门映射。若 Casdoor 中该组仍然
存在，后续成员登录时会按源端信息重新创建部门和父子层级。

## 验收

- Studio 登录：`/oauth2/auth` → `/login` → Casdoor → `/sso/callback/casdoor` → Hydra consent → `/studio/callback` → Hydra token；验证重复 state、错误 nonce、错误签名和错误 `sub` 均失败。
- 用户同步：首次登录生成 `source=sso` 用户，后续以 `issuer+sub` 更新；只替换该 SSO 源拥有的部门成员关系。UserInfo 缺失部门 claim 时不清空成员关系。Casdoor `groups` 使用完整组织/组标识作外部键并同步层级；其他 claim 仍以名称作外部键，同名不同级部门需要单独适配稳定 ID。
- 无浏览器换发：允许的 Casdoor 客户端、受众、scope 可换取 Hydra token；撤销的 token、未允许的客户端或 subject 不一致时拒绝。实际 Hydra introspection 必须包含 `sub`、`aud=bkn-safe` 和现有业务系统要求的 `ext` 字段。

接入涉及新增数据库表和 Hydra 信任关系；本文只给出部署步骤，未执行生产迁移、密钥变更或配置发布。
