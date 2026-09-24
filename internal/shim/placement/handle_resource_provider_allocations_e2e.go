// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"

	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// e2eTestResourceProviderAllocations is a passthrough scaffold for the
// GET /resource_providers/{uuid}/allocations endpoint. Exercising it requires a
// live resource provider; that fixture setup is left for the follow-up that
// rebuilds the endpoint logic. For now the scaffold only authenticates against
// the shim.
func e2eTestResourceProviderAllocations(ctx context.Context, _ client.Client) error {
	log := logf.FromContext(ctx)
	log.Info("Running resource_provider_allocations endpoint e2e test")
	config, err := conf.GetConfig[e2eRootConfig]()
	if err != nil {
		log.Error(err, "failed to get e2e config")
		return err
	}
	if _, err := makeE2EServiceClient(ctx, config); err != nil {
		log.Error(err, "failed to create placement service client for e2e test")
		return err
	}
	return nil
}

func init() {
	e2eTests = append(e2eTests, e2eTest{name: "resource_provider_allocations", run: e2eTestResourceProviderAllocations})
}
