// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"errors"
	"strconv"
	"strings"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/kpis/plugins"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/prometheus/client_golang/prometheus"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var vmFaultsKPIlogger = ctrl.Log.WithName("vm-faults-kpi")

// This kpi tracks vm faults in the datacenter. It exposes helpful information
// about the faults, such as the availability zone, hypervisor type, vm state,
// and error info if available. This can be used to identify issues in the
// datacenter and to monitor the overall health of the vms.
type VMFaultsKPI struct {
	plugins.BaseKPI[struct{} /* No opts */]

	// vmFaultsDesc describes the prometheus metric for vm faults.
	vmFaultsDesc *prometheus.Desc
}

// GetName returns a unique name for this kpi that is used for registration
// and configuration.
func (VMFaultsKPI) GetName() string { return "vm_faults_kpi" }

// Init initializes the kpi, e.g. by creating the necessary Prometheus
// descriptors. The base kpi is also initialized with the provided database,
// client and options.
func (k *VMFaultsKPI) Init(db *db.DB, client client.Client, opts conf.RawOpts) error {
	if err := k.BaseKPI.Init(db, client, opts); err != nil {
		return err
	}
	k.vmFaultsDesc = prometheus.NewDesc("cortex_vm_faults",
		"Number of vm faults in the datacenter",
		[]string{"az", "hvtype", "state", "faultcode", "faultmsg", "faultyvm"}, nil,
	)
	return nil
}

// Describe sends the descriptor of this kpi to the provided channel. This is
// used by Prometheus to know which metrics this kpi exposes.
func (k *VMFaultsKPI) Describe(ch chan<- *prometheus.Desc) { ch <- k.vmFaultsDesc }

// Collect collects the current state of vms from the database and sends it as
// Prometheus metrics to the provided channel.
func (k *VMFaultsKPI) Collect(ch chan<- prometheus.Metric) {
	vmFaultsKPIlogger.Info("collecting metrics")

	// This can happen when no datasource is provided that connects to a database.
	if k.DB == nil {
		err := errors.New("no database connection")
		vmFaultsKPIlogger.Error(err, "cannot collect metric")
		return
	}

	// Get all vms with their current state from the database.
	var servers []nova.Server
	nServers, err := k.DB.Select(&servers, "SELECT * FROM "+nova.Server{}.TableName())
	if err != nil {
		vmFaultsKPIlogger.Error(err, "failed to query servers from database")
		return
	}
	vmFaultsKPIlogger.Info("queried servers from database", "nServers", nServers)

	// Get all flavors from the database to map them to the vms.
	var flavors []nova.Flavor
	nFlavors, err := k.DB.Select(&flavors, "SELECT * FROM "+nova.Flavor{}.TableName())
	if err != nil {
		vmFaultsKPIlogger.Error(err, "failed to query flavors from database")
		return
	}
	vmFaultsKPIlogger.Info("queried flavors from database", "nFlavors", nFlavors)

	flavorsByName := make(map[string]nova.Flavor, len(flavors))
	for _, flavor := range flavors {
		flavorsByName[flavor.Name] = flavor
	}

	type labels struct {
		az         string
		hvtype     string
		state      string
		errcode    string
		errmessage string
		faultyVM   string
	}
	counts := make(map[labels]float64)

	// For each vm, get its hypervisor type and count up.
	// Note: this will also expose vms that are NOT in an error state,
	// but this can be useful to compare it to the number of faulty vms.
	for _, server := range servers {
		flavor, ok := flavorsByName[server.FlavorName]
		if !ok {
			vmFaultsKPIlogger.Info("warning: flavor not found for server", "server",
				server.ID, "flavor", server.FlavorName)
			continue
		}
		hypervisorType, err := flavor.GetHypervisorType()
		if err != nil {
			vmFaultsKPIlogger.Error(err, "failed to get hypervisor type for server",
				"server", server.ID, "flavor", flavor.Name)
			continue
		}
		var errcode uint = 0
		if server.FaultCode != nil {
			errcode = *server.FaultCode
		}
		errmsg := "n/a"
		if server.FaultMessage != nil {
			errmsg = *server.FaultMessage
			// Sometimes the VM ID may appear in the error message, which can
			// lead to high cardinality in the metric. To avoid this, we replace
			// the VM ID with a placeholder.
			errmsg = strings.ReplaceAll(errmsg, server.ID, "<vm_id>")
		}
		// Only provide the server ID for faulty VMs, to avoid cardinality
		// explosion in the metric.
		faultyVM := "no"
		if server.FaultCode != nil || server.FaultMessage != nil {
			faultyVM = server.ID
		}
		key := labels{
			az:         server.OSEXTAvailabilityZone,
			hvtype:     string(hypervisorType),
			state:      server.Status,
			errcode:    strconv.FormatUint(uint64(errcode), 10),
			errmessage: errmsg,
			faultyVM:   faultyVM,
		}
		counts[key]++
	}

	// Emit metrics to prometheus.
	for key, count := range counts {
		ch <- prometheus.MustNewConstMetric(k.vmFaultsDesc, prometheus.GaugeValue, count,
			key.az, key.hvtype, key.state, key.errcode, key.errmessage, key.faultyVM)
	}
	vmFaultsKPIlogger.Info("collected metrics", "nMetrics", len(counts))
}
