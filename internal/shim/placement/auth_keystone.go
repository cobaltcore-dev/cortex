// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"
	"fmt"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"github.com/gophercloud/gophercloud/v2/openstack/identity/v3/tokens"
)

// initTokenIntrospector authenticates the shim with Keystone using
// service credentials and creates the identity v3 client used for
// token validation. Called during Start after the HTTP client is ready.
func (s *Shim) initTokenIntrospector(ctx context.Context) error {
	if s.config.Auth == nil {
		return nil
	}
	authOpts := gophercloud.AuthOptions{
		IdentityEndpoint: s.config.KeystoneURL,
		Username:         s.config.OSUsername,
		DomainName:       s.config.OSUserDomainName,
		Password:         s.config.OSPassword,
		AllowReauth:      true,
		Scope: &gophercloud.AuthScope{
			ProjectName: s.config.OSProjectName,
			DomainName:  s.config.OSProjectDomainName,
		},
	}
	provider, err := openstack.NewClient(s.config.KeystoneURL)
	if err != nil {
		return fmt.Errorf("creating Keystone provider client: %w", err)
	}
	provider.HTTPClient = *s.httpClient
	if err := openstack.Authenticate(ctx, provider, authOpts); err != nil {
		return fmt.Errorf("authenticating with Keystone: %w", err)
	}
	identityClient, err := openstack.NewIdentityV3(provider, gophercloud.EndpointOpts{})
	if err != nil {
		return fmt.Errorf("creating identity v3 client: %w", err)
	}
	s.tokenIntrospector = &keystoneIntrospector{identityClient: identityClient}
	setupLog.Info("Auth middleware enabled with Keystone",
		"keystoneURL", s.config.KeystoneURL)
	return nil
}

// keystoneIntrospector validates tokens against Keystone using an
// authenticated service client. It calls GET /v3/auth/tokens with the
// shim's own service credentials as X-Auth-Token and the subject token
// as X-Subject-Token, then extracts roles, project, and expiry from
// the response.
type keystoneIntrospector struct {
	identityClient *gophercloud.ServiceClient
}

func (k *keystoneIntrospector) introspect(ctx context.Context, tokenValue string) (*tokenInfo, error) {
	result := tokens.Get(ctx, k.identityClient, tokenValue)
	if result.Err != nil {
		return nil, fmt.Errorf("keystone token introspection failed: %w", result.Err)
	}

	token, err := result.ExtractToken()
	if err != nil {
		return nil, fmt.Errorf("extracting token: %w", err)
	}

	roles, err := result.ExtractRoles()
	if err != nil {
		return nil, fmt.Errorf("extracting roles: %w", err)
	}

	roleNames := make([]string, len(roles))
	for i, r := range roles {
		roleNames[i] = r.Name
	}

	projectID := ""
	project, err := result.ExtractProject()
	if err != nil {
		return nil, fmt.Errorf("extracting project: %w", err)
	}
	if project != nil {
		projectID = project.ID
	}

	return &tokenInfo{
		roles:     roleNames,
		projectID: projectID,
		expiresAt: token.ExpiresAt,
		cachedAt:  time.Now(),
	}, nil
}
