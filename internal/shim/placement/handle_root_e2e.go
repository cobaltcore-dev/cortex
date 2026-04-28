// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"
	"fmt"
	"net/http"

	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// e2eTestGetRoot verifies basic connectivity to the placement shim.
// It sends a GET request to the root endpoint (/) and checks that the shim
// responds with a 2xx status code, confirming the service is reachable.
func e2eTestGetRoot(ctx context.Context, _ client.Client) error {
	log := logf.FromContext(ctx)
	log.Info("Running root endpoint e2e test")
	config, err := conf.GetConfig[e2eRootConfig]()
	if err != nil {
		log.Error(err, "failed to get e2e config")
		return err
	}
	sc, err := makeE2EServiceClient(ctx, config)
	if err != nil {
		log.Error(err, "failed to create placement service client for e2e test")
		return err
	}
	req, err := http.NewRequestWithContext(ctx,
		http.MethodGet, sc.Endpoint+"/", http.NoBody)
	if err != nil {
		log.Error(err, "failed to create request for placement shim")
		return err
	}
	resp, err := sc.HTTPClient.Do(req)
	if err != nil {
		log.Error(err, "failed to send request to placement shim")
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		log.Error(err, "placement shim returned an error")
		return err
	}
	log.Info("Placement shim root endpoint is healthy")
	return nil
}

func init() {
	e2eTests = append(e2eTests, e2eTest{name: "root", run: e2eWrapWithModes(e2eTestGetRoot)})
}
