// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"strings"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/keystone"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources/openstack"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/store"
)

// Syncer for OpenStack
type NovaSyncer struct {
	// Store to persist the nova objects in.
	Store store.Store
	// Monitor to track the syncer.
	Mon datasources.Monitor
	// Configuration for the nova syncer.
	Conf v1alpha1.NovaDatasource
	// Nova API client to fetch the data.
	API NovaAPI
}

// Init the OpenStack nova syncer.
func (s *NovaSyncer) Init(ctx context.Context) error {
	return s.API.Init(ctx)
}

// Sync the OpenStack nova objects and publish triggers.
func (s *NovaSyncer) Sync(ctx context.Context) (int64, error) {
	// Only sync the objects that are configured in the yaml conf.
	var err error
	var nResults int64
	switch s.Conf.Type {
	case v1alpha1.NovaDatasourceTypeServers:
		nResults, err = s.SyncAllServers(ctx)
	case v1alpha1.NovaDatasourceTypeDeletedServers:
		nResults, err = s.SyncDeletedServers(ctx)
	case v1alpha1.NovaDatasourceTypeHypervisors:
		nResults, err = s.SyncAllHypervisors(ctx)
	case v1alpha1.NovaDatasourceTypeFlavors:
		nResults, err = s.SyncAllFlavors(ctx)
	case v1alpha1.NovaDatasourceTypeMigrations:
		nResults, err = s.SyncAllMigrations(ctx)
	case v1alpha1.NovaDatasourceTypeAggregates:
		nResults, err = s.SyncAllAggregates(ctx)
	}
	return nResults, err
}

// isKVMFlavor returns true if the flavor name indicates a KVM-based VM.
func isKVMFlavor(flavorName string) bool {
	return strings.Contains(flavorName, "_k_")
}

// Sync all the active OpenStack servers into the database. (Includes ERROR, SHUTOFF, etc. state)
func (s *NovaSyncer) SyncAllServers(ctx context.Context) (int64, error) {
	allServers, err := s.API.GetAllServers(ctx)
	if err != nil {
		return 0, err
	}
	err = store.ReplaceAll(ctx, s.Store, "", allServers)
	if err != nil {
		return 0, err
	}
	label := Server{}.TableName()
	if s.Mon.ObjectsGauge != nil {
		gauge := s.Mon.ObjectsGauge.WithLabelValues(label)
		gauge.Set(float64(len(allServers)))
	}
	if s.Mon.RequestProcessedCounter != nil {
		counter := s.Mon.RequestProcessedCounter.WithLabelValues(label)
		counter.Inc()
	}
	return int64(len(allServers)), nil
}

// Sync all the deleted OpenStack servers into the database.
// Only fetch servers that were deleted since the last sync run.
func (s *NovaSyncer) SyncDeletedServers(ctx context.Context) (int64, error) {
	// Default time frame is the last 6 hours
	since := time.Now().Add(-6 * time.Hour)

	// If there is a configured value, use that instead.
	if s.Conf.DeletedServersChangesSinceMinutes != nil {
		since = time.Now().Add(-time.Duration(*s.Conf.DeletedServersChangesSinceMinutes) * time.Minute)
	}

	deletedServers, err := s.API.GetDeletedServers(ctx, since)
	if err != nil {
		return 0, err
	}
	err = store.ReplaceAll(ctx, s.Store, "", deletedServers)
	if err != nil {
		return 0, err
	}
	label := DeletedServer{}.TableName()
	if s.Mon.ObjectsGauge != nil {
		gauge := s.Mon.ObjectsGauge.WithLabelValues(label)
		gauge.Set(float64(len(deletedServers)))
	}
	if s.Mon.RequestProcessedCounter != nil {
		counter := s.Mon.RequestProcessedCounter.WithLabelValues(label)
		counter.Inc()
	}
	return int64(len(deletedServers)), nil
}

// Sync the OpenStack hypervisors into the database.
func (s *NovaSyncer) SyncAllHypervisors(ctx context.Context) (int64, error) {
	allHypervisors, err := s.API.GetAllHypervisors(ctx)
	if err != nil {
		return 0, err
	}
	// Since the nova api doesn't support only returning changed
	// hypervisors, we can just replace all hypervisors in the database.
	err = store.ReplaceAll(ctx, s.Store, "", allHypervisors)
	if err != nil {
		return 0, err
	}
	label := Hypervisor{}.TableName()
	if s.Mon.ObjectsGauge != nil {
		gauge := s.Mon.ObjectsGauge.WithLabelValues(label)
		gauge.Set(float64(len(allHypervisors)))
	}
	if s.Mon.RequestProcessedCounter != nil {
		counter := s.Mon.RequestProcessedCounter.WithLabelValues(label)
		counter.Inc()
	}
	return int64(len(allHypervisors)), nil
}

// Sync the OpenStack flavors into the database.
func (s *NovaSyncer) SyncAllFlavors(ctx context.Context) (int64, error) {
	allFlavors, err := s.API.GetAllFlavors(ctx)
	if err != nil {
		return 0, err
	}
	err = store.ReplaceAll(ctx, s.Store, "", allFlavors)
	if err != nil {
		return 0, err
	}
	label := Flavor{}.TableName()
	if s.Mon.ObjectsGauge != nil {
		gauge := s.Mon.ObjectsGauge.WithLabelValues(label)
		gauge.Set(float64(len(allFlavors)))
	}
	if s.Mon.RequestProcessedCounter != nil {
		counter := s.Mon.RequestProcessedCounter.WithLabelValues(label)
		counter.Inc()
	}
	return int64(len(allFlavors)), nil
}

// Sync the OpenStack migrations into the database.
func (s *NovaSyncer) SyncAllMigrations(ctx context.Context) (int64, error) {
	allMigrations, err := s.API.GetAllMigrations(ctx)
	if err != nil {
		return 0, err
	}
	err = store.ReplaceAll(ctx, s.Store, "", allMigrations)
	if err != nil {
		return 0, err
	}
	label := Migration{}.TableName()
	if s.Mon.ObjectsGauge != nil {
		gauge := s.Mon.ObjectsGauge.WithLabelValues(label)
		gauge.Set(float64(len(allMigrations)))
	}
	if s.Mon.RequestProcessedCounter != nil {
		counter := s.Mon.RequestProcessedCounter.WithLabelValues(label)
		counter.Inc()
	}
	return int64(len(allMigrations)), nil
}

// Sync the OpenStack aggregates into the database.
func (s *NovaSyncer) SyncAllAggregates(ctx context.Context) (int64, error) {
	allAggregates, err := s.API.GetAllAggregates(ctx)
	if err != nil {
		return 0, err
	}
	err = store.ReplaceAll(ctx, s.Store, "", allAggregates)
	if err != nil {
		return 0, err
	}
	label := Aggregate{}.TableName()
	if s.Mon.ObjectsGauge != nil {
		gauge := s.Mon.ObjectsGauge.WithLabelValues(label)
		gauge.Set(float64(len(allAggregates)))
	}
	if s.Mon.RequestProcessedCounter != nil {
		counter := s.Mon.RequestProcessedCounter.WithLabelValues(label)
		counter.Inc()
	}
	return int64(len(allAggregates)), nil
}

func init() {
	openstack.RegisterModels(
		v1alpha1.OpenStackDatasourceTypeNova,
		func(ds v1alpha1.OpenStackDatasource) string { return string(ds.Nova.Type) },
		map[string]store.Model{
			string(v1alpha1.NovaDatasourceTypeServers):        Server{},
			string(v1alpha1.NovaDatasourceTypeDeletedServers): DeletedServer{},
			string(v1alpha1.NovaDatasourceTypeHypervisors):    Hypervisor{},
			string(v1alpha1.NovaDatasourceTypeFlavors):        Flavor{},
			string(v1alpha1.NovaDatasourceTypeMigrations):     Migration{},
			string(v1alpha1.NovaDatasourceTypeAggregates):     Aggregate{},
		},
	)
	openstack.Index[v1alpha1.OpenStackDatasourceTypeNova] = func(
		datasource v1alpha1.Datasource,
		st store.Store,
		authenticatedKeystone keystone.KeystoneClient,
		monitor datasources.Monitor,
	) openstack.Syncer {

		return &NovaSyncer{
			Store: st,
			Mon:   monitor,
			Conf:  datasource.Spec.OpenStack.Nova,
			API:   NewNovaAPI(monitor, authenticatedKeystone, datasource.Spec.OpenStack.Nova),
		}
	}
}
