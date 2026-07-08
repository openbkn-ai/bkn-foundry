# BKN Golang SDK

Go SDK for parsing, serializing, loading, and diffing BKN networks.

## Requirements

- **Go 1.25+**

## Structure

```
├── bkn/
│   ├── models.go        # Data structures
│   ├── parser.go        # Parse .bkn files (per type)
│   ├── loader.go        # LoadNetwork, LoadNetworkWithFS
│   ├── serialize.go     # Serialize* functions
│   ├── validator.go     # ValidateNetwork
│   ├── checksum.go      # GenerateChecksumFile, VerifyChecksumFile
│   ├── differ.go        # DiffNetworks, ComputeNetworkChecksums
│   ├── fs.go            # FileSystem interface, OSFileSystem, MemoryFileSystem
│   ├── pack_tar.go      # PackDirToTar
│   ├── tar_loader.go    # LoadNetworkFromTar, ExtractTarToMemory
│   ├── tar_writer.go    # WriteNetworkToTar
│   ├── tar_checksum.go  # ComputeChecksumFromTar, VerifyChecksumFromTar, DiffNetworksFromTar
│   └── parser_test.go
├── tests/
│   └── integration_test.go
└── tools/
    └── regenerate_checksum.go  # 批量重新生成 examples/ 的 CHECKSUM
```

---

## Usage

### 加载网络

```go
// 从目录加载（自动发现 network.bkn）
net, err := bkn.LoadNetwork("path/to/network-dir")

// 从 tar 加载
f, _ := os.Open("network.tar")
net, err := bkn.LoadNetworkFromTar(f)
```

### 解析单个文件

```go
content, _ := os.ReadFile("action_types/restart.bkn")

at, err := bkn.ParseActionTypeFile(string(content), "restart.bkn")
if err != nil {
    panic(err)
}
fmt.Println(at.ID)                          // "restart"
fmt.Println(at.TriggerCondition.Operation)  // "=="
```

### 序列化

```go
// 模型 → BKN 文本
text := bkn.SerializeActionType(at)

// 网络 → tar
var buf bytes.Buffer
err := bkn.WriteNetworkToTar(net, &buf)

// 目录 → tar 文件
err := bkn.PackDirToTar("path/to/dir", "output.tar", false)
```

### 校验和 & Diff

```go
// 生成 CHECKSUM 文件
checksum, err := bkn.GenerateChecksumFile("path/to/dir")

// 验证 CHECKSUM 文件
ok, errs := bkn.VerifyChecksumFile("path/to/dir")

// 从 tar 验证
ok, errs := bkn.VerifyChecksumFromTar(tarReader)

// 比较两个 tar 的差异
result, err := bkn.DiffNetworksFromTar(oldTar, newTar)
for _, e := range result.Creates() { fmt.Println("create:", e.Key) }
for _, e := range result.Updates() { fmt.Println("update:", e.Key) }
for _, e := range result.Deletes() { fmt.Println("delete:", e.Key) }
```

### 验证

```go
result := bkn.ValidateNetwork(net)
if !result.OK() {
    for _, e := range result.Errors {
        fmt.Println(e)
    }
}
```

---

## API

### Parser

| 函数 | 说明 |
|------|------|
| `ParseFrontmatter(text)` | 解析 YAML frontmatter，返回 `map[string]any` |
| `ParseNetworkFile(text, sourcePath)` | 解析 network 文件 |
| `ParseObjectTypeFile(text, sourcePath)` | 解析 object_type 文件 |
| `ParseRelationTypeFile(text, sourcePath)` | 解析 relation_type 文件 |
| `ParseActionTypeFile(text, sourcePath)` | 解析 action_type 文件（含 TriggerCondition） |
| `ParseRiskTypeFile(text, sourcePath)` | 解析 risk_type 文件 |
| `ParseConceptGroupFile(text, sourcePath)` | 解析 concept_group 文件 |

### Loader

| 函数 | 说明 |
|------|------|
| `LoadNetwork(rootPath)` | 从目录加载完整网络（自动发现 network.bkn） |
| `LoadNetworkWithFS(fsys, rootPath)` | 使用自定义 FileSystem 加载网络 |
| `LoadNetworkFromTar(r)` | 从 tar 流加载网络 |
| `ExtractTarToMemory(r)` | 将 tar 解压到内存文件系统 |

### Serializer

| 函数 | 说明 |
|------|------|
| `SerializeBknNetwork(doc)` | 序列化 network frontmatter |
| `SerializeObjectType(ot)` | 序列化 object_type |
| `SerializeRelationType(rt)` | 序列化 relation_type |
| `SerializeActionType(at)` | 序列化 action_type |
| `SerializeRiskType(rt)` | 序列化 risk_type |
| `SerializeConceptGroup(cg)` | 序列化 concept_group |
| `WriteNetworkToTar(doc, w)` | 将完整网络写入 tar 流 |
| `PackDirToTar(sourceDir, outputPath, gzip)` | 将目录打包为 tar 文件（macOS 自动设置 `COPYFILE_DISABLE=1`） |

### Checksum & Diff

| 函数 | 说明 |
|------|------|
| `GenerateChecksumFile(root)` | 生成并写入 CHECKSUM 文件 |
| `VerifyChecksumFile(root)` | 验证目录 CHECKSUM，返回 `(ok, errors)` |
| `ComputeChecksumFromTar(r)` | 从 tar 计算各条目 checksum |
| `GenerateChecksumFromTar(r)` | 从 tar 生成 CHECKSUM 内容字符串 |
| `VerifyChecksumFromTar(r)` | 验证 tar 中的 CHECKSUM，返回 `(ok, errors)` |
| `DiffNetworks(old, new)` | 比较两个 checksum map，返回 `*DiffResult` |
| `DiffNetworksFromTar(oldTar, newTar)` | 比较两个 tar 的差异，返回 `*DiffResult` |
| `ComputeNetworkChecksums(fsys, root)` | 计算网络目录的 checksum map |

### Validator

| 函数 | 说明 |
|------|------|
| `ValidateNetwork(doc)` | 验证网络结构，返回 `*ValidationResult` |

### FileSystem

| 函数 | 说明 |
|------|------|
| `NewOSFileSystem()` | 基于 OS 的文件系统实现 |
| `NewMemoryFileSystem()` | 内存文件系统，用于测试或 tar 解压 |

---

## Tools

### regenerate_checksum.go

批量重新生成 `examples/` 下所有网络的 CHECKSUM 文件。当 examples 内容发生变更（重命名、修改 .bkn 文件、更新 SKILL.md 等）后需要执行。

```bash
# 在 sdk/golang/ 目录下运行，传入 examples 父目录
go run tools/regenerate_checksum.go ../../examples
```

## Tests

```bash
# 单元测试
go test ./bkn/... -v

# 集成测试（使用 tests/testdata/ 中的真实网络）
go test ./tests/... -v

# 全部
go test ./... -v
```
