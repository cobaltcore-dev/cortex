// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/descheduler/nova/plugins"
	"github.com/cobaltcore-dev/cortex/internal/keystone"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/sapcc/go-bits/jobloop"
)

// Configuration of steps supported by the descheduler.
// The steps actually used by the scheduler are defined through the configuration file.
var supportedSteps = []Step{
	&plugins.DemoStep{}, // Example step, replace with actual steps.
}

type Descheduler struct {
	// Steps to execute in the descheduler.
	steps []Step
	// Configuration for the descheduler.
	config conf.DeschedulerConfig
	// Keystone API for authentication.
	keystoneAPI keystone.KeystoneAPI
	// Service client for Nova API.
	sc *gophercloud.ServiceClient
}

func NewDescheduler(config conf.DeschedulerConfig, keystoneAPI keystone.KeystoneAPI) *Descheduler {
	// Initialize the descheduler with the provided configuration and database.
	descheduler := &Descheduler{
		config:      config,
		keystoneAPI: keystoneAPI,
	}
	return descheduler
}

func (d *Descheduler) Init(ctx context.Context, db db.DB, config conf.DeschedulerConfig) {
	if err := d.keystoneAPI.Authenticate(ctx); err != nil {
		panic(err)
	}
	// Automatically fetch the nova endpoint from the keystone service catalog.
	provider := d.keystoneAPI.Client()
	serviceType := "compute"
	url, err := d.keystoneAPI.FindEndpoint(config.Nova.Availability, serviceType)
	if err != nil {
		panic(err)
	}
	slog.Info("using nova endpoint", "url", url)
	d.sc = &gophercloud.ServiceClient{
		ProviderClient: provider,
		Endpoint:       url,
		Type:           serviceType,
		Microversion:   "2.53",
	}

	supportedStepsByName := make(map[string]Step)
	for _, step := range supportedSteps {
		supportedStepsByName[step.GetName()] = step
	}

	// Load all steps from the configuration.
	d.steps = make([]Step, 0, len(config.Nova.Plugins))
	for _, stepConf := range config.Nova.Plugins {
		step, ok := supportedStepsByName[stepConf.Name]
		if !ok {
			slog.Error("descheduler: step not supported", "name", stepConf.Name)
			continue
		}
		if err := step.Init(db, stepConf.Options); err != nil {
			slog.Error("descheduler: failed to initialize step", "name", stepConf.Name, "error", err)
			continue
		}
		d.steps = append(d.steps, step)
		slog.Info(
			"descheduler: added step",
			"name", stepConf.Name,
			"options", stepConf.Options,
		)
	}
}

// Execute the descheduler steps in parallel and collect the decisions made by
// each step.
func (d *Descheduler) run() map[string][]string {
	var lock sync.Mutex
	decisionsByStep := map[string][]string{}
	var wg sync.WaitGroup
	for _, step := range d.steps {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slog.Info("descheduler: running step", "name", step.GetName())
			decisions, err := step.Run()
			if errors.Is(err, ErrStepSkipped) {
				slog.Info("descheduler: step skipped", "name", step.GetName())
				return
			}
			if err != nil {
				slog.Error("descheduler: failed to run step", "name", step.GetName(), "error", err)
				return
			}
			slog.Info("descheduler: finished step", "name", step.GetName())
			lock.Lock()
			defer lock.Unlock()
			decisionsByStep[step.GetName()] = decisions
		}()
	}
	wg.Wait()
	return decisionsByStep
}

// Combine the decisions made by each step into a single list of vms to deschedule.
func (d *Descheduler) deduplicate(decisionsByStep map[string][]string) []string {
	// Remove duplicates by converting to a map and back to a slice.
	uniqueVms := make(map[string]struct{}, len(decisionsByStep))
	for _, decisions := range decisionsByStep {
		for _, vmid := range decisions {
			uniqueVms[vmid] = struct{}{}
		}
	}
	vmsToDeschedule := make([]string, 0, len(uniqueVms))
	for vmid := range uniqueVms {
		vmsToDeschedule = append(vmsToDeschedule, vmid)
	}
	slog.Info("descheduler: deduplicated decisions", "vmsToDeschedule", vmsToDeschedule)
	return vmsToDeschedule
}

// Execute the virtual machine live-migrations using the Nova API.
func (d *Descheduler) execute(decisions []string) {
	for _, vmid := range decisions {
		slog.Info("descheduler: live-migrating", "vmid", vmid)
		if !d.config.Nova.DisableDryRun {
			slog.Info("descheduler: dry-run enabled, skipping execution", "vmid", vmid)
			continue
		}
		slog.Info("descheduler: executing migration for VM", "vmId", vmid)
		ctx := context.Background()
		lmo := servers.LiveMigrateOpts{}
		result := servers.LiveMigrate(ctx, d.sc, vmid, lmo)
		if result.Err != nil {
			slog.Error("descheduler: failed to live-migrate VM", "vmId", vmid, "error", result.Err)
			continue
		}
		// Wait for the migration to complete.
		slog.Info("descheduler: live-migration started", "vmId", vmid)
		for {
			status, err := servers.Get(ctx, d.sc, vmid).Extract()
			if err != nil {
				slog.Error("descheduler: failed to get VM status", "vmId", vmid, "error", err)
				// Consider migration as failed
				break
			}
			if status.Status == "ACTIVE" {
				slog.Info("descheduler: live-migration completed", "vmId", vmid)
				break
			}
			if status.Status == "ERROR" {
				slog.Error("descheduler: live-migration failed", "vmId", vmid)
				break
			}
			slog.Info("descheduler: waiting for live-migration to complete", "vmId", vmid, "status", status.Status)
			time.Sleep(5 * time.Second) // Wait before checking the status again.
		}
	}
}

func (d *Descheduler) DeschedulePeriodically(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			slog.Info("descheduler shutting down")
			return
		default:
			decisionsByStep := d.run()
			if len(decisionsByStep) == 0 {
				slog.Info("descheduler: no decisions made in this run")
				time.Sleep(jobloop.DefaultJitter(time.Minute))
				continue
			}
			slog.Info("descheduler: decisions made", "decisionsByStep", decisionsByStep)
			decisions := d.deduplicate(decisionsByStep)
			if len(decisions) == 0 {
				slog.Info("descheduler: no unique decisions made in this run")
				time.Sleep(jobloop.DefaultJitter(time.Minute))
				continue
			}
			slog.Info("descheduler: unique decisions made", "decisions", decisions)
			d.execute(decisions)
			time.Sleep(jobloop.DefaultJitter(time.Minute))
		}
	}
}
