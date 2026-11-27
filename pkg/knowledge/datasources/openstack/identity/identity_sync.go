// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"context"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/keystone"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/store"
)

type IdentitySyncer struct {
	// Store to persist the identity objects in.
	Store store.Store
	// Monitor to track the syncer.
	Mon datasources.Monitor
	// Configuration for the identity syncer.
	Conf v1alpha1.IdentityDatasource
	// Identity API client to fetch the data.
	API IdentityAPI
}

func (s *IdentitySyncer) Init(ctx context.Context) error {
	return s.API.Init(ctx)
}

func (s *IdentitySyncer) Sync(ctx context.Context) (int64, error) {
	// Only sync the objects that are configured in the yaml conf.
	var err error
	var nResults int64
	switch s.Conf.Type {
	case v1alpha1.IdentityDatasourceTypeDomains:
		nResults, err = s.SyncDomains(ctx)
	case v1alpha1.IdentityDatasourceTypeProjects:
		nResults, err = s.SyncProjects(ctx)
	}
	return nResults, err
}

func (s *IdentitySyncer) SyncDomains(ctx context.Context) (int64, error) {
	domains, err := s.API.GetAllDomains(ctx)
	if err != nil {
		return 0, err
	}
	if err := store.ReplaceAll(ctx, s.Store, "", domains); err != nil {
		return 0, err
	}
	label := Domain{}.TableName()
	if s.Mon.ObjectsGauge != nil {
		gauge := s.Mon.ObjectsGauge.WithLabelValues(label)
		gauge.Set(float64(len(domains)))
	}
	if s.Mon.RequestProcessedCounter != nil {
		counter := s.Mon.RequestProcessedCounter.WithLabelValues(label)
		counter.Inc()
	}
	return int64(len(domains)), nil
}

func (s *IdentitySyncer) SyncProjects(ctx context.Context) (int64, error) {
	projects, err := s.API.GetAllProjects(ctx)
	if err != nil {
		return 0, err
	}
	if err := store.ReplaceAll(ctx, s.Store, "", projects); err != nil {
		return 0, err
	}
	label := Project{}.TableName()
	if s.Mon.ObjectsGauge != nil {
		gauge := s.Mon.ObjectsGauge.WithLabelValues(label)
		gauge.Set(float64(len(projects)))
	}
	if s.Mon.RequestProcessedCounter != nil {
		counter := s.Mon.RequestProcessedCounter.WithLabelValues(label)
		counter.Inc()
	}
	return int64(len(projects)), nil
}

func init() {
	openstack.RegisterModels(
		v1alpha1.OpenStackDatasourceTypeIdentity,
		func(ds v1alpha1.OpenStackDatasource) string { return string(ds.Identity.Type) },
		map[string]store.Model{
			string(v1alpha1.IdentityDatasourceTypeProjects): Project{},
			string(v1alpha1.IdentityDatasourceTypeDomains):  Domain{},
		},
	)
	openstack.Index[v1alpha1.OpenStackDatasourceTypeIdentity] = func(
		datasource v1alpha1.Datasource,
		st store.Store,
		authenticatedKeystone keystone.KeystoneClient,
		monitor datasources.Monitor,
	) openstack.Syncer {

		return &IdentitySyncer{
			Store: st,
			Mon:   monitor,
			Conf:  datasource.Spec.OpenStack.Identity,
			API:   NewIdentityAPI(monitor, authenticatedKeystone, datasource.Spec.OpenStack.Identity),
		}
	}
}
