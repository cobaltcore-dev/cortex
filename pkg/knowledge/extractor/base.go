// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package extractor

import (
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Common base for go extractors, providing the options configured in the
// knowledge spec and a kubernetes client.
type BaseExtractor[Opts any] struct {
	// Options to pass via yaml to this extractor.
	conf.JsonOpts[Opts]
	// Kubernetes client to access other resources.
	Client client.Client
}

// Init the extractor with the kubernetes client and options.
func (e *BaseExtractor[Opts]) Init(client client.Client, spec v1alpha1.KnowledgeSpec) error {
	rawOpts := conf.NewRawOpts(`{}`)
	if spec.Extractor != nil && len(spec.Extractor.Config.Raw) > 0 {
		rawOpts = conf.NewRawOptsBytes(spec.Extractor.Config.Raw)
	}
	if err := e.Load(rawOpts); err != nil {
		return err
	}
	e.Client = client
	return nil
}
