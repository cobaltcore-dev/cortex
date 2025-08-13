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

// Get a unique key for the step, combining its name and alias.
func getStepKey[RequestType PipelineRequest](step Step[RequestType]) string {
	name := step.GetName()
	alias := step.GetAlias()
	key := ""
	if alias == "" {
		key = name
	} else {
		key = name + " (" + alias + ")"
	}
	return key
}

// Create a new pipeline with steps contained in the configuration.
func NewPipeline[RequestType PipelineRequest](
	supportedSteps map[string]func() Step[RequestType],
	confedSteps []conf.SchedulerStepConfig,
	stepWrappers []StepWrapper[RequestType],
	database db.DB,
	monitor PipelineMonitor,
	mqttClient mqtt.Client,
	mqttTopic string,
) Pipeline[RequestType] {
	// Load all steps from the configuration.
	steps := []Step[RequestType]{}
	applicationOrder := []string{}
	for _, stepConfig := range confedSteps {
		makeStep, ok := supportedSteps[stepConfig.Name]
		if !ok {
			panic("unknown pipeline step: " + stepConfig.Name)
		}
		step := makeStep()
		// Apply the step wrappers to the step.
		for _, wrapper := range stepWrappers {
			step = wrapper(step, stepConfig)
		}
		if err := step.Init(stepConfig.Alias, database, stepConfig.Options); err != nil {
			panic("failed to initialize pipeline step: " + err.Error())
		}
		steps = append(steps, step)
		applicationOrder = append(applicationOrder, getStepKey(step))
		slog.Info(
			"scheduler: added step",
			"name", stepConfig.Name,
			"alias", stepConfig.Alias,
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
				stepLog := log.With("stepName", step.GetName(), "stepAlias", step.GetAlias())
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
				activationsByStep[getStepKey(step)] = result.Activations
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
	for _, stepKey := range p.applicationOrder {
		stepActivations, ok := stepWeights[stepKey]
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

// Telemetry message as will be published to the mqtt broker.
type TelemetryMessage[RequestType PipelineRequest] struct {
	Time    int64                         `json:"time"`
	Request RequestType                   `json:"request"`
	Order   []string                      `json:"order"`
	In      map[string]float64            `json:"in"`
	Steps   map[string]map[string]float64 `json:"steps"`
	Out     map[string]float64            `json:"out"`
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
	go p.mqttClient.Publish(p.mqttTopic, TelemetryMessage[RequestType]{
		Time:    time.Now().Unix(),
		Request: request,
		Order:   p.applicationOrder,
		In:      inWeights,
		Steps:   stepWeights,
		Out:     outWeights,
	})

	return subjects, nil
}
