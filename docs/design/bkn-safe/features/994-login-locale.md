---
issue: "#994"
branch: "feat/994-login-locale"
module: "bkn-safe"
status: "draft"
author: "@rongwei-liu"
created: "2026-08-18"
pr: ""
---

# Feature #994: 打通 Studio 与 BKN Safe 登录流程语言切换

## Background and Goals

BKN Studio 已支持中英文切换，但登录页面由 BKN Safe 服务端渲染，进入 OAuth 登录流程后没有语言选择入口，也无法稳定继承 Studio 当前语言。目标是在不改变认证、授权和密码业务规则的前提下，让登录全流程保持一致的 `zh-CN` / `en-US` 展示语言。

## Design

### Summary

- Studio 发起 OAuth2 Authorization Code + PKCE 流程时，通过标准 `ui_locales` 参数传递当前语言。
- BKN Safe 登录页右上角提供“中文 / English”切换，保持现有卡片、Logo、输入框和按钮布局。
- 语言选择通过同源 `openbkn_locale` Cookie 在 BKN Safe 与 Studio 间同步；Studio 的 localStorage 继续作为前端本地兜底。
- BKN Safe 的解析优先级为：显式 `lang`、OIDC `ui_locales`、共享 Cookie、`Accept-Language`。
- 所有语言输入仅允许归一化为 `zh-CN` 或 `en-US`，只影响展示，不参与认证或授权决策。

### Interaction Design

- Interaction reference: [Issue #994](https://github.com/openbkn-ai/bkn-foundry/issues/994)
- Annotated layout: [assets/994-login-locale-annotation.svg](assets/994-login-locale-annotation.svg)
- 登录卡片右上角显示紧凑的语言链接；当前语言使用品牌蓝和较粗字重，键盘焦点清晰可见。
- 登录失败、首次修改密码、第三方授权、设备授权和设备成功页面使用同一个选择器，并通过隐藏 `lang` 字段延续 POST 后的语言。
- OpenBKN Logo 使用纯路径 SVG，通过 `/login/assets/` 下的内容指纹地址独立加载，并使用长期不可变缓存；资源由约 215 KiB 的 PNG 降至约 14 KiB，页面保留明确的图片尺寸，切换语言时不再重复传输约 287 KiB 的 Base64 数据，也不会因资源加载产生布局跳动。

### API Changes

没有新增业务 API。OAuth 授权请求新增标准参数：

```http
GET /oauth2/auth?...&ui_locales=en-US
```

BKN Safe 页面 GET/POST 接受仅用于展示的 `lang=zh-CN|en-US` 参数。

### Key Flow

```text
Studio current locale
  -> OAuth ui_locales
  -> Hydra login request_url
  -> BKN Safe locale resolver
  -> localized server-rendered page + openbkn_locale cookie
  -> Studio callback/app initialization reads shared cookie
```

Hydra 登录请求读取失败时，BKN Safe 回退到 Cookie / `Accept-Language`，不阻断登录页渲染。

## Acceptance Criteria

- [ ] 登录页可在中文与英文之间切换，默认中文。
- [ ] Studio 当前语言通过 `ui_locales` 传入登录流程。
- [ ] 登录页选择可同步到回跳后的 Studio。
- [ ] 登录失败、首次修改密码、授权和设备页面保持所选语言。
- [ ] 不改变原有认证、授权和密码业务逻辑。
- [ ] 前后端单元测试覆盖优先级、透传和持久化。

## Test Strategy

- Go：验证 `ui_locales`、显式选择、共享 Cookie、`Accept-Language` 的优先级，以及 HTML `lang`、隐藏字段、响应头和 Cookie。
- Vitest：验证 Studio 语言 Cookie 与 localStorage 的解析优先级，以及 OAuth URL 中的 `ui_locales`。
- 回归：运行 BKN Safe 全量 Go 测试和 BKN Studio 强制质量检查。

## Impact Analysis

- **Backward compatibility**: 兼容；新增的 query/form 参数和 Cookie 均为可选，原有 `Accept-Language` 仍是兜底。
- **Dependency changes**: 无。
- **Performance impact**: 登录页会对 Hydra 登录 challenge 做一次只读查询以取得原始 `ui_locales`；查询失败会无损降级。
- **Static asset caching**: 纯路径 SVG Logo 继续编译进 BKN Safe 二进制，不包含脚本、外链、字体或内嵌位图，也不依赖运行时文件系统；浏览器通过内容指纹 URL、`ETag` 和 `Cache-Control: public, max-age=31536000, immutable` 缓存。
- **Security**: Cookie 不含账号、令牌或会话数据，仅保存允许列表内的展示语言；使用 `SameSite=Lax`，HTTPS 下使用 `Secure`。

## References

- [Issue #994](https://github.com/openbkn-ai/bkn-foundry/issues/994)
- [OpenID Connect Core: `ui_locales`](https://openid.net/specs/openid-connect-core-1_0.html#AuthRequest)
