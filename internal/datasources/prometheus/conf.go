package prometheus

import "github.com/cobaltcore-dev/cortex/internal/env"

type PrometheusConfig interface {
	GetPrometheusURL() string
}

type prometheusConfig struct {
	PrometheusURL string
}

func NewPrometheusConfig() PrometheusConfig {
	return &prometheusConfig{
		PrometheusURL: env.ForceGetenv("PROMETHEUS_URL"),
	}
}

func (c *prometheusConfig) GetPrometheusURL() string { return c.PrometheusURL }
