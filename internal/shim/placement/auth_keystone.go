// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// keystoneIntrospector validates tokens against a real Keystone service
// by calling GET /v3/auth/tokens with the token as both X-Auth-Token
// (caller identity) and X-Subject-Token (token to validate). This
// self-validation pattern avoids the need for separate service
// credentials.
type keystoneIntrospector struct {
	keystoneURL string
	httpClient  *http.Client
}

// keystoneTokenResponse mirrors the relevant subset of the Keystone
// GET /v3/auth/tokens JSON response.
type keystoneTokenResponse struct {
	Token struct {
		ExpiresAt string `json:"expires_at"`
		Roles     []struct {
			Name string `json:"name"`
		} `json:"roles"`
		Project *struct {
			ID string `json:"id"`
		} `json:"project"`
	} `json:"token"`
}

func (k *keystoneIntrospector) introspect(ctx context.Context, tokenValue string) (*tokenInfo, error) {
	url := strings.TrimSuffix(k.keystoneURL, "/") + "/v3/auth/tokens"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody) //nolint:gosec // URL from trusted config
	if err != nil {
		return nil, fmt.Errorf("creating keystone request: %w", err)
	}
	req.Header.Set("X-Auth-Token", tokenValue)
	req.Header.Set("X-Subject-Token", tokenValue)

	resp, err := k.httpClient.Do(req) //nolint:gosec // intentional call to configured Keystone
	if err != nil {
		return nil, fmt.Errorf("keystone request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("keystone returned status %d", resp.StatusCode)
	}

	var body keystoneTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decoding keystone response: %w", err)
	}

	expiresAt, err := time.Parse(time.RFC3339, body.Token.ExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("parsing token expiry: %w", err)
	}

	roleNames := make([]string, len(body.Token.Roles))
	for i, r := range body.Token.Roles {
		roleNames[i] = r.Name
	}

	projectID := ""
	if body.Token.Project != nil {
		projectID = body.Token.Project.ID
	}

	return &tokenInfo{
		roles:     roleNames,
		projectID: projectID,
		expiresAt: expiresAt,
		cachedAt:  time.Now(),
	}, nil
}
