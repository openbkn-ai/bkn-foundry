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
    └── regenerate_checksum.go  # Regenerate CHECKSUM files for examples/ in bulk
```

---

## Usage

### Load a Network

```go
// Load from a directory, automatically discovering network.bkn.
net, err := bkn.LoadNetwork("path/to/network-dir")

// Load from a tar archive.
f, _ := os.Open("network.tar")
net, err := bkn.LoadNetworkFromTar(f)
```

### Parse a Single File

```go
content, _ := os.ReadFile("action_types/restart.bkn")

at, err := bkn.ParseActionTypeFile(string(content), "restart.bkn")
if err != nil {
    panic(err)
}
fmt.Println(at.ID)                          // "restart"
fmt.Println(at.TriggerCondition.Operation)  // "=="
```

### Serialize

```go
// Model -> BKN text.
text := bkn.SerializeActionType(at)

// Network -> tar.
var buf bytes.Buffer
err := bkn.WriteNetworkToTar(net, &buf)

// Directory -> tar file.
err := bkn.PackDirToTar("path/to/dir", "output.tar", false)
```

### Checksum & Diff

```go
// Generate a CHECKSUM file.
checksum, err := bkn.GenerateChecksumFile("path/to/dir")

// Verify a CHECKSUM file.
ok, errs := bkn.VerifyChecksumFile("path/to/dir")

// Verify from a tar archive.
ok, errs := bkn.VerifyChecksumFromTar(tarReader)

// Compare differences between two tar archives.
result, err := bkn.DiffNetworksFromTar(oldTar, newTar)
for _, e := range result.Creates() { fmt.Println("create:", e.Key) }
for _, e := range result.Updates() { fmt.Println("update:", e.Key) }
for _, e := range result.Deletes() { fmt.Println("delete:", e.Key) }
```

### Validate

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

| Function | Description |
|------|------|
| `ParseFrontmatter(text)` | Parses YAML frontmatter and returns `map[string]any` |
| `ParseNetworkFile(text, sourcePath)` | Parses a network file |
| `ParseObjectTypeFile(text, sourcePath)` | Parses an object_type file |
| `ParseRelationTypeFile(text, sourcePath)` | Parses a relation_type file |
| `ParseActionTypeFile(text, sourcePath)` | Parses an action_type file, including `TriggerCondition` |
| `ParseRiskTypeFile(text, sourcePath)` | Parses a risk_type file |
| `ParseConceptGroupFile(text, sourcePath)` | Parses a concept_group file |

### Loader

| Function | Description |
|------|------|
| `LoadNetwork(rootPath)` | Loads a complete network from a directory, automatically discovering network.bkn |
| `LoadNetworkWithFS(fsys, rootPath)` | Loads a network with a custom FileSystem |
| `LoadNetworkFromTar(r)` | Loads a network from a tar stream |
| `ExtractTarToMemory(r)` | Extracts a tar archive into an in-memory file system |

### Serializer

| Function | Description |
|------|------|
| `SerializeBknNetwork(doc)` | Serializes network frontmatter |
| `SerializeObjectType(ot)` | Serializes object_type |
| `SerializeRelationType(rt)` | Serializes relation_type |
| `SerializeActionType(at)` | Serializes action_type |
| `SerializeRiskType(rt)` | Serializes risk_type |
| `SerializeConceptGroup(cg)` | Serializes concept_group |
| `WriteNetworkToTar(doc, w)` | Writes a complete network to a tar stream |
| `PackDirToTar(sourceDir, outputPath, gzip)` | Packs a directory into a tar file; on macOS, sets `COPYFILE_DISABLE=1` automatically |

### Checksum & Diff

| Function | Description |
|------|------|
| `GenerateChecksumFile(root)` | Generates and writes a CHECKSUM file |
| `VerifyChecksumFile(root)` | Verifies the directory CHECKSUM and returns `(ok, errors)` |
| `ComputeChecksumFromTar(r)` | Computes checksums for tar entries |
| `GenerateChecksumFromTar(r)` | Generates CHECKSUM content from a tar archive |
| `VerifyChecksumFromTar(r)` | Verifies the CHECKSUM in a tar archive and returns `(ok, errors)` |
| `DiffNetworks(old, new)` | Compares two checksum maps and returns `*DiffResult` |
| `DiffNetworksFromTar(oldTar, newTar)` | Compares differences between two tar archives and returns `*DiffResult` |
| `ComputeNetworkChecksums(fsys, root)` | Computes the checksum map for a network directory |

### Validator

| Function | Description |
|------|------|
| `ValidateNetwork(doc)` | Validates the network structure and returns `*ValidationResult` |

### FileSystem

| Function | Description |
|------|------|
| `NewOSFileSystem()` | OS-backed file system implementation |
| `NewMemoryFileSystem()` | In-memory file system for tests or tar extraction |

---

## Tools

### regenerate_checksum.go

Regenerates CHECKSUM files for all networks under `examples/` in bulk. Run it after example content changes, such as renaming files, modifying .bkn files, or updating SKILL.md.

```bash
# Run from sdk/golang/ and pass the examples parent directory.
go run tools/regenerate_checksum.go ../../examples
```

## Tests

```bash
# Unit tests.
go test ./bkn/... -v

# Integration tests using real networks from tests/testdata/.
go test ./tests/... -v

# All tests.
go test ./... -v
```
