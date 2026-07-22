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
