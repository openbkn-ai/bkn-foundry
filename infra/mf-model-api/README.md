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

内部 envelope（参数校验、权限、配额等）经
`llm_controller.envelope_error_response()` 翻成上述形状后再出门，原 `code`
落到 OpenAI 的 `code` 字段，机器可读的身份不丢。

回归测试见 `app/test/test_openai_error.py` 与 `app/test/test_llm_error_contract.py`。

## 日志纪律

调第三方模型时，请求头带的是**供应商 api_key**，请求体带的是**用户的完整对话
内容**。这两样整体拼进日志就顺着采集链路离开了本服务，而日志的读权限模型跟凭据
管理、业务数据管理都不是一套（#636）。

往日志里放请求上下文一律经 `app/utils/log_redact.py`：

- `safe_headers(headers)` —— `Authorization` / `api-key` / `Cookie` 等只留掩码
  （保留 `Bearer` 方案前缀与前 4 位，够认出是哪把 key，不够拿去用），其余原样
- `request_digest(params)` —— 请求体压成 model / stream / 消息条数 / 字符数 /
  role 序列 / 采样参数，**不含任何 `content`**
- `messages_digest(messages)` —— 只有 messages 在手时的简写
- `safe_url(url)` —— 只留 scheme+host+path，query 一律抹掉（百度 oauth 把
  `client_secret` 拼在 query 里，`OtherClient.api_url` 又是管理员自由填的）

**成功路径同样适用**：原来 `BaiduTianchenClient` 每次调用都会把完整 `messages`
以 INFO 落盘一次，比出错才触发的那几处流得更狠，一并换成摘要了。

要复现问题用摘要里的 model 与参数，配合调用方自己的 trace，不要靠日志回放用户
原文。回归见 `app/test/test_log_redact.py`（含一条断言直接扫源码，防止泄露点
被重新写回来）。
