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
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type e2eRootConfig struct {
	config
	E2E e2eConfig `json:"e2e"`
}

type e2eConfig struct {
	// SVCURL is the url placement shim service, e.g.
	// "http://cortex-placement-shim-service:8080"
	SVCURL string `json:"svcURL"`
}

// makeE2EServiceClient creates an authenticated openstack placement client
// using the keystone configuration in the e2e config. It modifies the service
// client to use the shim's svc url instead of the real placement endpoint,
// so that the e2e tests can send authenticated requests to the shim.
func makeE2EServiceClient(ctx context.Context, rc e2eRootConfig) (*gophercloud.ServiceClient, error) {
	log := logf.FromContext(ctx)
	authOpts := gophercloud.AuthOptions{
		IdentityEndpoint: rc.KeystoneURL,
		Username:         rc.OSUsername,
		DomainName:       rc.OSUserDomainName,
		Password:         rc.OSPassword,
		AllowReauth:      true,
		Scope: &gophercloud.AuthScope{
			ProjectName: rc.OSProjectName,
			DomainName:  rc.OSProjectDomainName,
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
	provider.HTTPClient.Transport = &e2eModeTransport{base: transport}
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
	run  func(ctx context.Context, cl client.Client) error
}

// e2eTests is populated by init() functions in the handle_*_e2e.go files.
var e2eTests []e2eTest

// e2eAllModes is the list of feature modes exercised by e2e tests when
// AllowModeOverride is enabled.
var e2eAllModes = []FeatureMode{
	FeatureModePassthrough,
	FeatureModeHybrid,
	FeatureModeCRD,
}

// setFeatureModeHeader sets the X-Cortex-Feature-Mode override header on the
// request so the shim dispatches to the specified mode regardless of its
// configured mode.
func setFeatureModeHeader(req *http.Request, mode FeatureMode) {
	if mode != "" {
		req.Header.Set(headerFeatureModeOverride, string(mode))
	}
}

// e2eModeContextKey is used to pass the current test mode through context.
type e2eModeContextKey struct{}

// e2eCurrentMode retrieves the feature mode from context (set by
// e2eWrapWithModes). Returns empty string if not set.
func e2eCurrentMode(ctx context.Context) FeatureMode {
	if m, ok := ctx.Value(e2eModeContextKey{}).(FeatureMode); ok {
		return m
	}
	return ""
}

// e2eWrapWithModes returns a test function that iterates over all feature
// modes. For each mode it injects the mode into context (retrievable via
// e2eCurrentMode) so that the e2eModeTransport sets the override header on
// every outgoing request.
func e2eWrapWithModes(fn func(ctx context.Context, cl client.Client) error) func(ctx context.Context, cl client.Client) error {
	return func(ctx context.Context, cl client.Client) error {
		log := logf.FromContext(ctx)
		for _, mode := range e2eAllModes {
			modeLog := log.WithName(string(mode))
			modeCtx := context.WithValue(ctx, e2eModeContextKey{}, mode)
			modeCtx = logf.IntoContext(modeCtx, modeLog)
			modeLog.Info("Starting mode")
			if err := fn(modeCtx, cl); err != nil {
				return fmt.Errorf("mode %s: %w", mode, err)
			}
			modeLog.Info("Mode passed")
		}
		return nil
	}
}

// e2eProbeUnimplemented sends a single GET request with the mode override
// header to verify the endpoint returns 501 Not Implemented. Returns true if
// the endpoint is unimplemented for this mode (test should skip). Returns
// false if the endpoint returned a success status (test should continue).
// Returns an error for unexpected status codes (4xx/5xx other than 501).
func e2eProbeUnimplemented(ctx context.Context, sc *gophercloud.ServiceClient, probeURL string) (bool, error) {
	log := logf.FromContext(ctx)
	mode := e2eCurrentMode(ctx)
	if mode == "" || mode == FeatureModePassthrough {
		return false, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, http.NoBody)
	if err != nil {
		return false, err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("OpenStack-API-Version", "placement 1.6")
	setFeatureModeHeader(req, mode)
	resp, err := sc.HTTPClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotImplemented {
		log.Info("Endpoint correctly returns 501 for unimplemented mode", "mode", mode)
		return true, nil
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return false, fmt.Errorf("probe %s in mode %s returned unexpected status %d", probeURL, mode, resp.StatusCode)
	}
	return false, nil
}

// e2eModeTransport wraps an http.RoundTripper to automatically inject the
// X-Cortex-Feature-Mode header based on the mode stored in the request's
// context (via e2eModeContextKey). This avoids manually calling
// setFeatureModeHeader on every request in every e2e test.
type e2eModeTransport struct {
	base http.RoundTripper
}

func (t *e2eModeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if mode := e2eCurrentMode(req.Context()); mode != "" {
		req = req.Clone(req.Context())
		req.Header.Set(headerFeatureModeOverride, string(mode))
	}
	return t.base.RoundTrip(req)
}

// RunE2E executes end-to-end tests for all placement shim handlers.
// It stops on the first failure and returns the error.
func RunE2E(ctx context.Context, cl client.Client) error {
	log := logf.FromContext(ctx)
	log.Info("Running e2e test(s)", "count", len(e2eTests))
	totalStart := time.Now()
	for i, test := range e2eTests {
		log.Info("Starting e2e test",
			"index", i+1, "total", len(e2eTests), "name", test.name)
		start := time.Now()
		testCtx := logf.IntoContext(ctx, log.WithName(test.name))
		if err := test.run(testCtx, cl); err != nil {
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
