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
}

func NewDescheduler(config conf.DeschedulerConfig, db db.DB) *Descheduler {
	// Initialize the descheduler with the provided configuration and database.
	descheduler := &Descheduler{
		config: config,
	}
	// Initialize the steps based on the configuration.
	descheduler.Init(context.Background(), db, config)
	return descheduler
}

func (d *Descheduler) Init(ctx context.Context, db db.DB, config conf.DeschedulerConfig) {
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

	d.config = config
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
	vmsToDeschedule := []string{}
	for _, decisions := range decisionsByStep {
		vmsToDeschedule = append(vmsToDeschedule, decisions...)
	}
	return vmsToDeschedule
}

// Execute the virtual machine live-migrations using the Nova API.
func (d *Descheduler) execute(decisions []string) {
	for _, decision := range decisions {
		slog.Info("descheduler: executing decision", "decision", decision)
		if !d.config.Nova.DisableDryRun {
			slog.Info("descheduler: dry-run enabled, skipping execution", "decision", decision)
			continue
		}
		slog.Info("descheduler: executing migration for VM", "vmId", decision)
		// TODO
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
