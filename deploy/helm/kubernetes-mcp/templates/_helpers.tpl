{{- define "kubernetes-mcp.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "kubernetes-mcp.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s" (include "kubernetes-mcp.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "kubernetes-mcp.labels" -}}
app.kubernetes.io/name: {{ include "kubernetes-mcp.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- end -}}

{{- define "kubernetes-mcp.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kubernetes-mcp.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "kubernetes-mcp.serviceAccountName" -}}
{{ include "kubernetes-mcp.fullname" . }}
{{- end -}}

{{/* Secret name that holds one remote cluster's token + ca.crt. */}}
{{- define "kubernetes-mcp.remoteSecretName" -}}
{{- printf "%s-cluster-%s" (include "kubernetes-mcp.fullname" .root) .cluster.name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
