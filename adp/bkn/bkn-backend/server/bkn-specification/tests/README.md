# BKN Go SDK 集成测试

集成测试验证 SDK 与文件系统、tar 归档的完整交互。

## 测试设计原则

- **少即是多**：6个核心工作流 + 5个边界情况
- **严格验证**：使用深度比较确保数据一致性
- **真实数据**：使用 `examples/` 目录下的示例网络

## 核心工作流测试（6个）

| 测试 | 路径 | 验证方式 |
|------|------|---------|
| TestLoadFromFile | 文件 → Model | 严格深度比较 |
| TestLoadFromTar | Tar → Model | 严格深度比较 |
| TestSaveToFile | Model → 文件 | 重新加载后严格比较 |
| TestWriteToTar | Model → Tar | 重新加载后严格比较 |
| TestRoundTrip_FileToTar | 文件→Model→Tar→Model | 首尾严格一致 |
| TestRoundTrip_TarToTar | Tar→Model→Tar→Model | 首尾严格一致 |

## 边界情况测试（5个）

| 测试 | 场景 | 期望行为 |
|------|------|---------|
| TestEmptyNetwork | 空网络 | 正常加载，返回空结构 |
| TestMissingRootFile | 目录无 network.bkn | 返回错误：未找到根文件 |
| TestInvalidFrontmatter | 无效 YAML | 返回错误：解析失败 |
| TestCircularInclude | 循环包含 | 返回错误：检测到循环 |
| TestMissingInclude | include 文件不存在 | 返回错误：文件未找到 |

## 运行测试

```bash
cd sdk/golang/test

# 运行所有集成测试
go test -v ./...

# 只运行核心工作流
go test -v -run "TestLoad|TestSave|TestWrite|TestRoundTrip" ./...

# 只运行边界情况
go test -v -run "TestEmpty|TestMissing|TestInvalid|TestCircular" ./...
```

## 与单元测试的区别

| | 单元测试 (bkn/) | 集成测试 (test/) |
|--|----------------|-----------------|
| 数量 | ~30个 | 11个 |
| 依赖 | 无外部依赖 | 依赖文件系统、示例数据 |
| 速度 | < 1秒 | 1-3秒 |
| 运行时机 | 每次保存 | 提交前、CI/CD |

## 相关文档

- [BKN 设计文档](../../../design/bkn/features/bkn_docs/DESIGN.md)
- [SDK 使用指南](../README.md)
