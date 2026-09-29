{{- /*
Copyright The Linux Foundation and each contributor to LFX.
SPDX-License-Identifier: MIT
*/ -}}

{{/*
Name for the OpenSearch index-setup ConfigMap, bounded to the Kubernetes
63-character DNS label limit. Release names up to 53 chars combined with the
"-opensearch-index-config" suffix can otherwise exceed it.
*/}}
{{- define "lfx-v2-indexer-service.indexConfigMapName" -}}
{{- printf "%s-opensearch-index-config" .Release.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Name for the OpenSearch index-setup Job, bounded the same way as the
ConfigMap above.
*/}}
{{- define "lfx-v2-indexer-service.indexJobName" -}}
{{- printf "%s-opensearch-index-setup" .Release.Name | trunc 63 | trimSuffix "-" }}
{{- end }}
