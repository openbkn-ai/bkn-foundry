# 身份源扩展边界

社区核心提供 `extension/federation` 与 `extension/identity`，不包含第三方协议实现。
EE 入口在 `app.Boot` 与 `app.Run` 之间调用 `RegisterIdentityProvider(factory)`：

- factory 使用共享数据库初始化自己拥有的表，不迁移、重建或删除核心表。
- `BeforeLogin` 提供登录选项或默认跳转；`local=1` 始终绕过扩展，保留本地登录入口。
- `Mount` 注册扩展独有路由。核心 `/login` 继续使用原有账号密码逻辑。
- `TokenHook` 处理已由核心验证共享密钥的请求，可增加 token session 字段。
- `identity.RegisterCleanup` 按数据库实例注册删除清理，与核心用户/部门删除使用同一事务。
  清理失败回滚整个删除；没有注册扩展时不查询任何扩展表。

共享 Chart 的 `bundledDeps.ssoTokenHook.enabled` 保持默认 true，同时生成并在升级时
复用内部 Secret，向 Hydra 与 bkn-safe 注入匹配的 hook 配置。社区默认 hook 验证密钥后
返回 204，不改变普通签发结果；EE 镜像接入真正的身份源处理。不需要 EE 专属 Chart
或后续通过 Helm 启用身份源。已有数据库中的扩展表在社区升级或回退时保留。

`/internal/sso/token-hook` 是保留兼容性的内部路径，不暴露在 Ingress。缺少或错误的
hook key 返回 403，body 最大 32 KiB。SSO 登录、回调和换发接口只由 EE 注册。

发布顺序：先发布包含这些公共扩展包的核心提交，再将 EE 的 bkn-safe server 依赖固定到
该提交并构建 EE 镜像。不能用旧核心依赖构建新 EE，也不应在发布文件中保留本地 replace。
