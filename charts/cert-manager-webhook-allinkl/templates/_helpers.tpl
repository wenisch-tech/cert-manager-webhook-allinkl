{{- define "cert-manager-webhook-allinkl.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "cert-manager-webhook-allinkl.fullname" -}}
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

{{- define "cert-manager-webhook-allinkl.selfSignedIssuer" -}}
{{ printf "%s-selfsign" (include "cert-manager-webhook-allinkl.fullname" .) }}
{{- end -}}

{{- define "cert-manager-webhook-allinkl.rootCAIssuer" -}}
{{ printf "%s-ca" (include "cert-manager-webhook-allinkl.fullname" .) }}
{{- end -}}

{{- define "cert-manager-webhook-allinkl.rootCACertificate" -}}
{{ printf "%s-ca" (include "cert-manager-webhook-allinkl.fullname" .) }}
{{- end -}}

{{- define "cert-manager-webhook-allinkl.servingCertificate" -}}
{{ printf "%s-serving" (include "cert-manager-webhook-allinkl.fullname" .) }}
{{- end -}}

{{- define "cert-manager-webhook-allinkl.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "cert-manager-webhook-allinkl.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "cert-manager-webhook-allinkl.labels" -}}
app.kubernetes.io/name: {{ include "cert-manager-webhook-allinkl.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
{{- end -}}

{{- define "cert-manager-webhook-allinkl.selectorLabels" -}}
app.kubernetes.io/name: {{ include "cert-manager-webhook-allinkl.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}
