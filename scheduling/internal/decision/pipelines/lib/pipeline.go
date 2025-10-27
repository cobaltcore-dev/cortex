// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"errors"
	"log/slog"
	"maps"
	"math"
	"slices"
	"sort"
	"sync"

	libconf "github.com/cobaltcore-dev/cortex/lib/conf"
	"github.com/cobaltcore-dev/cortex/lib/db"
	"github.com/cobaltcore-dev/cortex/scheduling/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

type Pipeline[RequestType PipelineRequest] interface {
	// Run the scheduling pipeline with the given request.
	Run(request RequestType) (v1alpha1.DecisionResult, error)
}

type Premodifier[RequestType PipelineRequest] interface {
	// Modify the request before it is sent to the pipeline.
	ModifyRequest(request *RequestType) error
}

// Pipeline of scheduler steps.
type pipeline[RequestType PipelineRequest] struct {
	// The activation function to use when combining the
	// results of the scheduler steps.
	ActivationFunction
	// The order in which scheduler steps are applied, by their step name.
	order []string
	// The steps by their name.
	steps map[string]Step[RequestType]
	// Monitor to observe the pipeline.
	monitor PipelineMonitor
}

type StepWrapper[RequestType PipelineRequest] func(Step[RequestType], v1alpha1.Step) (Step[RequestType], error)

// Create a new pipeline with steps contained in the configuration.
func NewPipeline[RequestType PipelineRequest](
	supportedSteps map[string]func() Step[RequestType],
	confedSteps []v1alpha1.Step,
	stepWrappers []StepWrapper[RequestType],
	database db.DB,
	monitor PipelineMonitor,
) (Pipeline[RequestType], error) {
	// Load all steps from the configuration.
	steps := []Step[RequestType]{}
	order := []string{}
	for _, stepConfig := range confedSteps {
		makeStep, ok := supportedSteps[stepConfig.Name]
		if !ok {
			return nil, errors.New("unsupported scheduler step: " + stepConfig.Name)
		}
		step := makeStep()
		// Apply the step wrappers to the step.
		for _, wrapper := range stepWrappers {
			var err error
			if step, err = wrapper(step, stepConfig); err != nil {
				return nil, errors.New("failed to wrap scheduler step: " + err.Error())
			}
		}
		opts := libconf.NewRawOptsBytes(stepConfig.Spec.Opts.Raw)
		if err := step.Init(database, opts); err != nil {
			return nil, errors.New("failed to initialize pipeline step: " + err.Error())
		}
		steps = append(steps, step)
		order = append(order, stepConfig.Name)
		slog.Info(
			"scheduler: added step",
			"name", stepConfig.Name,
			"impl", stepConfig.Spec.Impl,
			"options", opts,
		)
	}
	stepsByName := make(map[string]Step[RequestType], len(steps))
	for _, step := range steps {
		stepsByName[step.GetName()] = step
	}

	return &pipeline[RequestType]{
		// All steps can be run in parallel.
		order:   order,
		steps:   stepsByName,
		monitor: monitor,
	}, nil
}

// Execute the scheduler steps in groups of the execution order.
// The steps are run in parallel.
func (p *pipeline[RequestType]) runSteps(log *slog.Logger, request RequestType) map[string]map[string]float64 {
	var lock sync.Mutex
	activationsByStep := map[string]map[string]float64{}
	var wg sync.WaitGroup
	for _, stepName := range p.order {
		step := p.steps[stepName]
		wg.Go(func() {
			stepLog := log.With("stepName", stepName)
			stepLog.Info("scheduler: running step")
			result, err := step.Run(stepLog, request)
			if errors.Is(err, ErrStepSkipped) {
				stepLog.Info("scheduler: step skipped")
				return
			}
			if err != nil {
				stepLog.Error("scheduler: failed to run step", "error", err)
				return
			}
			stepLog.Info("scheduler: finished step")
			lock.Lock()
			defer lock.Unlock()
			activationsByStep[stepName] = result.Activations
		})
	}
	wg.Wait()
	return activationsByStep
}

// Apply an initial weight to the subjects.
//
// Context:
// Openstack schedulers may give us very large (positive/negative) weights such as
// -99,000 or 99,000 (Nova). We want to respect these values, but still adjust them
// to a meaningful value. If the scheduler really doesn't want us to run on a subject, it
// should run a filter instead of setting a weight.
func (p *pipeline[RequestType]) normalizeInputWeights(weights map[string]float64) map[string]float64 {
	normalizedWeights := make(map[string]float64, len(weights))
	for subjectname, weight := range weights {
		normalizedWeights[subjectname] = math.Tanh(weight)
	}
	return normalizedWeights
}

// Apply the step weights to the input weights.
func (p *pipeline[RequestType]) applyStepWeights(
	stepWeights map[string]map[string]float64,
	inWeights map[string]float64,
) map[string]float64 {
	// Copy to avoid modifying the original weights.
	outWeights := make(map[string]float64, len(inWeights))
	maps.Copy(outWeights, inWeights)

	// Apply all activations in the strict order defined by the configuration.
	for _, stepName := range p.order {
		stepActivations, ok := stepWeights[stepName]
		if !ok {
			// This is ok, since steps can be skipped.
			continue
		}
		outWeights = p.Apply(outWeights, stepActivations)
	}
	return outWeights
}

// Sort the subjects by their weights.
func (s *pipeline[RequestType]) sortSubjectsByWeights(weights map[string]float64) []string {
	// Sort the subjects (keys) by their weights.
	subjects := slices.Collect(maps.Keys(weights))
	sort.Slice(subjects, func(i, j int) bool {
		return weights[subjects[i]] > weights[subjects[j]]
	})
	return subjects
}

// Evaluate the pipeline and return a list of subjects in order of preference.
func (p *pipeline[RequestType]) Run(request RequestType) (v1alpha1.DecisionResult, error) {
	slogArgs := request.GetTraceLogArgs()
	slogArgsAny := make([]any, 0, len(slogArgs))
	for _, arg := range slogArgs {
		slogArgsAny = append(slogArgsAny, arg)
	}
	traceLog := slog.With(slogArgsAny...)

	subjectsIn := request.GetSubjects()
	traceLog.Info("scheduler: starting pipeline", "subjects", subjectsIn)

	// Get weights from the scheduler steps, apply them to the input weights, and
	// sort the subjects by their weights. The input weights are normalized before
	// applying the step weights.
	stepWeights := p.runSteps(traceLog, request)
	traceLog.Info("scheduler: finished pipeline")
	inWeights := p.normalizeInputWeights(request.GetWeights())
	traceLog.Info("scheduler: input weights", "weights", inWeights)
	outWeights := p.applyStepWeights(stepWeights, inWeights)
	traceLog.Info("scheduler: output weights", "weights", outWeights)
	subjects := p.sortSubjectsByWeights(outWeights)
	traceLog.Info("scheduler: sorted subjects", "subjects", subjects)

	// Collect some metrics about the pipeline execution.
	go p.monitor.observePipelineResult(request, subjects)

	result := v1alpha1.DecisionResult{
		RawInWeights:         request.GetWeights(),
		NormalizedInWeights:  inWeights,
		AggregatedOutWeights: outWeights,
		OrderedHosts:         subjects,
	}
	if len(subjects) > 0 {
		result.TargetHost = &subjects[0]
	}
	for _, stepName := range p.order {
		if activations, ok := stepWeights[stepName]; ok {
			result.StepResults = append(result.StepResults, v1alpha1.StepResult{
				StepRef:     corev1.ObjectReference{Name: stepName},
				Activations: activations,
			})
		}
	}
	return result, nil
}
