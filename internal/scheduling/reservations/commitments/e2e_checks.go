// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	liquid "github.com/sapcc/go-api-declarations/liquid"
	"github.com/sapcc/go-bits/must"
)

const (
	// Default URL for the commitments API endpoint.
	// This should match the service name in the helm chart.
	defaultCommitmentsAPIURL = "http://cortex-nova-scheduler:8080"
)

// E2EChecksConfig holds the configuration for CR e2e checks.
type E2EChecksConfig struct {
	// Base URL for the commitments API. If empty, defaults to defaultCommitmentsAPIURL.
	BaseURL string `json:"baseURL"`
}

// CheckCommitmentsInfoEndpoint sends a GET request to the /v1/commitments/info endpoint
// and verifies that it returns HTTP 200 with a valid ServiceInfo response.
func CheckCommitmentsInfoEndpoint(ctx context.Context, config E2EChecksConfig) {
	baseURL := config.BaseURL
	if baseURL == "" {
		baseURL = defaultCommitmentsAPIURL
	}
	apiURL := baseURL + "/v1/commitments/info"
	slog.Info("checking commitments info endpoint", "apiURL", apiURL)

	httpReq := must.Return(http.NewRequestWithContext(ctx, http.MethodGet, apiURL, http.NoBody))
	httpReq.Header.Set("Accept", "application/json")

	//nolint:bodyclose // Body is closed in the deferred function below.
	resp := must.Return(http.DefaultClient.Do(httpReq))
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes := must.Return(io.ReadAll(resp.Body))
		slog.Error("commitments info API returned non-200 status code",
			"statusCode", resp.StatusCode,
			"responseBody", string(bodyBytes),
		)
		panic(fmt.Sprintf("commitments info API returned status %d, expected 200", resp.StatusCode))
	}

	var serviceInfo liquid.ServiceInfo
	if err := json.NewDecoder(resp.Body).Decode(&serviceInfo); err != nil {
		panic(fmt.Sprintf("failed to decode ServiceInfo response: %v", err))
	}

	// Basic validation of the response
	if serviceInfo.Version < 0 {
		slog.Warn("commitments info returned version -1, knowledge may not be ready yet")
	}

	slog.Info("commitments info endpoint check passed",
		"version", serviceInfo.Version,
		"resourceCount", len(serviceInfo.Resources),
	)
}

// RunCommitmentsE2EChecks runs all e2e checks for the commitments API.
func RunCommitmentsE2EChecks(ctx context.Context, config E2EChecksConfig) {
	slog.Info("running commitments e2e checks")
	CheckCommitmentsInfoEndpoint(ctx, config)
	slog.Info("all commitments e2e checks passed")
}
