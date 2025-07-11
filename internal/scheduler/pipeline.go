// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"errors"
	"log/slog"
	"maps"
	"math"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/mqtt"
)

type Pipeline[RequestType PipelineRequest] interface {
	// Run the scheduling pipeline with the given request.
	Run(request RequestType) ([]string, error)
}

// Pipeline of scheduler steps.
type pipeline[RequestType PipelineRequest] struct {
	// The activation function to use when combining the
	// results of the scheduler steps.
	ActivationFunction
	// The parallelizable order in which scheduler steps are executed.
	executionOrder [][]Step[RequestType]
	// The order in which scheduler steps are applied, by their step name.
	applicationOrder []string
	// Monitor to observe the pipeline.
	monitor PipelineMonitor
	// MQTT client to publish mqtt data when the pipeline is finished.
	mqttClient mqtt.Client
	// MQTT topic to publish telemetry data on when the pipeline is finished.
	mqttTopic string
}

type StepWrapper[RequestType PipelineRequest] func(Step[RequestType], conf.SchedulerStepConfig) Step[RequestType]

// Create a new pipeline with steps contained in the configuration.
func NewPipeline[RequestType PipelineRequest](
	supportedSteps []Step[RequestType],
	confedSteps []conf.SchedulerStepConfig,
	stepWrappers []StepWrapper[RequestType],
	config conf.SchedulerConfig,
	database db.DB,
	monitor PipelineMonitor,
	mqttClient mqtt.Client,
	mqttTopic string,
) Pipeline[RequestType] {

	supportedStepsByName := make(map[string]Step[RequestType])
	for _, step := range supportedSteps {
		supportedStepsByName[step.GetName()] = step
	}

	// Load all steps from the configuration.
	steps := []Step[RequestType]{}
	applicationOrder := []string{}
	for _, stepConfig := range confedSteps {
		step, ok := supportedStepsByName[stepConfig.Name]
		if !ok {
			panic("unknown pipeline step: " + stepConfig.Name)
		}
		// Apply the step wrappers to the step.
		for _, wrapper := range stepWrappers {
			step = wrapper(step, stepConfig)
		}
		if err := step.Init(database, stepConfig.Options); err != nil {
			panic("failed to initialize pipeline step: " + err.Error())
		}
		steps = append(steps, step)
		applicationOrder = append(applicationOrder, stepConfig.Name)
		slog.Info(
			"scheduler: added step",
			"name", stepConfig.Name,
			"options", stepConfig.Options,
		)
	}

	return &pipeline[RequestType]{
		// All steps can be run in parallel.
		executionOrder:   [][]Step[RequestType]{steps},
		applicationOrder: applicationOrder,
		monitor:          monitor,
		mqttClient:       mqttClient,
		mqttTopic:        mqttTopic,
	}
}

// Execute the scheduler steps in groups of the execution order.
// The steps are run in parallel.
func (p *pipeline[RequestType]) runSteps(log *slog.Logger, request RequestType) map[string]map[string]float64 {
	var lock sync.Mutex
	activationsByStep := map[string]map[string]float64{}
	for _, steps := range p.executionOrder {
		var wg sync.WaitGroup
		for _, step := range steps {
			wg.Add(1)
			go func() {
				defer wg.Done()
				log.Info("scheduler: running step", "name", step.GetName())
				result, err := step.Run(log, request)
				if errors.Is(err, ErrStepSkipped) {
					log.Info("scheduler: step skipped", "name", step.GetName())
					return
				}
				if err != nil {
					log.Error("scheduler: failed to run step", "error", err)
					return
				}
				log.Info("scheduler: finished step", "name", step.GetName())
				lock.Lock()
				defer lock.Unlock()
				activationsByStep[step.GetName()] = result.Activations
			}()
		}
		wg.Wait()
	}
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
	for _, stepName := range p.applicationOrder {
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
func (p *pipeline[RequestType]) Run(request RequestType) ([]string, error) {
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

	// Publish telemetry information about the scheduling to an mqtt broker.
	// In this way, other services can connect and record the scheduler
	// behavior over a longer time, or react to the scheduling decision.
	go p.mqttClient.Publish(p.mqttTopic, map[string]any{
		"time":    time.Now().Unix(),
		"request": request,
		"order":   p.applicationOrder,
		"in":      inWeights,
		"steps":   stepWeights,
		"out":     outWeights,
	})

	return subjects, nil
}
