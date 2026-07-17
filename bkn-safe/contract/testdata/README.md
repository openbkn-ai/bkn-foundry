# contract test 冻结夹具

`introspect/*.json` 是 hydra `/admin/oauth2/introspect` 响应的冻结 golden，由
`introspect_contract_test.go` 在运行时读取，喂给真实的 `kweaver-go-lib/hydra`
客户端，断言其解析不 panic（lib 用无 nil 检查的类型断言，缺字段会 panic）。

| fixture | 主体 | 关键约束 |
|---|---|---|
| `user.json` | 实名用户 | `active`(bool) 必有；`sub`≠`client_id`；`ext.{visitor_type,login_ip,udid,account_type,client_type}` 5 个全 string，缺一 panic |
| `app.json` | 应用（client_credentials） | `sub`==`client_id` 走 app 分支，`ext.*` 可缺 |
| `anonymous.json` | 匿名 | `ext.visitor_type`="anonymous" |
| `inactive.json` | 失效 token | `active`=false 直接返回 |

> 来源：ISF dip-poc 真实抓取。契约冻结的完整 spec 与设计文档见
> [bkn-docs `docs/foundry/`](https://github.com/openbkn-ai/bkn-docs/tree/main/docs/foundry)。
> 这些文件是 `go test` 的输入数据，不要删除或手改。
