{{/* vim: set filetype=mustache: */}}
{{/* Expand the name of the chart. */}}

{{- define "bkn-project.name" -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" }}
{{- end -}}


{{/* Generate bkn-project image */}}
{{- define "bkn-project.image" -}}
{{- if .Values.image.registry }}
{{- printf "%s/%s:%s" .Values.image.registry .Values.image.repository .Values.image.tag -}}
{{- else -}}
{{- printf "%s:%s" .Values.image.repository .Values.image.tag -}}
{{- end -}}
{{- end -}}


{{/* The Secret holding the database account: keys user and password. */}}
{{- define "bkn-project.dbSecret" -}}
{{- .Values.depServices.rds.existingSecret | default (printf "%s-db" .Values.moduleName) -}}
{{- end -}}
