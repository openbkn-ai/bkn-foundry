# Sandbox Standalone Helm Chart

This chart deploys Sandbox as a self-contained application.

Use this chart for development, demos, integration testing, or environments where Sandbox should be deployed independently from BKN Foundry. It owns its supporting services and is not constrained by the Core component values format.

For BKN Foundry component packaging, use `deploy/helm/sandbox`.

## Components

- **Control Plane**: FastAPI management service for sessions, templates, scheduling, and storage access.
- **Web Console**: React application for visual session, template, and execution management.
- **MariaDB**: Internal database for Sandbox data.
- **MinIO**: S3-compatible workspace object storage.
- **Default template metadata**: Initializes `python-basic` and `multi-language` template records.

## Prerequisites

- Kubernetes 1.24+
- Helm 3.0+
- A CNI plugin that enforces Kubernetes `NetworkPolicy`
- PV provisioner support for MariaDB and MinIO persistence

## Installing

From the repository root:

```bash
helm install sandbox deploy/helm/sandbox_standalone \
  --namespace sandbox-system \
  --create-namespace
```

With an override file:

```bash
helm install sandbox deploy/helm/sandbox_standalone \
  --namespace sandbox-system \
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
      repository: sandbox-template-python-basic
      tag: ""
    multiLanguage:
      repository: sandbox-template-multi-language
      tag: ""
```

An empty `tag` means the Control Plane reads `/app/VERSION` at startup and uses that value as the template image tag. Set the tag only when deploying branch or custom template images.

The deprecated `image.defaultTemplate` value is still accepted as a compatibility fallback for `python-basic`.

## Key Values

| Parameter | Description | Default |
|-----------|-------------|---------|
| `namespace` | Kubernetes namespace | `sandbox-system` |
| `imagePullPolicy` | Executor Pod image pull policy | `IfNotPresent` |
| `imagePullSecrets` | Executor Pod image pull secret names | `[]` |
| `image.registry` | Shared image registry prefix | `""` |
| `image.controlPlane.repository` | Control Plane image repository | `sandbox-control-plane` |
| `image.web.repository` | Web Console image repository | `sandbox-web` |
| `image.mariadb.repository` | MariaDB image repository | `mariadb` |
| `image.minio.repository` | MinIO image repository | `quay.io/minio/minio` |
| `image.defaultTemplates.pythonBasic.tag` | Python template image tag; empty uses `/app/VERSION` | `""` |
| `image.defaultTemplates.multiLanguage.tag` | Multi-language template image tag; empty uses `/app/VERSION` | `""` |
| `mariadb.enabled` | Deploy internal MariaDB | `true` |
| `web.enabled` | Deploy Sandbox Web Console | `true` |
| `minio.enabled` | Deploy internal MinIO | `true` |
| `networkPolicy.enabled` | Apply a default-deny egress policy to dynamic executor pods | `true` |
| `networkPolicy.publicHttps.enabled` | Allow public HTTPS while excluding cluster, private, link-local, and reserved address space; required by runtime dependency installation | `true` |
| `networkPolicy.bkn.enabled` | Allow an optional BKN deployment through agent-retrieval port 30780 | `false` |
| `networkPolicy.bkn.namespace` | Agent-retrieval namespace override; empty derives it from the MCP Service FQDN or uses the Sandbox namespace for a short service name | `""` |
| `networkPolicy.gateway` | Independent REST gateway namespace, Pod selector and target port. Complete installation discovers these from the ingress controller Service; configure them explicitly for a custom gateway. | disabled until configured or discovered |
| `networkPolicy.additionalEgress` | Extra Kubernetes egress rules for explicitly approved dependencies | `[]` |

## Executor Egress Isolation

The policy selects only dynamic executor pods. It allows DNS, the Sandbox
Control Plane, MinIO, and public HTTPS. The public HTTPS rule excludes private,
cluster, link-local, metadata, multicast, and reserved address ranges, which
keeps runtime dependency installation working without reopening platform
services. Standard Kubernetes NetworkPolicy cannot allow the package index by
DNS name, so this is the portable L3/L4 boundary. Disable
`networkPolicy.publicHttps.enabled` when dependencies are prebuilt.

MariaDB is used by the Control Plane, not executors, so it is intentionally not
allowed. Standalone installs do not include BKN, so BKN access is disabled by
default. When connecting one, set `controlPlane.env.BKN_BASE_URL` to the REST
gateway and enable/configure `networkPolicy.gateway`. Configure the optional MCP
endpoint separately on port 30780 and enable `networkPolicy.bkn` only if needed. An empty
`networkPolicy.bkn.namespace` derives the namespace from an in-cluster service
FQDN; set it explicitly for any other addressing convention. Never allow the
main port 30779 because that port also serves trusted `/in` routes.

The policy is enabled by default so fresh installations do not start with an
open executor network boundary. Existing installations must use a staged
upgrade: keep `networkPolicy.enabled=false` during the observation period,
collect executor traffic with the CNI's flow tooling, add only verified
dependencies to `networkPolicy.additionalEgress`, and enable the policy only
after validation. Public HTTPS is allowed by default for runtime package
installation; private-network and non-HTTPS public calls remain denied.

If this standalone installation connects to BKN, upgrade agent-retrieval and
verify port 30780 before changing the MCP URL from 30779 to 30780. When the
policy and MCP rule are enabled, Helm validates the MCP URL independently from
the REST gateway URL. Applying the policy affects already running executor
pods. NodeLocal DNSCache users must also allow its listener address explicitly.

## Access

```bash
kubectl port-forward svc/sandbox-control-plane 8000:8000 -n sandbox-system
kubectl port-forward svc/sandbox-web 1101:80 -n sandbox-system
kubectl port-forward svc/minio 9001:9001 -n sandbox-system
```

## Rendering

```bash
helm template sandbox deploy/helm/sandbox_standalone
```

To verify manual template versions:

```bash
helm template sandbox deploy/helm/sandbox_standalone \
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
