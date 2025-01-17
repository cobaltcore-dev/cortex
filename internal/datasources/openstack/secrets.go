// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import "github.com/cobaltcore-dev/cortex/internal/env"

type OpenStackSecrets interface {
	GetOSAuthURL() string
	GetOSUsername() string
	GetOSPassword() string
	GetOSProjectName() string
	GetOSUserDomainName() string
	GetOSProjectDomainName() string
}

type openStackSecrets struct {
	OSAuthURL           string // URL to the OpenStack Keystone authentication endpoint.
	OSUsername          string
	OSPassword          string
	OSProjectName       string
	OSUserDomainName    string
	OSProjectDomainName string
}

func NewOpenStackSecrets() OpenStackSecrets {
	return &openStackSecrets{
		OSAuthURL:           env.ForceGetenv("OS_AUTH_URL"),
		OSUsername:          env.ForceGetenv("OS_USERNAME"),
		OSPassword:          env.ForceGetenv("OS_PASSWORD"),
		OSProjectName:       env.ForceGetenv("OS_PROJECT_NAME"),
		OSUserDomainName:    env.ForceGetenv("OS_USER_DOMAIN_NAME"),
		OSProjectDomainName: env.ForceGetenv("OS_PROJECT_DOMAIN_NAME"),
	}
}

func (c *openStackSecrets) GetOSAuthURL() string           { return c.OSAuthURL }
func (c *openStackSecrets) GetOSUsername() string          { return c.OSUsername }
func (c *openStackSecrets) GetOSPassword() string          { return c.OSPassword }
func (c *openStackSecrets) GetOSProjectName() string       { return c.OSProjectName }
func (c *openStackSecrets) GetOSUserDomainName() string    { return c.OSUserDomainName }
func (c *openStackSecrets) GetOSProjectDomainName() string { return c.OSProjectDomainName }
