// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/prometheus/client_golang/prometheus"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var vmStateKPIlogger = ctrl.Log.WithName("vm-state-kpi")

// This kpi monitors the current state of vms, i.e. how many vms are running,
// stopped, paused, etc. It also exposes additional labels such as the vm's
// hypervisor type which can be used to define alerts on non-running vms.
type VMStateKPI struct {
	// Common base for all KPIs that provides standard functionality.
	plugins.BaseKPI[struct{}] // No options passed through yaml config

	// Current state of the VM, e.g. running, stopped, paused, etc.
	vmStateDesc *prometheus.Desc
}

// GetName returns a unique name for this kpi that is used for registration
// and configuration.
func (VMStateKPI) GetName() string { return "vm_state_kpi" }

// Init initializes the kpi, e.g. by creating the necessary Prometheus
// descriptors. The base kpi is also initialized with the provided database,
// client and options.
func (k *VMStateKPI) Init(db *db.DB, client client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, client, opts); err != nil {
		return err
	}
	k.vmStateDesc = prometheus.NewDesc("cortex_vm_state",
		"Current state of the VM, e.g. running, stopped, paused, etc.",
		[]string{"az", "hvtype", "state"}, nil,
	)
	return nil
}

// Describe sends the descriptor of this kpi to the provided channel. This is
// used by Prometheus to know which metrics this kpi exposes.
func (k *VMStateKPI) Describe(ch chan<- *prometheus.Desc) { ch <- k.vmStateDesc }

// Collect collects the current state of vms from the database and sends it as
// Prometheus metrics to the provided channel.
func (k *VMStateKPI) Collect(ch chan<- prometheus.Metric) {
	vmStateKPIlogger.Info("collecting vm state kpi")

	// This can happen when no datasource is provided that connects to a database.
	if k.DB == nil {
		vmStateKPIlogger.Error(nil, "no database connection, cannot collect vm state kpi")
		return
	}

	// Get all vms with their current state from the database.
	var servers []nova.Server
	nServers, err := k.DB.Select(&servers, "SELECT * FROM "+nova.Server{}.TableName())
	if err != nil {
		vmStateKPIlogger.Error(err, "failed to query servers from database")
		return
	}
	vmStateKPIlogger.Info("queried servers from database", "nServers", nServers)

	// Get all flavors from the database to map them to the vms.
	var flavors []nova.Flavor
	nFlavors, err := k.DB.Select(&flavors, "SELECT * FROM "+nova.Flavor{}.TableName())
	if err != nil {
		vmStateKPIlogger.Error(err, "failed to query flavors from database")
		return
	}
	vmStateKPIlogger.Info("queried flavors from database", "nFlavors", nFlavors)

	flavorsByName := make(map[string]nova.Flavor, len(flavors))
	for _, flavor := range flavors {
		flavorsByName[flavor.Name] = flavor
	}

	type labels struct {
		az     string
		hvtype string
		state  string
	}
	counts := make(map[labels]float64)

	// For each vm, get its hypervisor type and count up.
	for _, server := range servers {
		flavor, ok := flavorsByName[server.FlavorName]
		if !ok {
			vmStateKPIlogger.Error(nil, "flavor not found for server", "server",
				server.ID, "flavor", server.FlavorName)
			continue
		}
		hypervisorType, err := flavor.GetHypervisorType()
		if err != nil {
			vmStateKPIlogger.Error(err, "failed to get hypervisor type for server",
				"server", server.ID, "flavor", flavor.Name)
			continue
		}
		key := labels{
			az:     server.OSEXTAvailabilityZone,
			hvtype: string(hypervisorType),
			state:  server.Status,
		}
		counts[key]++
	}

	// Emit metrics to prometheus.
	for key, count := range counts {
		ch <- prometheus.MustNewConstMetric(k.vmStateDesc, prometheus.GaugeValue, count,
			key.az, key.hvtype, key.state)
	}
}
