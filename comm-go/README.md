# comm-go

openbkn Go 公共依赖库。回迁自独立仓 `openbkn-ai/bkn-comm-go`，module path 现为
`github.com/openbkn-ai/bkn-foundry/comm-go`。

## 改完之后：谁负责让消费者用上

comm-go 与各服务是**各自独立的 Go module**，同一个仓也不例外。消费者的 `go.mod`
里钉着一个版本，那行不会自己变。所以改公共库永远是两步——先合库，再让消费者
指过去——而且单体仓也没法把两步塞进同一个 PR：`require` 的那个版本得先存在。

第二步由谁做，取决于改动的性质：

| 改动 | 谁负责 | 为什么 |
|---|---|---|
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
