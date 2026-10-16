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
| `controlPlane.env.BKN_BASE_URL` | Explicit platform REST gateway origin; overrides installer-discovered `controlPlane.gateway.baseURL`. OSDK appends public `/api/bkn-backend/v1` and `/api/ontology-query/v1` routes. Never derived from the MCP endpoint. | `""` |
| `controlPlane.env.BKN_SANDBOX_MCP_URL` | In-cluster Context Loader MCP endpoint for the built-in `sandbox_sdk.bkn` face. It must use the authenticated-only sandbox listener on port 30780; port 30779 also carries trusted `/in` routes and must not be reachable from executors. | `http://agent-retrieval:30780/api/agent-retrieval/v1/mcp/` |
| `depServices.rds` | Core-provided database service configuration | enabled by values |
| `networkPolicy.enabled` | Apply a default-deny egress policy to dynamic executor pods | `true` |
| `networkPolicy.publicHttps.enabled` | Allow public HTTPS while excluding cluster, private, link-local, and reserved address space; required by runtime dependency installation | `true` |
| `networkPolicy.bkn.enabled` | Allow executor access to agent-retrieval's authenticated-only listener | `true` |
| `networkPolicy.bkn.namespace` | Agent-retrieval namespace override; empty derives it from the MCP Service FQDN or uses the Sandbox namespace for a short service name | `""` |
| `networkPolicy.bkn.port` | Authenticated-only agent-retrieval listener allowed from executors | `30780` |
| `networkPolicy.gateway` | Independent REST gateway namespace, Pod selector and target port. Complete installation discovers these from the ingress controller Service; configure them explicitly for a custom gateway. | disabled until configured or discovered |
| `networkPolicy.additionalEgress` | Extra Kubernetes egress rules for explicitly approved dependencies | `[]` |

## Executor Egress Isolation

The chart selects only dynamic execution pods carrying all three labels:
`app=sandbox-executor`, `managed_by=sandbox-control-plane`, and
`sandbox-type=execution`. Platform services and the Control Plane are not
isolated by this policy.

By default, executors can reach DNS, the Sandbox Control Plane, Sandbox MinIO,
the configured REST gateway, agent-retrieval's authenticated-only MCP port 30780, and public HTTPS destinations.
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

The policy is enabled by default so fresh installations do not start with an
open executor network boundary. Existing installations must use this staged
upgrade instead of applying the new default in one step:

1. Upgrade agent-retrieval first and verify that its Service and Pod answer
   `/health/ready` on port 30780.
2. Set `BKN_BASE_URL` to the unified gateway origin and `BKN_SANDBOX_MCP_URL`
   to the dedicated MCP listener on port 30780. Render or upgrade Sandbox with
   `networkPolicy.enabled=false` during observation. The MCP rule validates only
   the MCP endpoint; the gateway has its own namespace, selector and target port.
3. Collect executor traffic with the CNI's flow-observation facility and add
   only verified deployment-specific destinations.
4. Enable the policy and verify that executor calls to 30779/internal services
   are blocked while BKN on 30780 and runtime dependency installation work.

Applying an enabled policy immediately affects already running executor pods.
If NodeLocal DNSCache is used, also allow its documented listener address
through `networkPolicy.additionalEgress`; the default DNS rule selects CoreDNS
pods in `kube-system`.

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

## REST gateway resolution

The complete installer preserves an existing explicit `controlPlane.env.BKN_BASE_URL`
and applies current `-f` / `--set` overrides last. If no REST origin is configured,
it discovers exactly one ingress-nginx controller Service with an HTTP port. It
uses the Service name, namespace and Service port (not NodePort), and supplies
the matching Pod selector and target port to the independent gateway egress rule.
Discovery never uses `BKN_SANDBOX_MCP_URL`. Missing or ambiguous controllers stop
the installation before rollout; configure the origin and egress rule explicitly
for another gateway implementation or a cluster without Service discovery access.

`controlPlane.gateway` stores installer-owned discovery defaults in Helm values,
not additional environment variables. Standalone Helm installation has no installer
discovery: set `controlPlane.env.BKN_BASE_URL` and `networkPolicy.gateway` for OSDK.
A gateway with host-based routing must also accept its configured internal origin.
Public API authentication remains required; do not expose trusted `/in` routes.

Existing executor Pods retain their old environment. Apply this change with a
controlled Control Plane restart and executor/session recreation after preserving
workspaces; editing the ConfigMap alone does not refresh pooled executors.
