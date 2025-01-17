// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"io"
	"os"

	"gopkg.in/yaml.v2"
)

type SchedulerConfig interface {
	GetSteps() []StepConfig
}

type StepConfig struct {
	Name    string         `yaml:"name"`
	Options map[string]any `yaml:"options"`
}

type schedulerConfig struct {
	Scheduler struct {
		Steps []StepConfig `yaml:"steps"`
	} `yaml:"scheduler"`
}

func NewSchedulerConfig() SchedulerConfig {
	file, err := os.Open("/etc/config/conf.yaml")
	if err != nil {
		panic(err)
	}
	defer file.Close()
	bytes, err := io.ReadAll(file)
	if err != nil {
		panic(err)
	}
	var config schedulerConfig
	if err := yaml.Unmarshal(bytes, &config); err != nil {
		panic(err)
	}
	return &config
}

func (c *schedulerConfig) GetSteps() []StepConfig {
	return c.Scheduler.Steps
}
