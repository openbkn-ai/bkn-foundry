{{/* vim: set filetype=mustache: */}}
{{/* Expand the name of the chart. */}}

{{- define "vegagatewaypro.name" -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" }}
{{- end -}}


{{/* Generate vegagatewaypro image */}}
{{- define "vegagatewaypro.image" -}}
{{- if .Values.image.registry }}
{{- printf "%s/%s:%s" .Values.image.registry .Values.image.repository .Values.image.tag -}}
{{- else -}}
{{- printf "%s:%s" .Values.image.repository .Values.image.tag -}}
{{- end -}}
{{- end -}}