# Operator integration.

## 1. Project structure.
The directory structure of the project is as follows:

```
.
├── azure-pipelines.yml
├── docker
│   ├── Dockerfile
│   └── Makefile
├── docs
│   ├── apis
│   ├── build.sh
│   ├── data
│   └── preview.sh
├── go.mod
├── go.sum
├── helm
│   └── agent-operator-integration
├── migrations
│   ├── 1.0.0
│   ├── init.sql
│   └── readme.md
├── project.sh
├── README.md
├── server
│   ├── dbaccess
│   ├── drivenadapters
│   ├── driveradapters
│   ├── infra
│   ├── interfaces
│   ├── logics
│   ├── main.go
│   ├── mocks
│   ├── tests
│   └── utils
├── sonar-scanner.properties
└── VERSION
```


## 2. Project dependencies.
- database.
- hydra
- bkn-safe

## 3. Project construction.
### 3.1 Compilation.
```shell
go build -o agent-operator-integration ./server/main.go
```
### 3.2 Packaging.
```shell
docker build -t agent-operator-integration:latest .
```
### 3.3 Deployment.
```shell
helm install agent-operator-integration ./helm/agent-operator-integration
```

## 4. Project testing.
- [Registration query related](./server/tests/http/register interface related test data.md)
- [Update and delete related](./server/tests/http/operator.http)

## 5. Project operation.
- [Compile and run related scripts](./project.sh)

# API documentation.
## HTML format.
- [External interface documentation](./docs/apis/api_public/operator.html)
- [Internal interface documentation](./docs/apis/api_private/operator.html)
## YAML format.
- [External interface documentation](./docs/apis/api_public/operator.yaml)
- [Internal interface documentation](./docs/apis/api_private/operator.yaml)

#Test data.

- [Registration query related](./server/tests/http/register interface related test data.md)
- [Update and delete related](./server/tests/http/operator.http)
- [Process Scenario Test](./server/tests/http/scenario.md)

## Test file.
### JSON file.
- [json/auth.json](./server/tests/file/json/auth.json)
- [json/file_decrypt.json](./server/tests/file/json/file_decrypt.json)
- [full_text_subdoc.jso](./server/tests/file/json/full_text_subdoc.json)

### YAML files.
- [template.yaml](./server/tests/file/yaml/template.yaml)
- [test.yaml](./server/tests/file/yaml/test.yaml)

# Authentication header parameters.

## Overview.
Microservice internal interface calls need to pass authentication parameters in the HTTP header to identify the caller's identity information.

## Authentication header parameters.
- `x-account-id`: Account ID, uniquely identifies the caller.
- `x-account-type`: Account type, supports the following types:
- `user`: user account.
- `app`: application account.

These headers are only for trusted internal service-to-service calls. Public
APIs derive the subject from OAuth or AppKey authentication; request body fields
and caller-supplied identity headers cannot replace it. A missing, disabled, or
unknown subject has no execution permission.

## Usage.

### 1. Set authentication header in HTTP request.
```go
req.Header.Set("x-account-id", "user-123")
req.Header.Set("x-account-type", "user")
```

## Authorization

When `AUTH_ENABLED=true`, `BKN_SAFE_URL` is required. It must be an absolute
HTTP(S) service URL without credentials, query, fragment, or a non-root path;
invalid configuration stops the service at startup.

Tool, MCP, operator, and Skill execution checks the authenticated subject's
`execute` operation on the concrete `tool_box`, `mcp`, `operator`, or `skill`
resource. A denied or failed decision returns 403 and the target is not invoked.
Platform identity and trace-control headers are stripped before a tool or MCP
request leaves the platform, so downstream third parties do not receive the
OpenBKN account context.

For knowledge-network Action execution, ontology-query also checks the concrete
Action Type and all referenced data dependencies before execution-factory calls
the target. See the
[shared authorization contract](../../../docs/api/knowledge-network-authorization.md).

# Operator operation tool.

This tool is used to register, update, query, delete, publish, offline and other operations on operators.

[Tool introduction](./server/tests/tool/README.MD)
