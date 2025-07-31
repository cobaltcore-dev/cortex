// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package limes

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	gosync "sync"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/keystone"
	"github.com/cobaltcore-dev/cortex/internal/sync"
	"github.com/cobaltcore-dev/cortex/internal/sync/openstack/identity"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/prometheus/client_golang/prometheus"
)

type LimesAPI interface {
	// Init the limes API.
	Init(ctx context.Context)
	// Fetch all commitments for the given projects.
	GetAllCommitments(ctx context.Context, projects []identity.Project) ([]Commitment, error)
}

// API for OpenStack limes.
type limesAPI struct {
	// Monitor to track the api.
	mon sync.Monitor
	// Keystone api to authenticate against.
	keystoneAPI keystone.KeystoneAPI
	// Limes configuration.
	conf LimesConf
	// Authenticated OpenStack service client to fetch the data.
	sc *gophercloud.ServiceClient
	// Sleep interval to avoid overloading the API.
	sleepInterval time.Duration
}

// Create a new OpenStack limes api.
func NewLimesAPI(mon sync.Monitor, k keystone.KeystoneAPI, conf LimesConf) LimesAPI {
	return &limesAPI{mon: mon, keystoneAPI: k, conf: conf, sleepInterval: 50 * time.Millisecond}
}

// Init the limes API.
func (api *limesAPI) Init(ctx context.Context) {
	if err := api.keystoneAPI.Authenticate(ctx); err != nil {
		panic(err)
	}
	// Automatically fetch the limes endpoint from the keystone service catalog.
	// See: https://github.com/sapcc/limes/blob/5ea068b/docs/users/api-example.md?plain=1#L23
	provider := api.keystoneAPI.Client()
	serviceType := "resources"
	url, err := api.keystoneAPI.FindEndpoint(api.conf.Availability, serviceType)
	if err != nil {
		panic(err)
	}
	slog.Info("using limes endpoint", "url", url)
	api.sc = &gophercloud.ServiceClient{
		ProviderClient: provider,
		Endpoint:       url,
		Type:           serviceType,
	}
}

// Resolve the commitments for the given projects.
// This function fetches the commitments for each project in parallel.
func (api *limesAPI) GetAllCommitments(ctx context.Context, projects []identity.Project) ([]Commitment, error) {
	label := Commitment{}.TableName()
	slog.Info("fetching limes data", "label", label)
	if api.mon.PipelineRequestTimer != nil {
		hist := api.mon.PipelineRequestTimer.WithLabelValues(label)
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}

	resultMutex := gosync.Mutex{}
	results := []Commitment{}
	var wg gosync.WaitGroup
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Channel to communicate errors from goroutines.
	errChan := make(chan error, len(projects))

	for _, project := range projects {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Fetch commitments for the project.
			newResults, err := api.getCommitments(ctx, project)
			if err != nil {
				errChan <- err
				cancel()
				return
			}
			resultMutex.Lock()
			results = append(results, newResults...)
			resultMutex.Unlock()
		}()
		time.Sleep(api.sleepInterval) // Don't overload the API.
	}

	// Wait for all goroutines to finish and close the error channel.
	go func() {
		wg.Wait()
		close(errChan)
	}()
	// Return the first error encountered, if any.
	for err := range errChan {
		if err != nil {
			slog.Error("failed to resolve commitments", "error", err)
			return nil, err
		}
	}
	return results, nil
}

// Resolve the commitments for the given project.
func (api *limesAPI) getCommitments(ctx context.Context, project identity.Project) ([]Commitment, error) {
	url := api.sc.Endpoint + "v1" +
		"/domains/" + project.DomainID +
		"/projects/" + project.ID +
		"/commitments"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Auth-Token", api.sc.Token())
	resp, err := api.sc.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
	var list struct {
		Commitments []Commitment `json:"commitments"`
	}
	err = json.NewDecoder(resp.Body).Decode(&list)
	if err != nil {
		return nil, err
	}
	// Add the project information to each commitment.
	for i := range list.Commitments {
		list.Commitments[i].ProjectID = project.ID
		list.Commitments[i].DomainID = project.DomainID
	}
	return list.Commitments, nil
}
