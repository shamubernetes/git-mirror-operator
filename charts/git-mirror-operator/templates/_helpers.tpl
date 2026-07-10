{{/* Expand the name of the chart. */}}
{{- define "git-mirror-operator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/* Create a default fully qualified app name. */}}
{{- define "git-mirror-operator.fullname" -}}
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

{{/* Namespace for generated references. */}}
{{- define "git-mirror-operator.namespaceName" -}}
{{- .Release.Namespace }}
{{- end }}

{{/* Build a resource name while respecting Kubernetes' 63-character limit. */}}
{{- define "git-mirror-operator.resourceName" -}}
{{- $fullname := include "git-mirror-operator.fullname" .context }}
{{- $suffix := .suffix }}
{{- $maxLen := sub 62 (len $suffix) | int }}
{{- if gt (len $fullname) $maxLen }}
{{- printf "%s-%s" (trunc $maxLen $fullname | trimSuffix "-") $suffix | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" $fullname $suffix | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{/* Resolve the operator image, preferring an immutable digest when set. */}}
{{- define "git-mirror-operator.managerImage" -}}
{{- if .Values.manager.image.digest -}}
{{- printf "%s@%s" .Values.manager.image.repository .Values.manager.image.digest -}}
{{- else -}}
{{- printf "%s:%s" .Values.manager.image.repository (default .Chart.AppVersion .Values.manager.image.tag) -}}
{{- end -}}
{{- end }}

{{/* Resolve the sync Job image, preferring an immutable digest when set. */}}
{{- define "git-mirror-operator.syncImage" -}}
{{- if .Values.sync.image.digest -}}
{{- printf "%s@%s" .Values.sync.image.repository .Values.sync.image.digest -}}
{{- else -}}
{{- printf "%s:%s" .Values.sync.image.repository (default .Chart.AppVersion .Values.sync.image.tag) -}}
{{- end -}}
{{- end }}
