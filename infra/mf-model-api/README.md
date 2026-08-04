# 依赖
1.	该服务负责大模型和小模型api调用，和mf-model-manager使用同一个基础镜像和数据库

## OpenAI 兼容面的错误契约

`/v1/chat/completions`（公开路由与 S2S `private` 路由）对外声明为 OpenAI 兼容，
调用方（`@ai-sdk/openai-compatible`、`openai-python`、LangChain）解析响应时用的是
`union(chunkSchema, errorSchema)`：要么顶层有 `choices`，要么顶层有 `error`。
因此该路由上**所有失败出口**——SSE 帧与 JSON body——都必须是：

```json
{"error": {"message": "...", "type": "...", "param": null, "code": "..."}}
```

规则：

- **不套 envelope。** 模型工厂自家的 `{code, description, detail, solution, link}`
  两边都不匹配，客户端会抛 `TypeValidationError` 并把原始 body 冒给终端用户（#620）。
- **上游已合规就原样透传。** 上游返回的 `{"error": {...}}` 直接带下去，不要
  JSON 字符串化后塞进别的字段——那会逼调用方 `JSON.parse` 两次。
- **状态码跟随上游语义，但依赖侧的鉴权码不透传。** 描述「调用方这次请求本身有
  问题」的 4xx（400/408/413/422/429）透传；上游 401/403/404 描述的是本服务与
  模型厂商之间的认证结果，透出去会被调用方读成自己的凭据/权限失效（本服务自己
  的 403 表示「无该模型 execute 权限」），一律收敛成 `502`，真实原因留在
  `error.type` 里。5xx 收敛成 `503`，连不上是 `502`；限流/不可用带
  `Retry-After`。映射与判定集中在 `app/utils/openai_error.py`。
- **不外泄未知形态的上游 body。** 已知字段（`error.message` / `message` /
  `detail` / …）都没命中时给固定文案，原文只落日志——供应商 5xx 常回显整个请求、
  内部 trace id 和网关节点名。内部异常同理，`str(e)` 不进 `error.message`。
- **流式先发错误帧再断流。** SSE 已开流后才出错的，发一帧
  `data: {"error": {...}}` 然后结束，不要把错误塞在 chunk 的位置上。
  注意：`EventSourceResponse` 的响应头在生成器执行前就已刷出，
  所以流式场景的 HTTP 状态码恒为 200，错误只能靠错误帧表达。
- **瞬态错误先重试。** 上游 429/502/503/504 走退避重试（`sleep_before_retry`），
  重试用完才报错；4xx 参数类错误不重试。

兼容面覆盖到**框架层**：请求体在 pydantic 就被打回的那类走 FastAPI 的
`RequestValidationError` 处理器，同样按路径转成 OpenAI 错误体（`app/routers/__init__.py`
的 `_is_openai_compat`）。该处理器是全服务共用的——小模型、模型管理等端点不是
兼容面，继续用 envelope，改这里千万别一刀切。

内部 envelope（参数校验、权限、配额等）经
`llm_controller.envelope_error_response()` 翻成上述形状后再出门，原 `code`
落到 OpenAI 的 `code` 字段，机器可读的身份不丢。

回归测试见 `app/test/test_openai_error.py` 与 `app/test/test_llm_error_contract.py`。
