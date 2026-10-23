# comm-go

openbkn Go 公共依赖库。回迁自独立仓 `openbkn-ai/bkn-comm-go`，module path 现为
`github.com/openbkn-ai/bkn-foundry/comm-go`。

开发、构建和测试需要 Go 1.26.0 或更新版本。CI 从本模块的 `go.mod` 读取 Go
版本；消费者升级到本模块的新版本时，也需要使用 Go 1.26.0 或更新版本。

## 本地联调

本地联调通过仓库根目录的 `go.work` 使用工作区中的 `comm-go`，不在消费者的
`go.mod` 中添加本地 `replace`。例如，在仓库根目录执行：

```bash
go work init ./comm-go ./adp/bkn/bkn-backend/server
# 已有 go.work 时，用 go work use 添加需要联调的模块
go work use ./adp/bkn/ontology-query/server
```

`go.work` 和 `go.work.sum` 按仓库约定不提交。消费者的 `require` 仍需固定到已发布的
`comm-go` 版本；使用 `GOWORK=off` 验证独立构建，确保 CI 和镜像构建不依赖本地源码。

## 改完之后：谁负责让消费者用上

comm-go 与各服务是**各自独立的 Go module**，同一个仓也不例外。消费者的 `go.mod`
里钉着一个版本，那行不会自己变。所以改公共库永远是两步——先合库，再让消费者
指过去——而且单体仓也没法把两步塞进同一个 PR：`require` 的那个版本得先存在。

第二步由谁做，取决于改动的性质：

| 改动 | 谁负责 | 为什么 |
| --- | --- | --- |
| **加 API**（新函数、新类型、新包） | 用它的人 | 你要用就编不过，立刻发现 |
| **改行为**（bug 修复、安全修复、语义变更） | **改 comm-go 的人**，合并后立刻 bump 各消费者 | 编译照过、测试照绿、行为是旧的——不会有任何东西报错 |

第二类是这条规矩存在的全部理由。举个真实的：`entitlement` 修过一个「hub 不可达
时许可判定被冻住」的洞（掐掉出向流量就永久有效）。修完不 bump，那个绕过就一直
在线上，而没有人「需要」这次改动——没有缺失的符号会提醒任何人。

怎么 bump：

```bash
cd <消费者目录>
go get github.com/openbkn-ai/bkn-foundry/comm-go@<main 上的 commit>
go mod tidy && go build ./... && go test ./...
```

找出所有消费者：

```bash
# 要求路径后面跟着版本号，否则 comm-go/go.mod 的 module 行也会命中
grep -rlE 'openbkn-ai/bkn-foundry/comm-go v[0-9]' --include=go.mod . | xargs -n1 dirname
```

## 版本

日常钉 commit（`go get ...@<sha>`，落成 pseudo-version），正式发布才打
`comm-go/vX.Y.Z` 并让消费者钉 tag。

日常不打 tag，是因为 tag 不可撤回：Go 的 proxy 一旦缓存就永久保留，源仓删掉 ref
也照样发。给每次合并都发一个版本号，等于把版本序变成提交日志。pseudo-version
指向的 commit 在 main 上同样永久可达，坐标一样诚实，只是读不出语义。

## 包

- `entitlement` —— EE 版本门控：按档位判定（`AtLeast`）、装配注册表、
  从 bkn-safe 取证书并本地验签的 hub 客户端。设计见 bkn-docs
  `docs/shared/licensing/ee-design.md`。
- `entitlement/socket` —— 插座通用注册表，各服务的插座（`mcptool`、将来的
  `httproute`）共用它来保证「注册只在装配期、必须声明能力与档位、重复键必炸、
  列举稳定排序」这四条。
- 其余（`audit` `common` `crypto` `db` `hydra` `i18n` `logger` `middleware`
  `mq` `otel` `rest`）为回迁前既有的基础设施封装。

### 第三方源码

`db/driver/kingbase/gokb/` 是人大金仓（KingbaseES）官方 Go 驱动的源码，版权归
中电科金仓（北京）科技股份有限公司。它被 `db/driver/rds.go` 无条件 import，因此
链接进每个用到 `db/driver` 的服务。来源说明与第三方 license 例外待归档。

## REST 错误与 i18n

`i18n` 提供语言协商、context 语言传递和消息模板渲染；`rest` 提供错误码注册、HTTP 错误构造与响应写出。
设计说明见[共享错误国际化与 metadata 方案](https://github.com/openbkn-ai/bkn-docs/blob/main/docs/shared/decisions/issue-2375-error-i18n-metadata.md)。

消息使用 Go map 定义，随代码编译。每个模块按 `<module>_zh_cn.go`、`<module>_en_us.go` 分别维护中文和英文消息，
消息 key 使用错误码，文案包含完整的错误描述及处理指引。例如：

```go
// errors_en_us.go
var errorsEnUS = i18n.I18nMessages{
    Locale: i18n.AmericanEnglish,
    Messages: map[string]string{
        "Example.InvalidField": "Field {{ .field }} must not exceed {{ .limit }} characters.",
    },
}

// errors_zh_cn.go
var errorsZhCN = i18n.I18nMessages{
    Locale: i18n.SimplifiedChinese,
    Messages: map[string]string{
        "Example.InvalidField": "字段 {{ .field }} 不能超过 {{ .limit }} 个字符。",
    },
}
```

启动时串行注册消息、设置默认语言、检查语言包，最后注册错误码：

```go
i18n.RegisterI18n(&errorsZhCN)
i18n.RegisterI18n(&errorsEnUS)
i18n.SetDefaultLocale(i18n.SimplifiedChinese)
if err := i18n.ValidateMessageBundles(); err != nil {
    panic(err)
}
rest.Register([]string{"Example.InvalidField"})
```

`RegisterI18n` 每次接收一个 `*I18nMessages`，去除消息 key 和文案的首尾空白后编译模板。
空 key、空文案、非法模板或重复 key 会导致 panic；同次注册在检查全部 key 冲突后才追加消息。
语言值直接使用 `i18n.SimplifiedChinese`、`i18n.AmericanEnglish` 等语言常量，不做规范化。
公共消息及公共错误码由包初始化自动注册，业务消息和错误码由启动层显式注册；已有消息不得覆盖。
注册仅用于启动阶段，不支持运行时注册或重载。

`ValidateMessageBundles()` 以 `DefaultLanguage` 为基准检查各语言的消息 key 集合，要求提供 zh-CN 和 en-US，发现第一个问题即返回 error。
不同语言允许使用不同的模板参数。`ValidateMessages(ids...)` 检查指定消息 ID 在全部已注册语言包中是否存在，不执行模板；
`rest.Register` 会调用该检查，重复错误码或缺失消息会导致 panic。

HTTP 语言协商、context 读写和请求头常量位于 `i18n/language.go`。
默认语言为中文，可在启动时通过 `SetDefaultLocale` 设置已注册的语言，或通过 `SetLang` 解析支持的语言别名。
请求可使用 `rest.LanguageMiddleware()` 解析 Accept-Language，也可通过 `i18n.WithLanguage` 显式传递语言。
`WithLanguage` 直接保存已解析的 `Language`，应传入语言常量或 `NormalizeLanguage` / `ResolveLanguage` 的结果，不再规范化别名。
`Language` 是独立字符串类型，并不保证值合法；例如 `"en-us"`、`"fr-FR"` 经 `WithLanguage` 存入后，
`GetLanguageByCtx` 会回退到 `DefaultLanguage`。

```go
err := rest.NewHTTPError(ctx, http.StatusBadRequest, "Example.InvalidField").
    WithMetadata(map[string]any{"field": "title", "limit": 10})
rest.ReplyError(c, err)
```

`NewHTTPError` 保存选定语言，不立即生成文案。`WithMetadata` 将参数复制到错误自身的 map 并原地合并，后写覆盖同名键（含 nil），
nil/空 map 不改变已有值。metadata 同时用于模板渲染和响应 JSON，只能传入允许公开的数据；嵌套值共享，传入后不得修改。
`WithErrorDetails` 设置允许公开的 JSON 数据，不参与模板渲染。

`Render() error` 直接更新错误的 `BaseError.Description`，失败时清空旧描述并返回渲染错误，不写日志。
装配和渲染会修改错误对象，不支持对同一对象并发操作。
`BaseError` 是响应 DTO，直接编码或调用其 `Error()` 只输出现有字段，不执行渲染。
需要读取文案的调用点应先显式调用 `Render()`。

`i18n.Render(language, messageID, metadata)` 按语言和消息 ID 精确查找模板，不回退到默认语言。
模板使用 `missingkey=default`：缺失参数输出 `<no value>`，条件按空值处理；参数类型错误、越界索引等执行失败会丢弃部分输出并返回错误。
`*i18n.RenderError.Kind` 区分 `LocaleMissing`、`MessageMissing` 和 `TemplateExecution`，诊断只包含语言、消息 ID 和错误类型。

`HTTPError.MarshalJSON` 在值副本上渲染并编码 `BaseError`，不写日志，仅返回 JSON 编码错误；需要检查渲染错误时应显式调用 `Render()`。
`HTTPError.Error()` 返回 JSON 字符串，编码失败时记录诊断并返回错误码。
`ReplyError` 显式渲染、记录渲染诊断并编码响应；渲染失败保留业务状态码、错误码、metadata 和 error_details，JSON 编码失败转公共 500。
非 HTTPError 转为公共 500，其原始错误文本写入 error_details，调用方应将需要控制公开内容的错误显式映射为 HTTPError。
Content-Language 使用最终响应错误选定的语言。

响应包含 error_code、description、error_details 及非空时的 metadata，不包含 solution/error_link。
消费者可在 description 为空时展示 error_code。
