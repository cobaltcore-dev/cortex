// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package features

import (
	"io"
	"os"

	"gopkg.in/yaml.v2"
)

type FeaturesConfig interface {
	GetExtractors() []ExtractorConfig
}

type ExtractorConfig struct {
	Name string `yaml:"name"`
}

type featuresConfig struct {
	Features struct {
		Extractors []ExtractorConfig `yaml:"extractors"`
	} `yaml:"features"`
}

func NewFeaturesConfig() FeaturesConfig {
	file, err := os.Open("/etc/config/conf.yaml")
	if err != nil {
		panic(err)
	}
	defer file.Close()
	bytes, err := io.ReadAll(file)
	if err != nil {
		panic(err)
	}
	var config featuresConfig
	if err := yaml.Unmarshal(bytes, &config); err != nil {
		panic(err)
	}
	return &config
}

func (c *featuresConfig) GetExtractors() []ExtractorConfig {
	return c.Features.Extractors
}
