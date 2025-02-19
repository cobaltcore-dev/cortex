# Copyright 2025 SAP SE
# SPDX-License-Identifier: Apache-2.0

{{- if .Values.conf }}
{{ toYaml .Values.conf }}
{{- end }}