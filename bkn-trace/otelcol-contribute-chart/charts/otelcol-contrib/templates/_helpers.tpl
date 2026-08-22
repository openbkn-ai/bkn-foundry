{{/*
Copyright (c) 2026 OpenBKN
SPDX-License-Identifier: LicenseRef-OpenBKN
Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.
*/}}

{{- define "otelcol-contrib.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "otelcol-contrib.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "otelcol-contrib.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "otelcol-contrib.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}
