// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package collector

import (
	"context"
	"log/slog"
	"sync"
	"time"

	hvv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cobaltcore-dev/cortex/internal/exporters/schedulingquality/nova"
)

type Options struct {
	ScrapeInterval time.Duration
	NovaClient     *nova.Client
	K8sClient      client.Client
	MinCPU         int64  // fallback: minimum placeable CPU cores
	MinMemoryBytes int64  // fallback: minimum placeable memory bytes
}

type Collector struct {
	opts Options

	mu          sync.Mutex
	lastScrape  time.Time
	cachedMin   minPlaceableUnit
	flavorsDone bool
}

func New(opts Options) *Collector {
	return &Collector{
		opts: opts,
		cachedMin: minPlaceableUnit{
			CPU:    float64(opts.MinCPU),
			Memory: float64(opts.MinMemoryBytes),
		},
	}
}

const (
	ns = "cortex"

	labelHypervisor    = "hypervisor"
	labelResource      = "resource"
	labelZone          = "zone"
	labelBuildingBlock = "building_block"
	labelGroup         = "group"
	labelCellID        = "cell_id"
	labelMaintenance   = "maintenance"
)

var (
	descCapacity = prometheus.NewDesc(
		prometheus.BuildFQName(ns, "hypervisor", "capacity"),
		"Raw physical capacity of a hypervisor by resource dimension.",
		[]string{labelHypervisor, labelResource, labelZone, labelBuildingBlock, labelGroup}, nil,
	)
	descEffectiveCapacity = prometheus.NewDesc(
		prometheus.BuildFQName(ns, "hypervisor", "effective_capacity"),
		"Effective capacity of a hypervisor after overcommit ratios.",
		[]string{labelHypervisor, labelResource, labelZone, labelBuildingBlock, labelGroup}, nil,
	)
	descAllocation = prometheus.NewDesc(
		prometheus.BuildFQName(ns, "hypervisor", "allocation"),
		"Current resource allocation on a hypervisor.",
		[]string{labelHypervisor, labelResource, labelZone, labelBuildingBlock, labelGroup}, nil,
	)
	descFree = prometheus.NewDesc(
		prometheus.BuildFQName(ns, "hypervisor", "free"),
		"Free resources on a hypervisor (effective_capacity - allocation).",
		[]string{labelHypervisor, labelResource, labelZone, labelBuildingBlock, labelGroup}, nil,
	)
	descUtilization = prometheus.NewDesc(
		prometheus.BuildFQName(ns, "hypervisor", "utilization_ratio"),
		"Resource utilization ratio (allocation / effective_capacity).",
		[]string{labelHypervisor, labelResource, labelZone, labelBuildingBlock, labelGroup}, nil,
	)
	descBalanceScore = prometheus.NewDesc(
		prometheus.BuildFQName(ns, "hypervisor", "balance_score"),
		"Balanced allocation score (1 - stddev of per-dimension utilization fractions). "+
			"1.0 = perfectly balanced; 0.0 = maximally imbalanced. "+
			"Based on the Kubernetes NodeResourcesBalancedAllocation scoring plugin.",
		[]string{labelHypervisor, labelZone, labelBuildingBlock, labelGroup}, nil,
	)
	descStranded = prometheus.NewDesc(
		prometheus.BuildFQName(ns, "hypervisor", "stranded"),
		"Stranded resources on a hypervisor: free capacity that cannot be consumed because "+
			"another resource dimension is exhausted or insufficient for the minimum placeable unit. "+
			"Borg (EuroSys 2015) defines stranded resources as resources on a machine which cannot be "+
			"used because other resources on the same machine are depleted.",
		[]string{labelHypervisor, labelResource, labelZone, labelBuildingBlock, labelGroup}, nil,
	)
	descInstances = prometheus.NewDesc(
		prometheus.BuildFQName(ns, "hypervisor", "instances"),
		"Number of VM instances on a hypervisor.",
		[]string{labelHypervisor, labelZone, labelBuildingBlock, labelGroup}, nil,
	)
	descMaintenanceInfo = prometheus.NewDesc(
		prometheus.BuildFQName(ns, "hypervisor", "maintenance"),
		"1 if the hypervisor is in maintenance mode; 0 otherwise.",
		[]string{labelHypervisor, labelZone, labelBuildingBlock, labelGroup, labelMaintenance}, nil,
	)

	// --- Cluster-wide metric descriptors ---

	descClusterCapacity = prometheus.NewDesc(
		prometheus.BuildFQName(ns, "cluster", "capacity"),
		"Total raw capacity across all hypervisors.",
		[]string{labelResource}, nil,
	)
	descClusterEffectiveCapacity = prometheus.NewDesc(
		prometheus.BuildFQName(ns, "cluster", "effective_capacity"),
		"Total effective capacity across all hypervisors.",
		[]string{labelResource}, nil,
	)
	descClusterAllocation = prometheus.NewDesc(
		prometheus.BuildFQName(ns, "cluster", "allocation"),
		"Total allocation across all hypervisors.",
		[]string{labelResource}, nil,
	)
	descClusterFree = prometheus.NewDesc(
		prometheus.BuildFQName(ns, "cluster", "free"),
		"Total free capacity across all hypervisors.",
		[]string{labelResource}, nil,
	)
	descClusterStranded = prometheus.NewDesc(
		prometheus.BuildFQName(ns, "cluster", "stranded"),
		"Total stranded resources across all hypervisors.",
		[]string{labelResource}, nil,
	)
	descClusterFragmentationRatio = prometheus.NewDesc(
		prometheus.BuildFQName(ns, "cluster", "fragmentation_ratio"),
		"Cluster-wide fragmentation ratio (total_stranded / total_free) per resource dimension. "+
			"0.0 = no fragmentation; 1.0 = all free capacity is stranded. "+
			"Based on Tetris (SIGCOMM 2014) and FGD (ATC 2023) fragmentation analysis.",
		[]string{labelResource}, nil,
	)
	descClusterBalanceScore = prometheus.NewDesc(
		prometheus.BuildFQName(ns, "cluster", "balance_score"),
		"Cluster-wide balanced allocation score (aggregate across all hypervisors).",
		nil, nil,
	)
	descClusterHypervisorCount = prometheus.NewDesc(
		prometheus.BuildFQName(ns, "cluster", "hypervisor_count"),
		"Total number of hypervisors.",
		nil, nil,
	)
	descClusterInstanceCount = prometheus.NewDesc(
		prometheus.BuildFQName(ns, "cluster", "instance_count"),
		"Total number of VM instances across all hypervisors.",
		nil, nil,
	)
	descClusterMeanUtilization = prometheus.NewDesc(
		prometheus.BuildFQName(ns, "cluster", "mean_utilization_ratio"),
		"Mean utilization ratio across all hypervisors per resource dimension.",
		[]string{labelResource}, nil,
	)
	descClusterTetrisAlignment = prometheus.NewDesc(
		prometheus.BuildFQName(ns, "cluster", "mean_tetris_alignment"),
		"Mean Tetris alignment score across all hypervisors. "+
			"Measures how well free capacity shape matches the average workload demand shape. "+
			"Based on Tetris (SIGCOMM 2014): alignment(host) = normalized_free · normalized_demand.",
		nil, nil,
	)

	// --- Exporter health ---

	descScrapeSuccess = prometheus.NewDesc(
		prometheus.BuildFQName(ns, "exporter", "scrape_success"),
		"1 if the last scrape succeeded; 0 otherwise.",
		nil, nil,
	)
	descScrapeDuration = prometheus.NewDesc(
		prometheus.BuildFQName(ns, "exporter", "scrape_duration_seconds"),
		"Duration of the last scrape in seconds.",
		nil, nil,
	)
	descMinPlaceableCPU = prometheus.NewDesc(
		prometheus.BuildFQName(ns, "exporter", "min_placeable_cpu_cores"),
		"Minimum placeable CPU cores used for stranding computation.",
		nil, nil,
	)
	descMinPlaceableMemory = prometheus.NewDesc(
		prometheus.BuildFQName(ns, "exporter", "min_placeable_memory_bytes"),
		"Minimum placeable memory bytes used for stranding computation.",
		nil, nil,
	)
)

// Describe implements prometheus.Collector.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	// Per-hypervisor.
	ch <- descCapacity
	ch <- descEffectiveCapacity
	ch <- descAllocation
	ch <- descFree
	ch <- descUtilization
	ch <- descBalanceScore
	ch <- descStranded
	ch <- descInstances
	ch <- descMaintenanceInfo

	// Cluster-wide.
	ch <- descClusterCapacity
	ch <- descClusterEffectiveCapacity
	ch <- descClusterAllocation
	ch <- descClusterFree
	ch <- descClusterStranded
	ch <- descClusterFragmentationRatio
	ch <- descClusterBalanceScore
	ch <- descClusterHypervisorCount
	ch <- descClusterInstanceCount
	ch <- descClusterMeanUtilization
	ch <- descClusterTetrisAlignment

	// Exporter.
	ch <- descScrapeSuccess
	ch <- descScrapeDuration
	ch <- descMinPlaceableCPU
	ch <- descMinPlaceableMemory
}

// Collect implements prometheus.Collector.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Refresh minimum placeable unit from Nova flavors.
	c.refreshMinPlaceableUnit(ctx)

	// List all Hypervisor CRDs.
	hypervisors, err := listHypervisors(ctx, c.opts.K8sClient)
	if err != nil {
		slog.Error("failed to list hypervisors", "error", err)
		ch <- prometheus.MustNewConstMetric(descScrapeSuccess, prometheus.GaugeValue, 0)
		ch <- prometheus.MustNewConstMetric(descScrapeDuration, prometheus.GaugeValue, time.Since(start).Seconds())
		return
	}

	min := c.getMin()

	// Cluster-wide.
	clusterCap := map[string]float64{}
	clusterEffCap := map[string]float64{}
	clusterAlloc := map[string]float64{}
	clusterStranded := map[string]float64{}
	clusterUtilSum := map[string]float64{}

	var (
		clusterUtilCount int
		clusterBalanceSum float64
		clusterInstanceCount int
		clusterAlignmentSum float64
		clusterAlignmentCount int
	)

	demandVec := c.computeAverageDemand(hypervisors)

	for i := range hypervisors {
		hv := &hypervisors[i]
		name := hv.Name
		zone := hv.Labels["topology.kubernetes.io/zone"]
		bb := hv.Labels["kubernetes.metal.cloud.sap/bb"]
		group := hv.Labels["worker.garden.sapcloud.io/group"]

		rv := buildResourceVec(hv.Status.EffectiveCapacity, hv.Status.Allocation)

		// --- Per-resource metrics ---
		for dim := range rv.EffectiveCapacity {
			dimStr := dim

			// Raw capacity.
			if raw, ok := hv.Status.Capacity[hvv1.ResourceName(dim)]; ok {
				ch <- prometheus.MustNewConstMetric(
					descCapacity, prometheus.GaugeValue,
					quantityToFloat64(raw, dim),
					name, dimStr, zone, bb, group,
				)
			}

			// Effective capacity.
			ch <- prometheus.MustNewConstMetric(
				descEffectiveCapacity, prometheus.GaugeValue,
				rv.EffectiveCapacity[dim],
				name, dimStr, zone, bb, group,
			)

			// Allocation.
			ch <- prometheus.MustNewConstMetric(
				descAllocation, prometheus.GaugeValue,
				rv.Allocation[dim],
				name, dimStr, zone, bb, group,
			)

			// Free capa.
			free := rv.free()
			ch <- prometheus.MustNewConstMetric(
				descFree, prometheus.GaugeValue,
				free[dim],
				name, dimStr, zone, bb, group,
			)

			// Utilization ratio.
			fracs := rv.fractions()
			ch <- prometheus.MustNewConstMetric(
				descUtilization, prometheus.GaugeValue,
				fracs[dim],
				name, dimStr, zone, bb, group,
			)

			// Accumulate cluster-wide.
			clusterCap[dim] += quantityToFloat64OrZero(hv.Status.Capacity, dim)
			clusterEffCap[dim] += rv.EffectiveCapacity[dim]
			clusterAlloc[dim] += rv.Allocation[dim]
			clusterUtilSum[dim] += fracs[dim]
		}
		clusterUtilCount++

		// Balance score.
		bs := rv.balanceScore()
		ch <- prometheus.MustNewConstMetric(
			descBalanceScore, prometheus.GaugeValue, bs,
			name, zone, bb, group,
		)
		clusterBalanceSum += bs

		// Stranded resources.
		stranded := strandedResources(rv, min)
		for dim, val := range stranded {
			ch <- prometheus.MustNewConstMetric(
				descStranded, prometheus.GaugeValue, val,
				name, dim, zone, bb, group,
			)
			clusterStranded[dim] += val
		}

		// Instance count.
		ch <- prometheus.MustNewConstMetric(
			descInstances, prometheus.GaugeValue,
			float64(hv.Status.NumInstances),
			name, zone, bb, group,
		)
		clusterInstanceCount += hv.Status.NumInstances

		// Maintenance mode.
		maint := hv.Spec.Maintenance
		maintVal := 0.0
		if maint != "" {
			maintVal = 1.0
		}
		ch <- prometheus.MustNewConstMetric(
			descMaintenanceInfo, prometheus.GaugeValue, maintVal,
			name, zone, bb, group, maint,
		)

		// Tetris alignment.
		free := rv.free()
		if len(demandVec) > 0 {
			alignment := tetrisAlignmentScore(free, demandVec)
			clusterAlignmentSum += alignment
			clusterAlignmentCount++
		}
	}

	// --- Cluster-wide metrics ---
	for _, dim := range []string{"cpu", "memory"} {
		ch <- prometheus.MustNewConstMetric(descClusterCapacity, prometheus.GaugeValue, clusterCap[dim], dim)
		ch <- prometheus.MustNewConstMetric(descClusterEffectiveCapacity, prometheus.GaugeValue, clusterEffCap[dim], dim)
		ch <- prometheus.MustNewConstMetric(descClusterAllocation, prometheus.GaugeValue, clusterAlloc[dim], dim)

		totalFree := clusterEffCap[dim] - clusterAlloc[dim]
		if totalFree < 0 {
			totalFree = 0
		}
		ch <- prometheus.MustNewConstMetric(descClusterFree, prometheus.GaugeValue, totalFree, dim)
		ch <- prometheus.MustNewConstMetric(descClusterStranded, prometheus.GaugeValue, clusterStranded[dim], dim)
		ch <- prometheus.MustNewConstMetric(
			descClusterFragmentationRatio, prometheus.GaugeValue,
			fragmentationRatio(clusterStranded[dim], totalFree), dim,
		)

		if clusterUtilCount > 0 {
			ch <- prometheus.MustNewConstMetric(
				descClusterMeanUtilization, prometheus.GaugeValue,
				clusterUtilSum[dim]/float64(clusterUtilCount), dim,
			)
		}
	}

	if clusterUtilCount > 0 {
		ch <- prometheus.MustNewConstMetric(
			descClusterBalanceScore, prometheus.GaugeValue,
			clusterBalanceSum/float64(clusterUtilCount),
		)
	}
	ch <- prometheus.MustNewConstMetric(descClusterHypervisorCount, prometheus.GaugeValue, float64(len(hypervisors)))
	ch <- prometheus.MustNewConstMetric(descClusterInstanceCount, prometheus.GaugeValue, float64(clusterInstanceCount))

	if clusterAlignmentCount > 0 {
		ch <- prometheus.MustNewConstMetric(
			descClusterTetrisAlignment, prometheus.GaugeValue,
			clusterAlignmentSum/float64(clusterAlignmentCount),
		)
	}

	// Exporter health.
	ch <- prometheus.MustNewConstMetric(descScrapeSuccess, prometheus.GaugeValue, 1)
	ch <- prometheus.MustNewConstMetric(descScrapeDuration, prometheus.GaugeValue, time.Since(start).Seconds())
	ch <- prometheus.MustNewConstMetric(descMinPlaceableCPU, prometheus.GaugeValue, min.CPU)
	ch <- prometheus.MustNewConstMetric(descMinPlaceableMemory, prometheus.GaugeValue, min.Memory)
}

// refreshMinPlaceableUnit queries Nova for the smallest flavor and caches the
// result. Respects the configured scrape interval to avoid hammering Nova.
func (c *Collector) refreshMinPlaceableUnit(ctx context.Context) {
	if c.opts.NovaClient == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.flavorsDone && time.Since(c.lastScrape) < c.opts.ScrapeInterval {
		return
	}

	minCPU, minMem, err := c.opts.NovaClient.SmallestFlavor(ctx)
	if err != nil {
		slog.Warn("failed to fetch Nova flavors; using cached or fallback minimum", "error", err)
		return
	}

	c.cachedMin = minPlaceableUnit{
		CPU:    float64(minCPU),
		Memory: float64(minMem),
	}
	c.flavorsDone = true
	c.lastScrape = time.Now()
	slog.Debug("refreshed minimum placeable unit from Nova flavors",
		"min_cpu", minCPU, "min_memory_bytes", minMem)
}

func (c *Collector) getMin() minPlaceableUnit {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cachedMin
}

// computeAverageDemand computes the average per-instance resource demand across
// all hypervisors. This serves as the workload demand vector for Tetris alignment.
func (c *Collector) computeAverageDemand(hypervisors []hvv1.Hypervisor) map[string]float64 {
	totalAlloc := map[string]float64{}
	totalInstances := 0

	for i := range hypervisors {
		hv := &hypervisors[i]
		if hv.Status.NumInstances == 0 {
			continue
		}
		for rn, q := range hv.Status.Allocation {
			dim := string(rn)
			totalAlloc[dim] += quantityToFloat64(q, dim)
		}
		totalInstances += hv.Status.NumInstances
	}

	if totalInstances == 0 {
		return nil
	}

	demand := make(map[string]float64, len(totalAlloc))
	for dim, total := range totalAlloc {
		demand[dim] = total / float64(totalInstances)
	}
	return demand
}

// buildResourceVec constructs a resourceVec from Hypervisor status fields.
func buildResourceVec(
	effCap map[hvv1.ResourceName]resource.Quantity,
	alloc map[hvv1.ResourceName]resource.Quantity,
) resourceVec {
	rv := resourceVec{
		EffectiveCapacity: make(map[string]float64),
		Allocation:        make(map[string]float64),
	}

	for rn, q := range effCap {
		dim := string(rn)
		rv.EffectiveCapacity[dim] = quantityToFloat64(q, dim)
	}
	for rn, q := range alloc {
		dim := string(rn)
		rv.Allocation[dim] = quantityToFloat64(q, dim)
	}

	// Ensure all dimensions present in EffectiveCapacity also exist in Allocation.
	for dim := range rv.EffectiveCapacity {
		if _, ok := rv.Allocation[dim]; !ok {
			rv.Allocation[dim] = 0
		}
	}
	return rv
}

// quantityToFloat64OrZero looks up a dimension in a resource map and converts it.
func quantityToFloat64OrZero(m map[hvv1.ResourceName]resource.Quantity, dim string) float64 {
	q, ok := m[hvv1.ResourceName(dim)]
	if !ok {
		return 0
	}
	return quantityToFloat64(q, dim)
}
