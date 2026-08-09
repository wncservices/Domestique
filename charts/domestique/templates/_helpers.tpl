{{- define "domestique.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "domestique.fullname" -}}
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

{{- define "domestique.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "domestique.labels" -}}
helm.sh/chart: {{ include "domestique.chart" . }}
{{ include "domestique.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "domestique.selectorLabels" -}}
app.kubernetes.io/name: {{ include "domestique.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "domestique.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "domestique.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
Which Secret holds the PostgreSQL URL.

CloudNativePG publishes <cluster>-app when it creates a cluster: a Secret with
a ready-made connection string that already points at the read-write service
and follows a failover. Reading it directly means there is no copy of the
password anywhere, and nothing to update when the operator rotates it.

Failing here rather than defaulting to SQLite is deliberate. A chart that
quietly fell back would hand you a working pod holding the only copy of your
library on a disk nothing backs up, and you would find out later.
*/}}
{{- define "domestique.databaseSecret" -}}
{{- if .Values.postgresql.existingSecret -}}
{{- .Values.postgresql.existingSecret -}}
{{- else if .Values.postgresql.cluster -}}
{{- printf "%s-app" .Values.postgresql.cluster -}}
{{- else -}}
{{- fail "domestique needs PostgreSQL: set postgresql.cluster to a CloudNativePG cluster in this namespace, or postgresql.existingSecret to a Secret holding a connection URL" -}}
{{- end -}}
{{- end -}}

{{- define "domestique.databaseSecretKey" -}}
{{- if .Values.postgresql.existingSecret -}}
{{- .Values.postgresql.secretKey -}}
{{- else -}}
{{- /* CloudNativePG names it `uri`. */ -}}
uri
{{- end -}}
{{- end -}}
