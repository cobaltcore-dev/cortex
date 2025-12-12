// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package limes

import (
	"context"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/openstack/identity"
	"github.com/cobaltcore-dev/cortex/pkg/keystone"
	"github.com/cobaltcore-dev/cortex/pkg/openstack"
	"github.com/prometheus/client_golang/prometheus"
)

type LimesAPI interface {
	// Init the limes API.
	Init(ctx context.Context) error
	// Fetch all commitments for the given projects.
	GetAllCommitments(ctx context.Context, projects []identity.Project) ([]Commitment, error)
}

// API for OpenStack
type limesAPI struct {
	// Monitor to track the api.
	mon datasources.Monitor
	// Keystone api to authenticate against.
	keystoneAPI keystone.KeystoneAPI
	// Limes configuration.
	conf v1alpha1.LimesDatasource
	// Authenticated OpenStack service client to fetch the data.
	client *openstack.OpenstackClient
	// Sleep interval to avoid overloading the API.
	sleepInterval time.Duration
}

// Create a new OpenStack limes api.
func NewLimesAPI(mon datasources.Monitor, k keystone.KeystoneAPI, conf v1alpha1.LimesDatasource) LimesAPI {
	return &limesAPI{mon: mon, keystoneAPI: k, conf: conf, sleepInterval: 50 * time.Millisecond}
}

// Init the limes API.
func (api *limesAPI) Init(ctx context.Context) error {
	client, err := openstack.LimesClient(ctx, api.keystoneAPI)
	if err != nil {
		return err
	}
	api.client = client
	return nil
}

// Resolve the commitments for the given projects.
// This function fetches the commitments for each project in parallel.
func (api *limesAPI) GetAllCommitments(ctx context.Context, projects []identity.Project) ([]Commitment, error) {
	label := Commitment{}.TableName()
	slog.Info("fetching limes data", "label", label)
	if api.mon.RequestTimer != nil {
		hist := api.mon.RequestTimer.WithLabelValues(label)
		timer := prometheus.NewTimer(hist)
		defer timer.ObserveDuration()
	}

	resultMutex := sync.Mutex{}
	results := []Commitment{}
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Channel to communicate errors from goroutines.
	errChan := make(chan error, len(projects))

	for _, project := range projects {
		wg.Go(func() {
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
		})
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
	var commitments []Commitment

	path := "v1" +
		"/domains/" + project.DomainID +
		"/projects/" + project.ID +
		"/commitments"

	if err := api.client.List(ctx, path, url.Values{}, "commitments", &commitments); err != nil {
		return nil, err
	}
	// Add the project information to each commitment.
	for i := range commitments {
		commitments[i].ProjectID = project.ID
		commitments[i].DomainID = project.DomainID
	}
	return commitments, nil
}
