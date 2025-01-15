// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import "github.com/cobaltcore-dev/cortex/internal/env"

type OpenStackConfig interface {
	GetOSAuthURL() string
	GetOSUsername() string
	GetOSPassword() string
	GetOSProjectName() string
	GetOSUserDomainName() string
	GetOSProjectDomainName() string
}

type openStackConfig struct {
	OSAuthURL           string // URL to the OpenStack Keystone authentication endpoint.
	OSUsername          string
	OSPassword          string
	OSProjectName       string
	OSUserDomainName    string
	OSProjectDomainName string
}

func NewOpenStackConfig() OpenStackConfig {
	return &openStackConfig{
		OSAuthURL:           env.ForceGetenv("OS_AUTH_URL"),
		OSUsername:          env.ForceGetenv("OS_USERNAME"),
		OSPassword:          env.ForceGetenv("OS_PASSWORD"),
		OSProjectName:       env.ForceGetenv("OS_PROJECT_NAME"),
		OSUserDomainName:    env.ForceGetenv("OS_USER_DOMAIN_NAME"),
		OSProjectDomainName: env.ForceGetenv("OS_PROJECT_DOMAIN_NAME"),
	}
}

func (c *openStackConfig) GetOSAuthURL() string           { return c.OSAuthURL }
func (c *openStackConfig) GetOSUsername() string          { return c.OSUsername }
func (c *openStackConfig) GetOSPassword() string          { return c.OSPassword }
func (c *openStackConfig) GetOSProjectName() string       { return c.OSProjectName }
func (c *openStackConfig) GetOSUserDomainName() string    { return c.OSUserDomainName }
func (c *openStackConfig) GetOSProjectDomainName() string { return c.OSProjectDomainName }
