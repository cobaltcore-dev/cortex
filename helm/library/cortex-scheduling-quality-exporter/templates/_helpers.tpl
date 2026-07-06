{{/*
Expand the name of the chart.
*/}}
{{- define "cortex-scheduling-quality-exporter.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "cortex-scheduling-quality-exporter.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart label.
*/}}
{{- define "cortex-scheduling-quality-exporter.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "cortex-scheduling-quality-exporter.labels" -}}
helm.sh/chart: {{ include "cortex-scheduling-quality-exporter.chart" . }}
{{ include "cortex-scheduling-quality-exporter.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "cortex-scheduling-quality-exporter.selectorLabels" -}}
app.kubernetes.io/name: {{ include "cortex-scheduling-quality-exporter.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
ServiceAccount name.
*/}}
{{- define "cortex-scheduling-quality-exporter.serviceAccountName" -}}
{{- default (include "cortex-scheduling-quality-exporter.fullname" .) .Values.serviceAccount.name }}
{{- end }}

{{/*
OpenStack credentials Secret name.
*/}}
{{- define "cortex-scheduling-quality-exporter.osSecretName" -}}
{{- include "cortex-scheduling-quality-exporter.fullname" . }}-openstack
{{- end }}

{{/*
Returns true if Nova integration is enabled (all required openstack fields are set).
*/}}
{{- define "cortex-scheduling-quality-exporter.novaEnabled" -}}
{{- if and .Values.openstack.authURL .Values.openstack.username .Values.openstack.password .Values.openstack.projectName -}}
true
{{- end }}
{{- end }}
