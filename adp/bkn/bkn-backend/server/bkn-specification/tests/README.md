# BKN Go SDK Integration Tests

The integration tests verify the complete interaction between the SDK, file system, and tar archives.

## Test Design Principles

- **Less is more**: 6 core workflows + 5 edge cases
- **Strict verification**: use deep comparison to ensure data consistency
- **Real data**: use example networks from the `examples/` directory

## Core Workflow Tests (6)

| Test | Path | Verification |
|------|------|--------------|
| TestLoadFromFile | File -> Model | Strict deep comparison |
| TestLoadFromTar | Tar -> Model | Strict deep comparison |
| TestSaveToFile | Model -> File | Strict comparison after reloading |
| TestWriteToTar | Model -> Tar | Strict comparison after reloading |
| TestRoundTrip_FileToTar | File -> Model -> Tar -> Model | Strict consistency from start to end |
| TestRoundTrip_TarToTar | Tar -> Model -> Tar -> Model | Strict consistency from start to end |

## Edge Case Tests (5)

| Test | Scenario | Expected Behavior |
|------|----------|-------------------|
| TestEmptyNetwork | Empty network | Loads normally and returns an empty structure |
| TestMissingRootFile | Directory without network.bkn | Returns an error: root file not found |
| TestInvalidFrontmatter | Invalid YAML | Returns a parse failure |
| TestCircularInclude | Circular include | Returns an error: cycle detected |
| TestMissingInclude | Included file does not exist | Returns an error: file not found |

## Running Tests

```bash
cd sdk/golang/test

# Run all integration tests.
go test -v ./...

# Run only core workflows.
go test -v -run "TestLoad|TestSave|TestWrite|TestRoundTrip" ./...

# Run only edge cases.
go test -v -run "TestEmpty|TestMissing|TestInvalid|TestCircular" ./...
```

## Difference from Unit Tests

| | Unit tests (bkn/) | Integration tests (test/) |
|--|-------------------|---------------------------|
| Count | ~30 | 11 |
| Dependencies | No external dependencies | Depends on the file system and example data |
| Speed | < 1 second | 1-3 seconds |
| Run timing | On every save | Before commit and in CI/CD |

## Related Documentation

- [BKN design document](../../../design/bkn/features/bkn_docs/DESIGN.md)
- [SDK usage guide](../README.md)
