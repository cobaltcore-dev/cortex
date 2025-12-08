// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package shared

import (
	"context"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/shared"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/db"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Advanced statistics about openstack migrations.
type VMMigrationStatisticsKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[struct{}] // No options passed through yaml config

	// Time a VM has been on a host before migration.
	timeUntilMigrationDesc *prometheus.Desc
}

func (VMMigrationStatisticsKPI) GetName() string {
	return "vm_migration_statistics_kpi"
}

func (k *VMMigrationStatisticsKPI) Init(db *db.DB, client client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, client, opts); err != nil {
		return err
	}
	k.timeUntilMigrationDesc = prometheus.NewDesc(
		"cortex_vm_time_until_migration",
		"Time a VM has been on a host before migration",
		[]string{"type", "flavor_name"},
		nil,
	)
	return nil
}

func (k *VMMigrationStatisticsKPI) Describe(ch chan<- *prometheus.Desc) {
	ch <- k.timeUntilMigrationDesc
}

func (k *VMMigrationStatisticsKPI) Collect(ch chan<- prometheus.Metric) {
	knowledge := &v1alpha1.Knowledge{}
	if err := k.Client.Get(
		context.Background(),
		client.ObjectKey{Name: "vm-host-residency"},
		knowledge,
	); err != nil {
		slog.Error("failed to get knowledge vm-host-residency", "err", err)
		return
	}
	buckets, err := v1alpha1.
		UnboxFeatureList[shared.VMHostResidencyHistogramBucket](knowledge.Status.Raw)
	if err != nil {
		slog.Error("failed to unbox vm host residency", "err", err)
		return
	}
	bucketsByFlavor := make(map[string][]shared.VMHostResidencyHistogramBucket)
	for _, bucket := range buckets {
		if bucket.FlavorName == "" {
			slog.Warn("vm_host_residency: empty flavor name in bucket", "bucket", bucket)
			continue
		}
		bucketsByFlavor[bucket.FlavorName] = append(bucketsByFlavor[bucket.FlavorName], bucket)
	}
	for flavor, buckets := range bucketsByFlavor {
		if len(buckets) == 0 {
			slog.Warn("vm_host_residency: no buckets for flavor", "flavor", flavor)
			continue
		}
		var count uint64
		var sum float64
		hist := make(map[float64]uint64, len(buckets))
		for _, bucket := range buckets {
			hist[bucket.Bucket] = bucket.Value
			count = bucket.Count // Same for all bucket objects.
			sum = bucket.Sum     // Same for all bucket objects.
		}
		ch <- prometheus.MustNewConstHistogram(
			k.timeUntilMigrationDesc,
			count, sum, hist, "unknown", flavor,
		)
	}
}
