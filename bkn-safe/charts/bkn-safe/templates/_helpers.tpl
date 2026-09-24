{{/*
bkn-safe.instanceID resolves OPENBKN_INSTANCE_ID — the instance identity a
license activation is bound to (licverify hashes it into the fingerprint).

Stability is the whole point: the fingerprint must survive Pod rebuilds and
Helm upgrades, because a changed fingerprint invalidates an activated license
(openbkn-ai/bkn-foundry#508). Resolution order:

  1. an explicit config.license.instanceId wins. Note this is the OVERRIDE, not
     the sticky path: a bare `helm upgrade --set config.license.instanceId=…`
     moves the identity, and moving it invalidates the activated license. That
     is deliberate — it is the escape hatch for pinning an identity by hand —
     but it means the chart alone cannot protect an operator from typing it.
     deploy.sh is what makes the normal path sticky: it reads the value already
     in the cluster FIRST and passes that back here, and only derives from the
     node (status.nodeInfo.systemUUID, falling back to nodeInfo.machineID) when
     the cluster has none;
  2. otherwise reuse the value already stored in the release's instance-id
     ConfigMap, so a `helm upgrade` that passes no value at all — the plain
     `helm upgrade` an operator runs by hand — does not silently drop the
     identity;
  3. otherwise empty — no key is rendered, and licverify falls back to its own
     host-identity chain. Inside a Pod that chain finds nothing durable and
     fails loudly rather than inventing a value that drifts.

Deliberately NOT randAlphaNum like bkn-safe.hydraSecret: a generated identity
would be stable in this cluster yet meaningless as a machine identity, and it
would travel with any copy of the config. Derive from the host or render nothing.
*/}}
{{- define "bkn-safe.instanceID" -}}
{{- if .Values.config.license.instanceId -}}
{{- .Values.config.license.instanceId -}}
{{- else -}}
{{- $existing := lookup "v1" "ConfigMap" .Release.Namespace (printf "%s-instance-id" .Release.Name) -}}
{{- if and $existing $existing.data -}}
{{- index $existing.data "OPENBKN_INSTANCE_ID" | default "" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/* Canonical browser-facing Hydra origin (the single OIDC issuer). */}}
{{- define "bkn-safe.hydraBrowserPublicURL" -}}
{{- if .Values.bundledDeps.publicBaseURL -}}
{{- .Values.bundledDeps.publicBaseURL | trimSuffix "/" -}}
{{- else if and .Values.accessAddress .Values.accessAddress.host -}}
{{- $aa := .Values.accessAddress -}}
{{- $scheme := $aa.scheme | default "https" -}}
{{- $port := $aa.port | toString -}}
{{- if or (and (eq $scheme "https") (eq $port "443")) (and (eq $scheme "http") (eq $port "80")) -}}
{{- printf "%s://%s" $scheme $aa.host -}}
{{- else -}}
{{- printf "%s://%s:%s" $scheme $aa.host $port -}}
{{- end -}}
{{- else if .Values.config.hydra.browserPublicURL -}}
{{- .Values.config.hydra.browserPublicURL | trimSuffix "/" -}}
{{- else if .Values.bundledDeps.enabled -}}
{{- printf "http://%s-hydra-public:4444" .Release.Name -}}
{{- else -}}
{{- .Values.config.hydra.publicURL | trimSuffix "/" -}}
{{- end -}}
{{- end -}}

{{/* Deployment-owned Studio callback baseline as a JSON array. */}}
{{- define "bkn-safe.studioRedirectURIs" -}}
{{- $web := .Values.clientSeed.webRedirectUri -}}
{{- if and .Values.accessAddress .Values.accessAddress.host -}}
{{- /* Keep this deployment-owned callback source compatible with the original
      seed job. Hydra's issuer may deliberately use a different public origin. */ -}}
{{- $aa := .Values.accessAddress -}}
{{- $scheme := $aa.scheme | default "https" -}}
{{- $port := $aa.port | toString -}}
{{- if or (and (eq $scheme "https") (eq $port "443")) (and (eq $scheme "http") (eq $port "80")) -}}
{{- $web = printf "%s://%s/studio/callback" $scheme $aa.host -}}
{{- else -}}
{{- $web = printf "%s://%s:%s/studio/callback" $scheme $aa.host $port -}}
{{- end -}}
{{- end -}}
{{- $uris := list $web -}}
{{- range .Values.clientSeed.extraWebRedirectUris -}}
{{- if not (has . $uris) -}}{{- $uris = append $uris . -}}{{- end -}}
{{- end -}}
{{- $uris | toJson -}}
{{- end -}}

{{/* Deployment-owned Studio post-logout baseline as a JSON array. */}}
{{- define "bkn-safe.studioLogoutURIs" -}}
{{- $redirects := include "bkn-safe.studioRedirectURIs" . | fromJsonArray -}}
{{- $uris := list -}}
{{- range $redirects -}}
{{- $uris = append $uris (trimSuffix "/callback" .) -}}
{{- end -}}
{{- $uris | toJson -}}
{{- end -}}

{{/*
bkn-safe.hydraSecret resolves the hydra SECRETS_SYSTEM value.

hydra uses this key to sign/encrypt session and token material, so it must be a
per-install secret — never a shipped constant. But it must also be STABLE:
changing it invalidates every active session and makes hydra unable to decrypt
data written under the old key. The resolution order below gives a random value
to fresh installs while never rotating an existing one:

  1. an explicit override in values (bundledDeps.hydraSecretsSystem) wins, so an
     operator can still pin or rotate deliberately;
  2. otherwise reuse the value already stored in this release's Secret — this is
     the steady state on every upgrade once the Secret exists;
  3. otherwise, if this is an UPGRADE of an install that predates the Secret,
     carry the historical chart default forward unchanged, so upgrading an
     existing deployment never rotates its key;
  4. otherwise (a genuinely fresh install) generate a random 48-char value.

Actual rotation on an existing environment is a deliberate, out-of-band step
(clear the Secret / set an explicit override during a maintenance window), not a
side effect of `helm upgrade`.
*/}}
{{- define "bkn-safe.hydraSecret" -}}
{{- if .Values.bundledDeps.hydraSecretsSystem -}}
{{- .Values.bundledDeps.hydraSecretsSystem -}}
{{- else -}}
{{- $secretName := printf "%s-hydra-secrets" .Release.Name -}}
{{- $existing := lookup "v1" "Secret" .Release.Namespace $secretName -}}
{{- if and $existing $existing.data (index $existing.data "SECRETS_SYSTEM") -}}
{{- index $existing.data "SECRETS_SYSTEM" | b64dec -}}
{{- else if lookup "apps/v1" "Deployment" .Release.Namespace (printf "%s-hydra" .Release.Name) -}}
{{- /* A bundled hydra Deployment already exists but no hydra Secret does: this
       is an install that predates the Secret, and hydra is running under the
       old inline constant. Carry it forward so upgrading never rotates the key.
       Gating on the hydra Deployment (not merely .Release.IsUpgrade) matters:
       an upgrade that flips bundledDeps.enabled false->true for the first time
       has no prior bundled hydra, so it must NOT inherit the public constant —
       it falls through to a fresh random below. */ -}}
{{- "dev-only-change-me-32-bytes-secret" -}}
{{- else -}}
{{- randAlphaNum 48 -}}
{{- end -}}
{{- end -}}
{{- end -}}
