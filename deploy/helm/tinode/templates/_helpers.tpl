{{- define "tinode.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "tinode.fullname" -}}
{{- printf "%s-%s" .Release.Name (include "tinode.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "tinode.labels" -}}
app.kubernetes.io/name: {{ include "tinode.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "tinode.selectorLabels" -}}
app.kubernetes.io/name: {{ include "tinode.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}
