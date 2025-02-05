// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package sim

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
)

// Simulate a request error by sending a malformed payload to the scheduler.
func SimulateRequestError() {
	request := struct {
		HereBeDragons string `json:"here_be_dragons"`
	}{
		HereBeDragons: "123",
	}

	url := "http://localhost:8080/scheduler/nova/external"
	slog.Info("sending POST request", "url", url)
	requestBody, err := json.Marshal(request)
	if err != nil {
		slog.Error("failed to marshal request", "error", err)
		return
	}
	ctx := context.Background()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(requestBody))
	if err != nil {
		slog.Error("failed to create request", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Error("failed to send POST request", "error", err)
		return
	}
	defer resp.Body.Close()

	// Print out response json (without unmarshalling it)
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		slog.Error("failed to read response", "error", err)
		return
	}
	slog.Info("received response", "status", resp.Status, "body", buf.String())
}
