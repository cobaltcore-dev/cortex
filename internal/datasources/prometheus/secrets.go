// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package prometheus

import (
	"github.com/cobaltcore-dev/cortex/internal/env"
)

type PrometheusSecrets interface {
	GetPrometheusURL() string
}

type prometheusSecrets struct {
	PrometheusURL string
}

func NewPrometheusSecrets() PrometheusSecrets {
	return &prometheusSecrets{
		PrometheusURL: env.ForceGetenv("PROMETHEUS_URL"),
	}
}

func (c *prometheusSecrets) GetPrometheusURL() string { return c.PrometheusURL }
