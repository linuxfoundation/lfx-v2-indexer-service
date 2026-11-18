{{- /*
Copyright The Linux Foundation and each contributor to LFX.
SPDX-License-Identifier: MIT
*/ -}}

{{/*
Name for the OpenSearch index-setup Job, bounded to the Kubernetes
63-character DNS label limit. Release names up to 53 chars combined with the
"-opensearch-index-setup" suffix can otherwise exceed it.
*/}}
{{- define "lfx-v2-indexer-service.indexJobName" -}}
{{- printf "%s-opensearch-index-setup" .Release.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/* Parse the same integer h/m/s/ms duration forms supported by the grace check.
Keep milliseconds until the final grace rounding so sub-second waits aren't lost. */}}
{{- define "lfx-v2-indexer-service.durationMillis" -}}
{{- $value := toString .value -}}
{{- if or (eq $value "") (not (regexMatch "^([0-9]+h)?([0-9]+m)?([0-9]+s)?([0-9]+ms)?$" $value)) -}}
{{- fail (printf "%s must use integer h/m/s/ms components (e.g. 1m30s or 1500ms)" .name) -}}
{{- end -}}
{{- $ms := regexFind "[0-9]+ms" $value -}}
{{- $rest := regexReplaceAll "[0-9]+ms" $value "" -}}
{{- $hours := regexFind "[0-9]+h" $rest -}}
{{- $minutes := regexFind "[0-9]+m" $rest -}}
{{- $seconds := regexFind "[0-9]+s" $rest -}}
{{- $total := 0.0 -}}
{{- if $hours }}{{- $total = addf $total (mulf ($hours | trimSuffix "h" | float64) 3600000.0) }}{{- end -}}
{{- if $minutes }}{{- $total = addf $total (mulf ($minutes | trimSuffix "m" | float64) 60000.0) }}{{- end -}}
{{- if $seconds }}{{- $total = addf $total (mulf ($seconds | trimSuffix "s" | float64) 1000.0) }}{{- end -}}
{{- if $ms }}{{- $total = addf $total ($ms | trimSuffix "ms" | float64) }}{{- end -}}
{{- if gt $total 9223372036854.0 }}{{- fail (printf "%s exceeds Go's duration range" .name) }}{{- end -}}
{{- printf "%.0f" $total -}}
{{- end -}}

{{/* Preserve pure JSON mapping fixtures while sharing provisioning settings. */}}
{{- define "lfx-v2-indexer-service.privateIndexBody" -}}
{{- $body := fromJson .mapping -}}
{{- $_ := set $body "settings" (dict "index" (dict "number_of_shards" (.job.numberOfShards | int) "number_of_replicas" (.job.numberOfReplicas | int))) -}}
{{- toJson $body -}}
{{- end -}}
