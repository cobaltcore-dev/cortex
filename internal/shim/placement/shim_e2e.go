// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/cobaltcore-dev/cortex/pkg/sso"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type e2eRootConfig struct {
	E2E e2eConfig `yaml:"e2e"`

	// Shared with the main shim config.

	// SSO is an optional configuration for the certificates the http client
	// should use when talking to the placement API over ingress with single-sign-on.
	SSO *sso.SSOConfig `json:"sso,omitempty"`
	// KeystoneURL is the URL of the OpenStack Keystone identity service,
	// shared with the main shim config.
	KeystoneURL string `json:"keystoneURL,omitempty"`
}

type e2eConfig struct {
	// SVCURL is the url placement shim service, e.g.
	// "http://cortex-placement-shim-service:8080"
	SVCURL string `json:"svcURL"`
	// OSUsername is the OpenStack username for keystone authentication
	// (OS_USERNAME in openstack cli).
	OSUsername string `json:"osUsername"`
	// OSPassword is the OpenStack password for keystone authentication
	// (OS_PASSWORD in openstack cli).
	OSPassword string `json:"osPassword"`
	// OSProjectName is the OpenStack project name for keystone authentication
	// (OS_PROJECT_NAME in openstack cli).
	OSProjectName string `json:"osProjectName"`
	// OSUserDomainName is the OpenStack user domain name for keystone
	// authentication (OS_USER_DOMAIN_NAME in openstack cli).
	OSUserDomainName string `json:"osUserDomainName"`
	// OSProjectDomainName is the OpenStack project domain name for keystone
	// authentication (OS_PROJECT_DOMAIN_NAME in openstack cli).
	OSProjectDomainName string `json:"osProjectDomainName"`
}

// makeE2EServiceClient creates an authenticated openstack placement client
// using the keystone configuration in the e2e config. It modifies the service
// client to use the shim's svc url instead of the real placement endpoint,
// so that the e2e tests can send authenticated requests to the shim.
func makeE2EServiceClient(ctx context.Context, rc e2eRootConfig) (*gophercloud.ServiceClient, error) {
	log := logf.FromContext(ctx)
	authOpts := gophercloud.AuthOptions{
		IdentityEndpoint: rc.KeystoneURL,
		Username:         rc.E2E.OSUsername,
		DomainName:       rc.E2E.OSUserDomainName,
		Password:         rc.E2E.OSPassword,
		AllowReauth:      true,
		Scope: &gophercloud.AuthScope{
			ProjectName: rc.E2E.OSProjectName,
			DomainName:  rc.E2E.OSProjectDomainName,
		},
	}
	log.Info("Authenticating with keystone for e2e tests",
		"endpoint", authOpts.IdentityEndpoint,
		"username", authOpts.Username,
		"project", authOpts.Scope.ProjectName)
	provider, err := openstack.NewClient(rc.KeystoneURL)
	if err != nil {
		log.Error(err, "Failed to create openstack provider client")
		return nil, fmt.Errorf("failed to create openstack provider client: %w", err)
	}
	// Build the transport with optional SSO TLS credentials.
	var transport *http.Transport
	if rc.SSO != nil {
		log.Info("SSO config provided, creating transport for placement API")
		transport, err = sso.NewTransport(*rc.SSO)
		if err != nil {
			log.Error(err, "Failed to create transport from SSO config")
			return nil, err
		}
	} else {
		log.Info("No SSO config provided, using plain transport for placement API")
		transport = &http.Transport{}
	}
	provider.HTTPClient.Transport = transport
	if err := openstack.Authenticate(ctx, provider, authOpts); err != nil {
		log.Error(err, "Failed to authenticate with keystone")
		return nil, fmt.Errorf("failed to authenticate with keystone: %w", err)
	}
	log.Info("Successfully authenticated with keystone for e2e tests")
	return &gophercloud.ServiceClient{
		ProviderClient: provider,
		Endpoint:       rc.E2E.SVCURL,
		Type:           "placement",
	}, nil
}

// e2eTest is a named end-to-end test registered by handler e2e files.
type e2eTest struct {
	name string
	run  func(ctx context.Context) error
}

// e2eTests is populated by init() functions in the handle_*_e2e.go files.
var e2eTests []e2eTest

// RunE2E executes end-to-end tests for all placement shim handlers.
// It stops on the first failure and returns the error.
func RunE2E(ctx context.Context) error {
	log := logf.FromContext(ctx)
	log.Info("Running e2e test(s)", "count", len(e2eTests))
	totalStart := time.Now()
	for i, test := range e2eTests {
		log.Info("Starting e2e test",
			"index", i+1, "total", len(e2eTests), "name", test.name)
		start := time.Now()
		testCtx := logf.IntoContext(ctx, log.WithName(test.name))
		if err := test.run(testCtx); err != nil {
			log.Error(err, "FAIL e2e test",
				"index", i+1, "total", len(e2eTests), "name", test.name,
				"took_ms", time.Since(start).Milliseconds())
			return fmt.Errorf("e2e test %q failed: %w", test.name, err)
		}
		log.Info("PASS e2e test",
			"index", i+1, "total", len(e2eTests), "name", test.name,
			"took_ms", time.Since(start).Milliseconds())
	}
	log.Info("All e2e tests passed",
		"took_ms", time.Since(totalStart).Milliseconds())
	return nil
}
