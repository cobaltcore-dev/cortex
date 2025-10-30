// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"

	libconf "github.com/cobaltcore-dev/cortex/lib/conf"
	"github.com/cobaltcore-dev/cortex/lib/db"
	"github.com/cobaltcore-dev/cortex/scheduling/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/descheduling/nova/plugins"
	"github.com/cobaltcore-dev/cortex/scheduling/internal/descheduling/nova/plugins/kvm"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Configuration of steps supported by the descheduler.
// The steps actually used by the scheduler are defined through the configuration file.
var supportedSteps = []Step{
	&kvm.AvoidHighStealPctStep{},
}

type Pipeline struct {
	// Kubernetes client to create descheduling resources.
	client.Client
	// Cycle detector to avoid cycles in descheduling.
	CycleDetector CycleDetector
	// Monitor to use for tracking the pipeline.
	Monitor Monitor

	// Steps to execute in the descheduler.
	steps []Step
}

func (p *Pipeline) Init(confedSteps []v1alpha1.Step, supportedSteps []Step, db db.DB) error {
	supportedStepsByName := make(map[string]Step)
	for _, step := range supportedSteps {
		supportedStepsByName[step.GetName()] = step
	}

	// Load all steps from the configuration.
	p.steps = make([]Step, 0, len(confedSteps))
	for _, stepConf := range confedSteps {
		step, ok := supportedStepsByName[stepConf.Spec.Impl]
		if !ok {
			return errors.New("descheduler: unsupported step: " + stepConf.Spec.Impl)
		}
		step = monitorStep(step, p.Monitor)
		rawOpts := libconf.NewRawOptsBytes(stepConf.Spec.Opts.Raw)
		if err := step.Init(db, rawOpts); err != nil {
			return err
		}
		p.steps = append(p.steps, step)
		slog.Info(
			"descheduler: added step",
			"name", stepConf.Name,
			"options", rawOpts,
		)
	}
	return nil
}

// Execute the descheduler steps in parallel and collect the decisions made by
// each step.
func (p *Pipeline) run() map[string][]plugins.Decision {
	if p.Monitor.pipelineRunTimer != nil {
		timer := prometheus.NewTimer(p.Monitor.pipelineRunTimer)
		defer timer.ObserveDuration()
	}
	var lock sync.Mutex
	decisionsByStep := map[string][]plugins.Decision{}
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
func (p *Pipeline) combine(decisionsByStep map[string][]plugins.Decision) []plugins.Decision {
	// Order the step names to have a consistent order of processing.
	stepNames := make([]string, 0, len(decisionsByStep))
	for stepName := range decisionsByStep {
		stepNames = append(stepNames, stepName)
	}
	slices.Sort(stepNames)
	// If there are more than one decision for the same vm, we need to combine them.
	decisionsByVMID := make(map[string][]plugins.Decision)
	for _, stepName := range stepNames {
		decisions := decisionsByStep[stepName]
		for _, decision := range decisions {
			decisionsByVMID[decision.VMID] = append(decisionsByVMID[decision.VMID], decision)
		}
	}

	combinedDecisions := make([]plugins.Decision, 0, len(decisionsByVMID))
	for vmID, decisions := range decisionsByVMID {
		if len(decisions) == 1 {
			combinedDecisions = append(combinedDecisions, decisions[0])
			continue
		}
		// If the host is not the same in all decisions, we need to skip this vm.
		host := decisions[0].Host
		sameHost := true
		for _, decision := range decisions[1:] {
			if decision.Host != host {
				sameHost = false
				break
			}
		}
		if !sameHost {
			slog.Error("descheduler: skipping vm with conflicting origin hosts", "vmId", vmID, "decisions", decisions)
			continue
		}
		var reasonBuilder strings.Builder
		reasonBuilder.WriteString("multiple reasons: ")
		for i, decision := range decisions {
			if i > 0 {
				reasonBuilder.WriteString("; ")
			}
			reasonBuilder.WriteString(decision.Reason)
		}
		combinedDecisions = append(combinedDecisions, plugins.Decision{
			VMID:   vmID,
			Reason: reasonBuilder.String(),
			Host:   host,
		})
	}

	slog.Info("descheduler: combined decisions", "combined", combinedDecisions)
	return combinedDecisions
}

func (p *Pipeline) createDeschedulings(ctx context.Context) error {
	decisionsByStep := p.run()
	if len(decisionsByStep) == 0 {
		slog.Info("descheduler: no decisions made in this run")
		return nil
	}
	slog.Info("descheduler: decisions made", "decisionsByStep", decisionsByStep)
	decisions := p.combine(decisionsByStep)
	var err error
	decisions, err = p.CycleDetector.Filter(ctx, decisions)
	if err != nil {
		slog.Error("descheduler: failed to filter decisions for cycles", "error", err)
		return err
	}
	for _, decision := range decisions {
		// Precaution: If a descheduling for the VM already exists, skip it.
		// The TTL controller will clean up old deschedulings so the vm
		// can be descheduled again later if needed, or we can manually
		// delete the descheduling if we want to deschedule the VM again.
		var existing v1alpha1.Descheduling
		err := p.Get(ctx, client.ObjectKey{Name: decision.VMID}, &existing)
		if err == nil {
			slog.Info("descheduler: descheduling already exists for VM, skipping", "vmId", decision.VMID)
			continue
		}

		descheduling := &v1alpha1.Descheduling{}
		descheduling.Name = decision.VMID
		descheduling.Spec.Ref = decision.VMID
		descheduling.Spec.RefType = v1alpha1.DeschedulingSpecVMReferenceNovaServerUUID
		descheduling.Spec.PrevHostType = v1alpha1.DeschedulingSpecHostTypeNovaComputeHostName
		descheduling.Spec.PrevHost = decision.Host
		descheduling.Spec.Reason = decision.Reason
		descheduling.Status.Phase = v1alpha1.DeschedulingStatusPhaseQueued
		if err := p.Create(ctx, descheduling); err != nil {
			return err
		}
		slog.Info("descheduler: created descheduling", "vmId", decision.VMID, "host", decision.Host, "reason", decision.Reason)
	}
	return nil
}
