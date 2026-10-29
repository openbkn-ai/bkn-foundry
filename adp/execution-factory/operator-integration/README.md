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

### 3.4 PyPI mirror policy (upgrade note).
`GET /function/dependency-versions/{package_name}` only queries PyPI indexes listed in
`pypi.trusted_index_urls` (Helm: `service.pypi.trustedIndexURLs`, default
`["https://pypi.org/simple"]`). A `pypi_repo_url` outside the list is rejected with 400; when it is
omitted, the first listed index is used. Add internal mirrors to the list before callers use them.

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

`BKN_SAFE_URL` is required. It must be an absolute
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

### Outbound TLS and upgrade (schema 0.2.1)

Outbound HTTPS verifies the certificate chain and hostname against the system trust
store. An optional private CA is mounted from a Secret:

```yaml
outboundTLS:
  caSecretName: private-ca
  caSecretKey: ca.crt
```

The Secret key must contain PEM CA certificates. A missing or malformed bundle is
a startup error. Add the CA in addition to the system roots; a CA alone cannot fix
an ingress certificate whose SAN does not match the access address.

Run the ordinary schema migrator before deploying this binary. The additive
`t_outbound_tls_trust` table is introduced in the 0.2.1 fresh-install and upgrade
scripts. Before handlers or reconcilers start, the service enrolls the existing
MCP draft/released URLs and toolbox URLs. It uses a credential-free TLS handshake
only. Publicly trusted endpoints retain normal CA verification; untrusted/private
certificates are saved as SHA-256 leaf-certificate fingerprints. This covers the
default ingress fake certificate, including its missing IP SAN. Enrollment failure
(e.g. an unavailable endpoint or an expired certificate) stops startup with the
registration ID. Already saved policies are retained without probing on later
starts and repeated upgrades.

New MCP/toolbox registrations and URL changes enroll their trust in the same
transaction as the registration. Ordinary requests never enroll or refresh a
certificate. Pins and explicit exceptions belong to one registration and HTTPS
authority (hostname plus port); redirects cannot inherit another authority's
exception. HTTP service-to-service calls keep their existing behavior.

A changed pin fails with `TLS_CERTIFICATE_CHANGED`. Inspect the replacement
certificate through your normal certificate-distribution process. The fingerprint
can also be read without sending application credentials:

```sh
openssl s_client -connect mcp.example.com:443 -servername mcp.example.com </dev/null 2>/dev/null | openssl x509 -outform DER | openssl dgst -sha256
```

Read the saved `tls` policy from MCP `base_info.tls` or the toolbox detail's `tls`.
Explicitly re-trust through the existing authenticated **full update** endpoint:
`PUT /api/agent-operator-integration/v1/mcp/{mcp_id}` or
`PUT /api/agent-operator-integration/v1/tool-box/{box_id}`. Include all the endpoint's
usual required update fields, adding this JSON fragment (replace the example
fingerprints with the saved and inspected SHA-256 hex digests):

```json
{
  "tls_change": {
    "expected": {"fingerprint": "<saved 64-character digest>", "skip_verify": false},
    "policy": {"fingerprint": "<replacement 64-character digest>", "skip_verify": false}
  }
}
```

The CLI may submit the same PUT request. The existing registration's `modify`
permission is required. A concurrent trust change returns HTTP 409; read the policy
again before retrying. Omitting `tls_change` preserves trust. Use an empty fingerprint and
`skip_verify: false` for ordinary CA verification. A per-registration emergency
exception uses an empty fingerprint and `skip_verify: true`; it defaults to false
and cannot be combined with a pin. An optional `tls_change.endpoint` addresses an already
enrolled previous HTTPS authority for that same registration. Certificate expiry
requires certificate renewal; it is not silently accepted by a pin. Runtime trust
store failures reject the request even if a connection was previously pooled.

Configured private service dependencies (as opposed to registered MCP/toolboxes)
may specify `outboundTLS.endpoints` entries with `url`, `fingerprint`, and
`skip_verify`. These explicit configuration policies survive pod restarts in the
Helm configuration. There is no global verification-disable switch. Reverting the
binary does not require deleting the additive trust table; retain it with normal
database backups so a later rollout does not lose established trust.

The authorized detail response also includes `tls_policies`, keyed by HTTPS
authority, for inspecting saved historical/fallback endpoints before a change
using `tls_change.endpoint`. This read does not probe endpoints or modify trust.
