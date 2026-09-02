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
- `anonymous`: anonymous access.

## Usage.

### 1. Set authentication header in HTTP request.
```go
req.Header.Set("x-account-id", "user-123")
req.Header.Set("x-account-type", "user")
```

# Operator operation tool.

This tool is used to register, update, query, delete, publish, offline and other operations on operators.

[Tool introduction](./server/tests/tool/README.MD)
