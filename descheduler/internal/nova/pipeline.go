// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/cobaltcore-dev/cortex/descheduler/internal/conf"
	"github.com/cobaltcore-dev/cortex/descheduler/internal/nova/plugins"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/keystone"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sapcc/go-bits/jobloop"
)

// Configuration of steps supported by the descheduler.
// The steps actually used by the scheduler are defined through the configuration file.
var SupportedSteps = []Step{
	&plugins.DemoStep{}, // Example step, replace with actual steps.
}

type Pipeline struct {
	// Steps to execute in the descheduler.
	steps []Step
	// Configuration for the descheduler.
	config conf.DeschedulerConfig
	// Nova API to use for the descheduler.
	novaAPI NovaAPI
	// Executor for the migrations.
	executor Executor
	// Cycle detector to avoid cycles in descheduling.
	cycleDetector CycleDetector
	// Monitor to use for tracking the pipeline.
	monitor Monitor
}

func NewDescheduler(config conf.DeschedulerConfig, m Monitor, keystoneAPI keystone.KeystoneAPI) *Pipeline {
	// Initialize the descheduler with the provided configuration and database.
	novaAPI := NewNovaAPI(keystoneAPI, config.Nova)
	descheduler := &Pipeline{
		config:        config,
		novaAPI:       novaAPI,
		executor:      NewExecutor(novaAPI, m, config),
		cycleDetector: NewCycleDetector(novaAPI, config),
		monitor:       m,
	}
	return descheduler
}

func (p *Pipeline) Init(supported []Step, ctx context.Context, db db.DB, config conf.DeschedulerConfig) {
	p.novaAPI.Init(ctx)

	supportedStepsByName := make(map[string]Step)
	for _, step := range supported {
		supportedStepsByName[step.GetName()] = step
	}

	// Load all steps from the configuration.
	p.steps = make([]Step, 0, len(config.Nova.Plugins))
	for _, stepConf := range config.Nova.Plugins {
		step, ok := supportedStepsByName[stepConf.Name]
		if !ok {
			slog.Error("descheduler: step not supported", "name", stepConf.Name)
			continue
		}
		step = monitorStep(step, p.monitor)
		if err := step.Init(db, stepConf.Options); err != nil {
			slog.Error("descheduler: failed to initialize step", "name", stepConf.Name, "error", err)
			continue
		}
		p.steps = append(p.steps, step)
		slog.Info(
			"descheduler: added step",
			"name", stepConf.Name,
			"options", stepConf.Options,
		)
	}
}

// Execute the descheduler steps in parallel and collect the decisions made by
// each step.
func (p *Pipeline) run() map[string][]string {
	if p.monitor.pipelineRunTimer != nil {
		timer := prometheus.NewTimer(p.monitor.pipelineRunTimer)
		defer timer.ObserveDuration()
	}
	var lock sync.Mutex
	decisionsByStep := map[string][]string{}
	var wg sync.WaitGroup
	for _, step := range p.steps {
		wg.Go(func() {
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
		})
	}
	wg.Wait()
	return decisionsByStep
}

// Combine the decisions made by each step into a single list of vms to deschedule.
func (p *Pipeline) deduplicate(decisionsByStep map[string][]string) []string {
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

func (p *Pipeline) DeschedulePeriodically(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			slog.Info("descheduler shutting down")
			return
		default:
			decisionsByStep := p.run()
			if len(decisionsByStep) == 0 {
				slog.Info("descheduler: no decisions made in this run")
				time.Sleep(jobloop.DefaultJitter(time.Minute))
				continue
			}
			slog.Info("descheduler: decisions made", "decisionsByStep", decisionsByStep)
			decisions := p.deduplicate(decisionsByStep)
			var err error
			decisions, err = p.cycleDetector.Filter(ctx, decisions)
			if err != nil {
				slog.Error("descheduler: failed to filter decisions for cycles", "error", err)
				time.Sleep(jobloop.DefaultJitter(time.Minute))
				continue
			}
			if err = p.executor.Deschedule(ctx, decisions); err != nil {
				slog.Error("descheduler: failed to deschedule VMs", "error", err)
			}
			time.Sleep(jobloop.DefaultJitter(time.Minute))
		}
	}
}
