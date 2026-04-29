// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// e2eTestUsages tests the /usages endpoint.
//
//  1. GET /projects from keystone — obtain a list of real project IDs.
//  2. GET /usages?project_id={id} — for up to 5 projects, request total
//     resource usages and verify each returns a successful response.
//
// This test is read-only and does not create any resources.
func e2eTestUsages(ctx context.Context, _ client.Client) error {
	log := logf.FromContext(ctx)
	log.Info("Running usages endpoint e2e test")
	config, err := conf.GetConfig[e2eRootConfig]()
	if err != nil {
		log.Error(err, "failed to get e2e config")
		return err
	}
	log.Info("Creating openstack client for usages e2e test")
	sc, err := makeE2EServiceClient(ctx, config)
	if err != nil {
		log.Error(err, "failed to create placement service client for e2e test")
		return err
	}
	log.Info("Successfully created openstack client for usages e2e test")

	const apiVersion = "placement 1.9"

	// Probe: for non-passthrough modes, verify endpoint returns 501.
	unimplemented, err := e2eProbeUnimplemented(ctx, sc, sc.Endpoint+"/usages?project_id=test")
	if err != nil {
		return fmt.Errorf("probe: %w", err)
	}
	if unimplemented {
		return nil
	}

	// Get the list of projects from the identity service, so that we can test
	// the /usages endpoint with a valid project id.
	log.Info("Getting list of projects from identity service for usages e2e test")
	req, err := http.NewRequestWithContext(ctx,
		http.MethodGet, sc.IdentityEndpoint+"/projects", http.NoBody)
	if err != nil {
		log.Error(err, "failed to create request for identity service projects endpoint")
		return err
	}
	req.Header.Set("X-Auth-Token", sc.TokenID)
	req.Header.Set("Accept", "application/json")
	resp, err := sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send request to identity service projects endpoint")
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "identity service projects endpoint returned an error")
		return err
	}
	var list struct {
		Projects []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"projects"`
	}
	err = json.NewDecoder(resp.Body).Decode(&list)
	if err != nil {
		log.Error(err, "failed to decode response from identity service projects endpoint")
		return err
	}
	log.Info("Successfully retrieved projects from identity service",
		"projects", len(list.Projects))

	// Pick 5 projects and test the /usages endpoint with them.
	for i, project := range list.Projects {
		if i >= 5 {
			break
		}
		log.Info("Testing /usages endpoint with project",
			"projectID", project.ID, "projectName", project.Name)
		projReq, err := http.NewRequestWithContext(ctx,
			http.MethodGet, sc.Endpoint+"/usages?project_id="+project.ID, http.NoBody)
		if err != nil {
			log.Error(err, "failed to create request for /usages endpoint")
			return err
		}
		projReq.Header.Set("X-Auth-Token", sc.TokenID)
		projReq.Header.Set("OpenStack-API-Version", apiVersion)
		projReq.Header.Set("Accept", "application/json")
		projResp, err := sc.HTTPClient.Do(projReq)
		if err != nil {
			log.Error(err, "failed to send request to placement shim /usages endpoint",
				"projectID", project.ID, "projectName", project.Name)
			return err
		}
		if projResp.StatusCode < 200 || projResp.StatusCode >= 300 {
			projResp.Body.Close()
			err := fmt.Errorf("unexpected status code: %d", projResp.StatusCode)
			log.Error(err, "placement shim /usages endpoint returned an error",
				"projectID", project.ID, "projectName", project.Name)
			return err
		}
		projResp.Body.Close()
		log.Info("Successfully retrieved usages from placement shim for project",
			"projectID", project.ID, "projectName", project.Name)
	}

	return nil
}

func init() {
	e2eTests = append(e2eTests, e2eTest{name: "usages", run: e2eWrapWithModes(e2eTestUsages)})
}
