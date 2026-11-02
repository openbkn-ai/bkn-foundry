{{/* vim: set filetype=mustache: */}}
{{/* Expand the name of the chart. */}}

{{- define "ontology-query.name" -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" }}
{{- end -}}

{{/* A stable release Secret keeps authenticated cursors usable after restart. */}}
{{- define "ontology-query.cursorSecretName" -}}
{{- default (printf "%s-query-cursor" .Release.Name | trunc 63 | trimSuffix "-") .Values.cursorEncryption.existingSecret -}}
{{- end -}}


{{/* Generate ontology-query image */}}
{{- define "ontology-query.image" -}}
{{- if .Values.image.registry }}
{{- printf "%s/%s:%s" .Values.image.registry .Values.image.repository .Values.image.tag -}}
{{- else -}}
{{- printf "%s:%s" .Values.image.repository .Values.image.tag -}}
{{- end -}}
{{- end -}}
