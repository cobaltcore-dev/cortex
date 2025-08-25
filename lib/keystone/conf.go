// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package keystone

import "github.com/cobaltcore-dev/cortex/lib/sso"

// Configuration for the keystone authentication.
type Config struct {
	// The URL of the keystone service.
	URL string `json:"url"`
	// The SSO certificate to use. If none is given, we won't
	// use SSO to connect to the openstack services.
	SSO sso.Config `json:"sso,omitempty"`
	// The OpenStack username (OS_USERNAME in openstack cli).
	OSUsername string `json:"username"`
	// The OpenStack password (OS_PASSWORD in openstack cli).
	OSPassword string `json:"password"`
	// The OpenStack project name (OS_PROJECT_NAME in openstack cli).
	OSProjectName string `json:"projectName"`
	// The OpenStack user domain name (OS_USER_DOMAIN_NAME in openstack cli).
	OSUserDomainName string `json:"userDomainName"`
	// The OpenStack project domain name (OS_PROJECT_DOMAIN_NAME in openstack cli).
	OSProjectDomainName string `json:"projectDomainName"`
}
