# Sandbox Core Component Helm Chart

This chart deploys Sandbox as a BKN Foundry component.

Use this chart when Sandbox is installed by, or packaged into, BKN Foundry. It follows the Core component values format and expects shared platform services, especially the database service, to be provided by Core.

For a self-contained Sandbox deployment with MariaDB and the web console, use `deploy/helm/sandbox_standalone`.

## Components

- **Control Plane**: FastAPI management service for sessions, templates, scheduling, and storage access.
- **MinIO**: S3-compatible workspace object storage when enabled by this chart.
- **Default template metadata**: Initializes `python-basic` and `multi-language` template records through Control Plane environment variables.

This chart does not deploy `sandbox_web` or MariaDB.

## Prerequisites

- Kubernetes 1.24+
- Helm 3.0+
- A CNI plugin that enforces Kubernetes `NetworkPolicy`
- BKN Foundry deployment environment
- Core-provided database service through `depServices.rds`

## Installing

From the repository root:

```bash
helm install sandbox deploy/helm/sandbox --namespace anyshare --create-namespace
```

With an override file:

```bash
helm install sandbox deploy/helm/sandbox \
  --namespace anyshare \
  --create-namespace \
  -f custom-values.yaml
```

## Default Template Images

The Control Plane seeds two default template rows into `t_sandbox_template`:

- `python-basic`
- `multi-language`

The chart exposes both image tags independently:

```yaml
image:
  defaultTemplates:
    pythonBasic:
      repository: dip/sandbox-template-python-basic
      tag: ""
    multiLanguage:
      repository: dip/sandbox-template-multi-language
      tag: ""
```

An empty `tag` means the Control Plane reads `/app/VERSION` at startup and uses that value as the template image tag. Set the tag only when deploying branch or custom template images.

The deprecated `image.defaultTemplate` value is still accepted as a compatibility fallback for `python-basic`.

## Key Values

| Parameter | Description | Default |
|-----------|-------------|---------|
| `namespace` | Kubernetes namespace | `anyshare` |
| `imagePullPolicy` | Executor Pod image pull policy | `IfNotPresent` |
| `imagePullSecrets` | Executor Pod image pull secret names | `[]` |
| `image.registry` | Shared image registry prefix | `acr.aishu.cn` |
| `image.controlPlane.repository` | Control Plane image repository | `dip/sandbox-control-plane` |
| `image.controlPlane.tag` | Control Plane image tag | `latest` |
| `image.defaultTemplates.pythonBasic.repository` | Python template image repository | `dip/sandbox-template-python-basic` |
| `image.defaultTemplates.pythonBasic.tag` | Python template image tag; empty uses `/app/VERSION` | `""` |
| `image.defaultTemplates.multiLanguage.repository` | Multi-language template image repository | `dip/sandbox-template-multi-language` |
| `image.defaultTemplates.multiLanguage.tag` | Multi-language template image tag; empty uses `/app/VERSION` | `""` |
| `controlPlane.env.ENVIRONMENT` | Control Plane environment | `staging` |
| `controlPlane.env.BKN_BASE_URL` | In-cluster platform address written into every sandbox pod, so `bkn-osdk` reaches BKN with no `configure()` call in user code. Host and port only — the SDK appends its own paths (`/api/agent-retrieval/v1/kn/` for REST, `/api/agent-retrieval/v1/mcp` for the MCP face), so no trailing slash and no path. **Defaults to empty on purpose**: the control plane prefers this key, so a shipped default would override the address an existing deployment already set in `BKN_SANDBOX_MCP_URL` — including the FQDN a cross-namespace install needs. Empty lets that derivation run, which is what makes an upgrade need no values change. Set it explicitly on a new install. Both keys empty means functions that call BKN fail with the SDK's "No base URL" error. | `""` |
| `controlPlane.env.BKN_SANDBOX_MCP_URL` | In-cluster Context Loader MCP endpoint for the built-in `sandbox_sdk.bkn` face. It must use the authenticated-only sandbox listener on port 30780; port 30779 also carries trusted `/in` routes and must not be reachable from executors. | `http://agent-retrieval:30780/api/agent-retrieval/v1/mcp/` |
| `depServices.rds` | Core-provided database service configuration | enabled by values |
| `networkPolicy.enabled` | Apply a default-deny egress policy to dynamic executor pods | `true` |
| `networkPolicy.publicHttps.enabled` | Allow public HTTPS while excluding cluster, private, link-local, and reserved address space; required by runtime dependency installation | `true` |
| `networkPolicy.bkn.namespace` | Agent-retrieval namespace override; empty derives it from an in-cluster BKN FQDN or uses the Sandbox namespace for a short service name | `""` |
| `networkPolicy.bkn.port` | Authenticated-only agent-retrieval listener allowed from executors | `30780` |
| `networkPolicy.additionalEgress` | Extra Kubernetes egress rules for explicitly approved dependencies | `[]` |

## Executor Egress Isolation

The chart selects only dynamic execution pods carrying all three labels:
`app=sandbox-executor`, `managed_by=sandbox-control-plane`, and
`sandbox-type=execution`. Platform services and the Control Plane are not
isolated by this policy.

By default, executors can reach DNS, the Sandbox Control Plane, Sandbox MinIO,
agent-retrieval's authenticated-only port 30780, and public HTTPS destinations.
The HTTPS rule excludes private, cluster, link-local, metadata, multicast, and
reserved address ranges, so the existing runtime package installation path can
reach its HTTPS package index without reopening platform services. Standard
Kubernetes NetworkPolicy cannot allow a DNS name directly, so this is the
portable L3/L4 boundary. Disable `networkPolicy.publicHttps.enabled` when all
dependencies are prebuilt and public HTTPS is not required.

All other destinations, including platform `/in` and `internal-v1` ports,
private-network services, and non-HTTPS public endpoints, are denied. Add an
explicit `networkPolicy.additionalEgress` rule only for a verified dependency.
Never add agent-retrieval port 30779 or platform internal service ports.

When `networkPolicy.bkn.namespace` is empty, the chart derives a namespace from
an in-cluster FQDN such as `agent-retrieval.openbkn.svc.cluster.local`; a short
service name uses the Sandbox namespace. Set the value explicitly for any other
addressing convention.

For an existing installation, first render or upgrade with
`networkPolicy.enabled=false` while collecting executor egress with the CNI's
flow-observation facility. Add only verified legitimate destinations, then
enable the policy. Applying an enabled policy immediately affects already
running executor pods. If NodeLocal DNSCache is used, also allow its documented
listener address through `networkPolicy.additionalEgress`; the default DNS rule
selects CoreDNS pods in `kube-system`.

## Rendering

```bash
helm template sandbox deploy/helm/sandbox
```

To verify manual template versions:

```bash
helm template sandbox deploy/helm/sandbox \
  --show-only templates/configmap.yaml \
  --set image.defaultTemplates.pythonBasic.tag=dev-python \
  --set image.defaultTemplates.multiLanguage.tag=dev-multi
```

## Related Chart

See `deploy/helm/README.md` for the comparison between `sandbox` and `sandbox_standalone`.
