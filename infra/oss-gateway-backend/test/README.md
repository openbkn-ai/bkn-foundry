# OSS Gateway Test Guide

## Test Structure

Test files are located under the `test/` directory and are organized as follows:

```
test/
├── handler/          # Handler-layer tests
│   ├── storage_test.go
│   ├── upload_test.go
│   ├── download_test.go
│   ├── head_test.go
│   ├── delete_test.go
│   └── health_test.go
└── pkg/             # Utility package tests
    ├── crypto_test.go
    ├── response_test.go
    └── utils_test.go
```

## Running Tests

### Run all tests

```bash
make test
```

or:

```bash
go test ./test/... -v
```

### Run tests and generate a coverage report

```bash
make test-coverage
```

or:

```bash
go test ./test/handler/... ./test/pkg/... -v -coverprofile=coverage.out -coverpkg=./internal/...,./pkg/...
```

### View the HTML coverage report

```bash
go tool cover -html=coverage.out
```

## Test Coverage Scope

### Handler-layer tests (test/handler/)

- **storage_test.go**: Storage configuration management API tests
  - Create, update, delete, and query storage configurations
  - Storage connection checks
  - Parameter validation and error handling

- **upload_test.go**: Upload API tests
  - Single-file upload URL retrieval
  - Multipart upload initialization
  - Multipart upload URL retrieval
  - Multipart upload completion

- **download_test.go**: Download API tests
  - Download URL retrieval
  - Custom file names
  - Internal-network access

- **head_test.go**: Metadata query tests
  - Single-object metadata URL retrieval
  - Batch object metadata URL retrieval

- **delete_test.go**: Delete API tests
  - Delete URL retrieval
  - URL encoding handling

- **health_test.go**: Health-check tests
  - Liveness checks
  - Readiness checks

### Utility package tests (test/pkg/)

- **crypto_test.go**: AES encryption/decryption tests
  - Key validation
  - Encryption and decryption functionality
  - Large-data handling
  - Error handling

- **response_test.go**: Response-format tests
  - Success responses
  - Error responses
  - Various error codes

- **utils_test.go**: Utility function tests
  - ID generation
  - Endpoint parsing
  - Parameter validation

## Test Coverage

Current test coverage is above **50%**, mainly covering:

- All HTTP APIs in the handler layer
- Core utility package functions
- Error-handling logic

## Adding New Tests

1. Create a `*_test.go` file under the appropriate directory.
2. Write test cases with the `testify` framework.
3. Mock external dependencies such as databases, Redis, and OSS adapters.
4. Run tests to verify the functionality.

## Dependencies

The test framework uses:

- `github.com/stretchr/testify` - Assertions and mocks
- `net/http/httptest` - HTTP tests
- `github.com/gin-gonic/gin` - Gin framework test mode
