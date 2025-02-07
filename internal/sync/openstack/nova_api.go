// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package openstack

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	"github.com/prometheus/client_golang/prometheus"
)

type NovaAPI[M NovaModel, L NovaList] interface {
	List(auth KeystoneAuth) ([]M, error)
}

type novaAPI[M NovaModel, L NovaList] struct {
	conf    conf.SyncOpenStackConfig
	monitor sync.Monitor
}

func NewNovaAPI[M NovaModel, L NovaList](
	conf conf.SyncOpenStackConfig, monitor sync.Monitor,
) NovaAPI[M, L] {

	return &novaAPI[M, L]{
		conf:    conf,
		monitor: monitor,
	}
}

// List returns a list of models from the OpenStack Nova API.
// Note that this function may make multiple requests in case the returned
// data has multiple pages.
func (api *novaAPI[M, L]) List(auth KeystoneAuth) ([]M, error) {
	return api.list(auth, nil)
}

// List a novaAPI page. If nil is given, the first page is returned.
func (api *novaAPI[M, L]) list(auth KeystoneAuth, url *string) ([]M, error) {
	var model M
	var list L

	if api.monitor.PipelineRequestTimer != nil {
		hist := api.monitor.PipelineRequestTimer.WithLabelValues(model.GetName())
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}

	var pageURL = api.conf.NovaURL + "/" + list.GetURL()
	if url != nil {
		pageURL = *url
	}
	slog.Info("getting openstack list data", "pageURL", pageURL)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, pageURL, http.NoBody)
	if err != nil {
		slog.Error("failed to create request", "error", err)
		return nil, err
	}
	req.Header.Set("X-Auth-Token", auth.token)
	client, err := sync.NewHTTPClient(api.conf.SSO)
	if err != nil {
		slog.Error("failed to create HTTP client", "error", err)
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		slog.Error("failed to send request", "error", err)
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		slog.Error("unexpected status code", "status", resp.StatusCode)
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	err = json.NewDecoder(resp.Body).Decode(&list)
	if err != nil {
		slog.Error("failed to decode response", "error", err)
		return nil, err
	}

	var results = list.GetModels().([]M)
	// If we got a paginated response, follow the next link.
	links := list.GetLinks()
	if links != nil {
		for _, link := range *links {
			if link.Rel == "next" {
				newList, err := api.list(auth, &link.Href)
				if err != nil {
					return nil, err
				}
				results = append(results, newList...)
			}
		}
	}

	if api.monitor.PipelineRequestProcessedCounter != nil {
		api.monitor.PipelineRequestProcessedCounter.WithLabelValues(model.GetName()).Inc()
	}
	slog.Info("got openstack list data", "pageURL", pageURL, "count", len(results))
	return results, nil
}
