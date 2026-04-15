// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"
	"fmt"
	"net/http"

	"github.com/cobaltcore-dev/cortex/pkg/conf"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// e2eTestRoot is a simple test that just sends a request to the root endpoint
// of the shim, which should return a 200 OK response with a simple message.
func e2eTestRoot(ctx context.Context) error {
	log := logf.FromContext(ctx)
	log.Info("Running root endpoint e2e test")
	config, err := conf.GetConfig[e2eConfigRoot]()
	if err != nil {
		log.Error(err, "failed to get e2e config")
		return err
	}
	resp, err := http.Get(config.E2E.SVCURL + "/")
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
	e2eTests = append(e2eTests, e2eTest{name: "root", run: e2eTestRoot})
}
