// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"io"
	"os"

	"gopkg.in/yaml.v2"
)

type OpenStackConfig interface {
	GetHypervisorsEnabled() bool
	GetServersEnabled() bool
}

type openStackConfig struct {
	Sync struct {
		OpenStack struct {
			GetHypervisorsEnabled bool `yaml:"hypervisors"`
			GetServersEnabled     bool `yaml:"servers"`
		} `yaml:"openstack"`
	} `yaml:"sync"`
}

func NewOpenStackConfig() OpenStackConfig {
	file, err := os.Open("/etc/config/conf.yaml")
	if err != nil {
		panic(err)
	}
	defer file.Close()
	bytes, err := io.ReadAll(file)
	if err != nil {
		panic(err)
	}
	var config openStackConfig
	if err := yaml.Unmarshal(bytes, &config); err != nil {
		panic(err)
	}
	return &config
}

func (c *openStackConfig) GetHypervisorsEnabled() bool {
	return c.Sync.OpenStack.GetHypervisorsEnabled
}

func (c *openStackConfig) GetServersEnabled() bool {
	return c.Sync.OpenStack.GetServersEnabled
}
