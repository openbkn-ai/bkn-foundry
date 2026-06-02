#!/usr/bin/env bash
# onboard_oss_storage.sh — register a default OSS storage in oss-gateway-backend.
#
# Skill registration and other file-asset features call oss-gateway's
# GetDefaultStorageID (GET /storages?enabled=true&is_default=true). A fresh
# install has an empty t_storage_config, so those features fail with
# AgentOperatorIntegration.NotFound.OSSGatewayDefaultStorageNotFound.
#
# This provisions one default storage backed by the in-cluster MinIO that the
# sandbox chart deploys. Idempotent: a no-op when a default already exists, or
# when MinIO / oss-gateway are absent (e.g. a deployment without sandbox).
#
# Tunables (env):
#   OSS_DEFAULT_BUCKET        bucket to use/create (default: kweaver-oss)
#   OSS_DEFAULT_STORAGE_NAME  display name        (default: minio-default)
#   ONBOARD_SKIP_OSS_STORAGE  set true to skip entirely

onboard_provision_oss_default_storage() {
    local ns="${1:-${NAMESPACE:-kowell}}"
    [[ "${ONBOARD_SKIP_OSS_STORAGE:-false}" == "true" ]] && return 0
    command -v kubectl >/dev/null 2>&1 || return 0

    local bucket="${OSS_DEFAULT_BUCKET:-kweaver-oss}"
    local sname="${OSS_DEFAULT_STORAGE_NAME:-minio-default}"

    # oss-gateway-backend must exist; otherwise nothing to configure.
    kubectl get svc oss-gateway-backend -n "${ns}" >/dev/null 2>&1 || {
        onboard_log_info "oss-gateway-backend not found in ${ns}; skipping default-storage provisioning"
        return 0
    }

    # Locate the in-cluster MinIO pod (deployed by the sandbox chart).
    local mpod
    mpod="$(kubectl get pod -n "${ns}" -l component=minio -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)"
    [[ -z "${mpod}" ]] && {
        onboard_log_warn "No MinIO pod (component=minio) in ${ns}; cannot auto-provision OSS default storage. Register one manually if skill register / file assets are needed."
        return 0
    }

    # Port-forward the internal API (the /storages endpoint is not on the
    # public ingress). Works from wherever kubectl works.
    local lport=18080 pf=""
    kubectl port-forward -n "${ns}" svc/oss-gateway-backend "${lport}:8080" >/dev/null 2>&1 &
    pf=$!
    # shellcheck disable=SC2064
    trap "kill ${pf} 2>/dev/null || true" RETURN
    local base="http://127.0.0.1:${lport}/api/v1"
    local i
    for i in $(seq 1 15); do
        curl -s --max-time 3 "${base}/storages" >/dev/null 2>&1 && break
        sleep 1
    done

    # Idempotent: a default already configured?
    if curl -s --max-time 5 "${base}/storages?enabled=true&is_default=true" 2>/dev/null | grep -q '"storage_id"'; then
        onboard_log_info "OSS default storage already configured; skipping"
        return 0
    fi

    # Read MinIO root credentials from the pod env (set by the chart). They are
    # used only to build the storage record and are never logged here.
    local muser mpass
    muser="$(kubectl exec -n "${ns}" "${mpod}" -- printenv MINIO_ROOT_USER 2>/dev/null | tr -d '\r\n')"
    mpass="$(kubectl exec -n "${ns}" "${mpod}" -- printenv MINIO_ROOT_PASSWORD 2>/dev/null | tr -d '\r\n')"
    [[ -z "${muser}" || -z "${mpass}" ]] && {
        onboard_log_warn "Could not read MinIO credentials from ${mpod}; skipping OSS default-storage provisioning"
        return 0
    }

    # Ensure the bucket exists (mc ships in the MinIO image). The mc alias name
    # must start with a letter — an underscore-prefixed alias is silently
    # rejected and the bucket is never created.
    kubectl exec -n "${ns}" "${mpod}" -- sh -c \
        'mc alias set kwoss "http://127.0.0.1:9000" "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD" >/dev/null 2>&1; mc mb -p "kwoss/'"${bucket}"'" >/dev/null 2>&1 || true' \
        >/dev/null 2>&1 || true

    # Register the default storage (vendor_type ECEPH = generic S3, fits MinIO).
    local endpoint="http://minio.${ns}.svc.cluster.local:9000"
    local body resp
    body="$(MUSER="${muser}" MPASS="${mpass}" SNAME="${sname}" BUCKET="${bucket}" EP="${endpoint}" python3 -c '
import json, os
print(json.dumps({
    "storage_name": os.environ["SNAME"],
    "vendor_type": "ECEPH",
    "endpoint": os.environ["EP"],
    "bucket_name": os.environ["BUCKET"],
    "access_key_id": os.environ["MUSER"],
    "access_key_secret": os.environ["MPASS"],
    "region": "us-east-1",
    "is_default": True,
}))')"
    resp="$(curl -s --max-time 15 -X POST "${base}/storages" \
        -H 'Content-Type: application/json' \
        -H 'x-business-domain: bd_public' \
        -d "${body}" 2>/dev/null)"

    if printf '%s' "${resp}" | grep -qE '"status":"ok"|"storage_id"|"id"'; then
        onboard_log_info "Registered default OSS storage '${sname}' → MinIO bucket '${bucket}'"
    else
        onboard_log_warn "OSS default-storage registration did not confirm (response: $(printf '%s' "${resp}" | head -c 200)). Skill register / file assets may fail until a default storage exists."
    fi
}
