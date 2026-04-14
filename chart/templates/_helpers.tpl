{{- define "k8s-aws-oidc-chart.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "k8s-aws-oidc-chart.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := include "k8s-aws-oidc-chart.name" . -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "k8s-aws-oidc-chart.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "k8s-aws-oidc-chart.selectorLabels" -}}
app.kubernetes.io/name: {{ include "k8s-aws-oidc-chart.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "k8s-aws-oidc-chart.labels" -}}
helm.sh/chart: {{ include "k8s-aws-oidc-chart.chart" . }}
{{ include "k8s-aws-oidc-chart.selectorLabels" . }}
app.kubernetes.io/component: oidc-proxy
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
{{- end -}}

{{- define "k8s-aws-oidc-chart.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "k8s-aws-oidc-chart.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- required "serviceAccount.name must be set when serviceAccount.create=false" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "k8s-aws-oidc-chart.stateSecretName" -}}
{{- default (printf "%s-state" (include "k8s-aws-oidc-chart.fullname" .)) .Values.tailscale.stateSecret.name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "k8s-aws-oidc-chart.defaultImageDigest" -}}
{{- $annotations := .Chart.Annotations | default dict -}}
{{- index $annotations "io.github.meigma/default-image-digest" | default "" -}}
{{- end -}}

{{- define "k8s-aws-oidc-chart.image" -}}
{{- if .Values.image.digest -}}
{{- printf "%s@%s" .Values.image.repository .Values.image.digest -}}
{{- else if .Values.image.tag -}}
{{- printf "%s:%s" .Values.image.repository .Values.image.tag -}}
{{- else if include "k8s-aws-oidc-chart.defaultImageDigest" . -}}
{{- printf "%s@%s" .Values.image.repository (include "k8s-aws-oidc-chart.defaultImageDigest" .) -}}
{{- else -}}
{{- printf "%s:%s" .Values.image.repository .Chart.AppVersion -}}
{{- end -}}
{{- end -}}
