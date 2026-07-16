# bkn-foundry 文档入口

`bkn-foundry` 的需求文档、设计实现文档、测试计划和迁移方案已迁移到 `openbkn-ai/bkn-docs` 统一维护。

正式维护位置：

- https://github.com/openbkn-ai/bkn-docs/tree/main/docs/foundry
- https://github.com/openbkn-ai/bkn-docs/blob/main/docs/foundry/_migration/issue-1-bkn-foundry-docs-migration.md

本仓库只保留代码旁必要 README、构建/测试/部署直接消费的文件，以及指向正式文档的入口说明；不再保留已迁移文档的正文副本。

## 📚 本仓保留的文档

| 目录 | 内容 |
| --- | --- |
| [`api/`](api/) | 各服务 OpenAPI 文档（YAML 真相源 + 渲染的 Markdown）。属「代码旁 / 构建消费」，随代码维护 |
| [`images/`](images/) | 架构图等图片资源 |

> bkn-safe（ISF 替换）的设计文档已迁 [bkn-docs](https://github.com/openbkn-ai/bkn-docs/tree/main/docs/foundry)；contract test 冻结夹具随代码放在 [`bkn-safe/contract/testdata/`](../bkn-safe/contract/testdata/)。
