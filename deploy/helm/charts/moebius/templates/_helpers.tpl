{{/*
Common labels
*/}}
{{- define "moebius.labels" -}}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version }}
{{- end }}

{{/*
Image helper: builds full image reference from global registry + component image config.
*/}}
{{- define "moebius.image" -}}
{{ .global.imageRegistry }}/{{ .image.repository }}:{{ .image.tag | default .appVersion }}
{{- end }}
