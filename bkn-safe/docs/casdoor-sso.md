# Casdoor SSO 接入

本实现保留现有 Studio → Hydra 授权码 + PKCE 流程。Hydra 把浏览器带到 bkn-safe `/login` 后，配置为默认登录源时，bkn-safe 再跳转 Casdoor。Casdoor 回调地址必须是 **bkn-safe** 的 `GET /sso/callback/casdoor`，不是 `/studio/`。bkn-safe 验证 ID Token（RS256 签名、`iss`、`aud`、`nonce`、时间）、access token introspection 与 UserInfo 的同一 `sub`，同步本地用户和部门，再接受 Hydra login challenge。后续 consent、Studio callback 和 `/oauth2/token` 与原流程一致；Studio 最终使用的 token 仍由 Hydra 签发。

无浏览器场景调用 `POST /api/safe/v1/auth/token-exchange?provider_id=casdoor`，`Authorization: Bearer <Casdoor access token>`。bkn-safe 在 Casdoor introspection 中核对 `active`、`iss`、`exp`、`aud`、`scope`、`client_id`，用 UserInfo 核对 `sub` 并同步用户，随后用一分钟有效的签名断言请求 Hydra JWT bearer grant。Hydra token hook 填充 OpenBKN 的 `ext` 字段。bkn-safe 再 introspect 新 token，核对 `sub`、`client_id`、`aud=bkn-safe` 及必要 `ext` 字段后才返回它。调用方不接触 Hydra 客户端密钥和断言私钥。这个接口不提供 refresh token；外部 access token 过期后调用方需从 Casdoor 重新取得令牌。

## 新环境安装与配置

随新版本 bkn-safe 镜像一起交付本目录下的 `charts/bkn-safe`。安装时仍填写常规的
OpenBKN 对外访问地址、数据库等参数，SSO 基础设施无需另行安装：

- `bundledDeps.ssoTokenHook.enabled` 默认 `true`，同时配置 bkn-safe 和 bundled Hydra。
- Chart 自动创建 `<release>-sso` Secret，保存随机 hook key、随机 Hydra 客户端密钥
  和 RSA 签名私钥。Helm 升级通过 lookup 复用原值；若已有 Secret 缺字段则停止渲染，
  避免静默更换密钥。应将此 Secret 与数据库一起备份。
- bkn-safe 启动即可提供 hook。未配置身份源时四张 SSO 表为空，仍使用原来的本地登录；
  普通 Hydra 客户端的 hook 请求返回 204，保留原来的 claims。
- 第一次有效的外部 token 换发自动创建/更新专用 Hydra 客户端，并登记签名公钥信任。
  该过程不依赖手动 SQL 以外的部署操作，也不会在启动时互相等待。失败的初始化不缓存，
  下一次请求重试。初始化期间的 Hydra 错误只影响该次换发。
- 专用客户端和信任的 scope 来自所有已启用换发身份源的 `hydra_issue_scope` 并集，
  数据库修改后下次换发生效。按使用情况每天核对一次信任；到期前 30 天自动更新为一年。
  断言仍仅有效一分钟，签名私钥始终留在 bkn-safe 内。

客户后续接入 Casdoor 只需以下步骤，不需要再次修改 Chart 或执行 Helm upgrade：

1. 在 Casdoor 注册机密客户端，精确登记 `https://<openbkn-host>/sso/callback/casdoor`
   为回调，允许授权码、PKCE S256 和实际需要的 scopes。配置 OIDC discovery、JWKS、
   UserInfo 和 introspection，ID Token 签名算法为 RS256。
2. 在 bkn-safe 数据库的 `sso_providers` 表写入客户配置。下面是字段示例，替换域名、
   客户端 ID、密钥、受众以及允许换发的外部应用 ID 后执行：

   ```sql
   INSERT INTO sso_providers
     (id, display_name, protocol, adapter, issuer, discovery_url,
      client_id, client_secret, client_secret_env, redirect_uri, scopes, department_claim,
      allowed_client_ids, exchange_audience, external_required_scope, hydra_issue_scope,
      login_enabled, exchange_enabled, enabled, default_for_login,
      allow_insecure_http, created_at, updated_at)
   VALUES
     ('casdoor', 'Casdoor', 'oidc', 'casdoor',
      'https://casdoor.example', 'https://casdoor.example/.well-known/openid-configuration',
      'openbkn-safe', '<客户的 Casdoor 应用密钥>', '',
      'https://openbkn.example/sso/callback/casdoor',
      'openid profile email', 'groups',
      'trusted-api-client', 'openbkn-safe', 'read', 'openid',
      true, true, true, true, false, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);
   ```

   `client_secret` 是数据库中的敏感凭据，当前不做应用层加密；需保护数据库读权限、
   备份和 SQL 操作记录，服务不会将该字段序列化为 JSON。`client_secret_env` 留空时直接
   使用数据库密钥，修改后下一次请求生效。已有 Secret 环境变量方式继续支持：显式配置
   `client_secret_env` 时优先读取该变量，变量缺失则报错，不回退到数据库旧密钥。

   `allowed_client_ids` 是允许换发的外部客户端 ID，空格分隔；`exchange_audience` 可含
   多个值，外部令牌至少包含其中之一。`external_required_scope` 是外部令牌必须全部具有
   的 scopes；`hydra_issue_scope` 是新 OpenBKN token 的 scopes，两者独立，例如外部
   `read` 换发内部 `openid`。后者由有权限维护数据库的管理员配置，应遵循最小授权。
   旧数据未填新 scope 字段时继续回退到 `exchange_scope`。

   `issuer` 必须与 discovery 和 introspection 的 issuer 一致。HTTP 测试环境需明确设置
   `allow_insecure_http=true`。生产使用 HTTPS。部门字段按 Casdoor 实际返回值配置，
   `groups` 的层级同步见下文。
3. 从 Studio 正常发起登录验证；无浏览器调用仍为：

   ```bash
   curl -X POST 'https://openbkn.example/api/safe/v1/auth/token-exchange?provider_id=casdoor' \
     -H "Authorization: Bearer ${CASDOOR_ACCESS_TOKEN}"
   ```

`default_for_login=true` 表示 `/login` 自动跳转该身份源；改为 false 显示登录页入口。
`enabled=false` 停用该身份源。需要始终使用本地管理员时，通过带原有
`login_challenge` 的 `/login?local=1` 进入本地登录。

## 兼容原部署与外部 Hydra

- 新版本仅增加 `sso_providers.client_secret` 列，没有新增第五张 SSO 表。
- 已有版本需要按正常版本发布流程升级镜像和 Chart 一次；后续身份源配置只操作数据库。
  镜像必须包含本次代码，测试环境的 hostPath 二进制挂载不会进入客户交付镜像。
  企业镜像须由构建方更新其 bkn-safe server 依赖后重新构建。
- 默认方式无需提前创建 Secret。已有外部 Secret 时可设置
  `bundledDeps.ssoTokenHook.secretName`，须具有 `hookKey`（或 `secretKey` 指定的键）、
  `hydraClientSecret`、`assertionPrivateKeyPEM` 三项。不要将客户的第三方应用密钥混入
  新建内部 Secret；客户凭据按上文写数据库。
- 完全停用自动基础设施可设置 `bundledDeps.ssoTokenHook.enabled=false`。
  原手工部署方式仍支持，通过 `extraEnv` 提供原有的 `SAFE_SSO_*` 变量即可；
  `SAFE_SSO_AUTO_BOOTSTRAP` 未设为 true 时不自动维护 Hydra 客户端与信任。
  `SAFE_SSO_ASSERTION_KEY_ID` 可继续显式指定；未指定时按公钥指纹自动生成稳定 kid。
- 独立的 `charts/hydra` 不属于 bundled 安装。使用外部 Hydra 时，由其部署方在首次安装
  同步配置 hook URL 和相同的 hook Secret；bkn-safe 不能通过管理 API 修改外部 Hydra
  进程配置。此处的免补配置交付路径针对 `charts/bkn-safe` 自带 Hydra 的部署。
- `/internal/sso/token-hook` 不加入 Ingress。启用 NetworkPolicy 时需允许 Hydra 访问
  bkn-safe 的服务端口。Hydra 的 token 签发依赖 hook 可达；普通客户端无需身份源配置，
  但同样依赖 bkn-safe 的可用性。
- Helm 升级复用密钥需要集群 Secret 的读取权限。不要用离线 `helm template` 结果覆盖
  已有集群的 Secret：离线渲染无法查询旧密钥，会生成新值。恢复和升级应走 Helm。

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

接入配置界面和管理接口暂未提供。本文不包含生产环境迁移或配置发布操作。
