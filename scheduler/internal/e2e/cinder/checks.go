// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package cinder

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	api "github.com/cobaltcore-dev/cortex/scheduler/api/delegation/cinder"
	"github.com/cobaltcore-dev/cortex/scheduler/internal/conf"
	"github.com/sapcc/go-bits/must"
)

// Run all checks.
func RunChecks(ctx context.Context, config conf.Config) {
	checkCinderSchedulerReturnsValidHosts(ctx, config)
}

// Check that the Cinder external scheduler returns a valid set of share hosts.
func checkCinderSchedulerReturnsValidHosts(ctx context.Context, config conf.Config) {
	// TODO ADD THIS CHECK

	//

	request := api.ExternalSchedulerRequest{
		Hosts:   []api.ExternalSchedulerHost{},
		Weights: map[string]float64{},
	}
	port := strconv.Itoa(config.APIConfig.Port)
	apiURL := "http://cortex-cinder-scheduler:" + port + "/scheduler/cinder/external"
	slog.Info("sending request to external scheduler", "apiURL", apiURL)

	requestBody := must.Return(json.Marshal(request))
	buf := bytes.NewBuffer(requestBody)
	req := must.Return(http.NewRequestWithContext(ctx, http.MethodPost, apiURL, buf))
	req.Header.Set("Content-Type", "application/json")
	//nolint:bodyclose // We don't care about the body here.
	respRaw := must.Return(http.DefaultClient.Do(req))
	defer respRaw.Body.Close()
	if respRaw.StatusCode != http.StatusOK {
		// Log the response body for debugging
		bodyBytes := must.Return(io.ReadAll(respRaw.Body))
		slog.Error("external scheduler API returned non-200 status code",
			"statusCode", respRaw.StatusCode,
			"responseBody", string(bodyBytes),
		)
		panic("external scheduler API returned non-200 status code")
	}
	var resp api.ExternalSchedulerResponse
	must.Succeed(json.NewDecoder(respRaw.Body).Decode(&resp))
	if len(resp.Hosts) == 0 {
		panic("no share hosts found in response")
	}
	slog.Info("check successful, got share hosts", "count", len(resp.Hosts))
}
