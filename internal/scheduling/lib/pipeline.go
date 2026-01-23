// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package lib

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"math"
	"slices"
	"sort"
	"sync"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Pipeline[RequestType PipelineRequest] interface {
	// Run the scheduling pipeline with the given request.
	Run(request RequestType) (v1alpha1.DecisionResult, error)
}

// Pipeline of scheduler steps.
type pipeline[RequestType PipelineRequest] struct {
	// The activation function to use when combining the
	// results of the scheduler steps.
	ActivationFunction
	// The order in which filters are applied, by their step name.
	filtersOrder []string
	// The filters by their name.
	filters map[string]Step[RequestType]
	// The order in which weighers are applied, by their step name.
	weighersOrder []string
	// The weighers by their name.
	weighers map[string]Step[RequestType]
	// Monitor to observe the pipeline.
	monitor PipelineMonitor
}

type StepWrapper[RequestType PipelineRequest] func(
	ctx context.Context,
	client client.Client,
	step v1alpha1.StepSpec,
	impl Step[RequestType],
) (Step[RequestType], error)

// Create a new pipeline with filters and weighers contained in the configuration.
func InitNewFilterWeigherPipeline[RequestType PipelineRequest](
	ctx context.Context,
	client client.Client,
	name string,
	supportedFilters map[string]func() Step[RequestType],
	confedFilters []v1alpha1.StepSpec,
	supportedWeighers map[string]func() Step[RequestType],
	confedWeighers []v1alpha1.StepSpec,
	monitor PipelineMonitor,
) PipelineInitResult[Pipeline[RequestType]] {

	pipelineMonitor := monitor.SubPipeline(name)

	// Ensure there are no overlaps between filter and weigher names.
	for filterName := range supportedFilters {
		if _, ok := supportedWeighers[filterName]; ok {
			return PipelineInitResult[Pipeline[RequestType]]{
				CriticalErr: errors.New("step name overlap between filters and weighers: " + filterName),
			}
		}
	}

	// Load all filters from the configuration.
	filtersByName := make(map[string]Step[RequestType], len(confedFilters))
	filtersOrder := []string{}
	for _, filterConfig := range confedFilters {
		slog.Info("scheduler: configuring filter", "name", filterConfig.Name)
		slog.Info("supported:", "filters", maps.Keys(supportedFilters))
		makeFilter, ok := supportedFilters[filterConfig.Name]
		if !ok {
			return PipelineInitResult[Pipeline[RequestType]]{
				CriticalErr: errors.New("unsupported filter name: " + filterConfig.Name),
			}
		}
		filter := makeFilter()
		filter = monitorStep(ctx, client, filterConfig, filter, pipelineMonitor)
		if err := filter.Init(ctx, client, filterConfig); err != nil {
			return PipelineInitResult[Pipeline[RequestType]]{
				CriticalErr: errors.New("failed to initialize filter: " + err.Error()),
			}
		}
		filtersByName[filterConfig.Name] = filter
		filtersOrder = append(filtersOrder, filterConfig.Name)
		slog.Info("scheduler: added filter", "name", filterConfig.Name)
	}

	// Load all weighers from the configuration.
	weighersByName := make(map[string]Step[RequestType], len(confedWeighers))
	weighersOrder := []string{}
	var nonCriticalErr error
	for _, weigherConfig := range confedWeighers {
		slog.Info("scheduler: configuring weigher", "name", weigherConfig.Name)
		slog.Info("supported:", "weighers", maps.Keys(supportedWeighers))
		makeWeigher, ok := supportedWeighers[weigherConfig.Name]
		if !ok {
			nonCriticalErr = errors.New("unsupported weigher name: " + weigherConfig.Name)
			continue // Weighers are optional.
		}
		weigher := makeWeigher()
		// Validate that the weigher doesn't unexpectedly filter out hosts.
		weigher = validateWeigher(weigher)
		weigher = monitorStep(ctx, client, weigherConfig, weigher, pipelineMonitor)
		if err := weigher.Init(ctx, client, weigherConfig); err != nil {
			nonCriticalErr = errors.New("failed to initialize weigher: " + err.Error())
			continue // Weighers are optional.
		}
		weighersByName[weigherConfig.Name] = weigher
		weighersOrder = append(weighersOrder, weigherConfig.Name)
		slog.Info("scheduler: added weigher", "name", weigherConfig.Name)
	}

	return PipelineInitResult[Pipeline[RequestType]]{
		NonCriticalErr: nonCriticalErr,
		Pipeline: &pipeline[RequestType]{
			filtersOrder:  filtersOrder,
			filters:       filtersByName,
			weighersOrder: weighersOrder,
			weighers:      weighersByName,
			monitor:       pipelineMonitor,
		},
	}
}

// Execute filters and collect their activations by step name.
// During this process, the request is mutated to only include the
// remaining subjects.
func (p *pipeline[RequestType]) runFilters(
	log *slog.Logger,
	request RequestType,
) (filteredRequest RequestType) {

	filteredRequest = request
	for _, filterName := range p.filtersOrder {
		filter := p.filters[filterName]
		stepLog := log.With("filter", filterName)
		stepLog.Info("scheduler: running filter")
		result, err := filter.Run(stepLog, filteredRequest)
		if errors.Is(err, ErrStepSkipped) {
			stepLog.Info("scheduler: filter skipped")
			continue
		}
		if err != nil {
			stepLog.Error("scheduler: failed to run filter", "error", err)
			continue
		}
		stepLog.Info("scheduler: finished filter")
		// Mutate the request to only include the remaining subjects.
		// Assume the resulting request type is the same as the input type.
		filteredRequest = filteredRequest.FilterSubjects(result.Activations).(RequestType)
	}
	return filteredRequest
}

// Execute weighers and collect their activations by step name.
func (p *pipeline[RequestType]) runWeighers(
	log *slog.Logger,
	filteredRequest RequestType,
) map[string]map[string]float64 {

	activationsByStep := map[string]map[string]float64{}
	// Weighers can be run in parallel as they do not modify the request.
	var lock sync.Mutex
	var wg sync.WaitGroup
	for _, weigherName := range p.weighersOrder {
		weigher := p.weighers[weigherName]
		wg.Go(func() {
			stepLog := log.With("weigher", weigherName)
			stepLog.Info("scheduler: running weigher")
			result, err := weigher.Run(stepLog, filteredRequest)
			if errors.Is(err, ErrStepSkipped) {
				stepLog.Info("scheduler: weigher skipped")
				return
			}
			if err != nil {
				stepLog.Error("scheduler: failed to run weigher", "error", err)
				return
			}
			stepLog.Info("scheduler: finished weigher")
			lock.Lock()
			defer lock.Unlock()
			activationsByStep[weigherName] = result.Activations
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
func (p *pipeline[RequestType]) applyWeights(
	stepWeights map[string]map[string]float64,
	inWeights map[string]float64,
) map[string]float64 {
	// Copy to avoid modifying the original weights.
	outWeights := make(map[string]float64, len(inWeights))
	maps.Copy(outWeights, inWeights)

	// Apply all activations in the strict order defined by the configuration.
	for _, weigherName := range p.weighersOrder {
		weigherActivations, ok := stepWeights[weigherName]
		if !ok {
			// This is ok, since steps can be skipped.
			continue
		}
		outWeights = p.Apply(outWeights, weigherActivations)
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

	// Normalize the input weights so we can apply step weights meaningfully.
	inWeights := p.normalizeInputWeights(request.GetWeights())
	traceLog.Info("scheduler: input weights", "weights", inWeights)

	// Run filters first to reduce the number of subjects.
	// Any weights assigned to filtered out subjects are ignored.
	filteredRequest := p.runFilters(traceLog, request)
	traceLog.Info(
		"scheduler: finished filters",
		"remainingSubjects", filteredRequest.GetSubjects(),
	)

	// Run weighers on the filtered subjects.
	remainingWeights := make(map[string]float64, len(filteredRequest.GetSubjects()))
	for _, subject := range filteredRequest.GetSubjects() {
		remainingWeights[subject] = inWeights[subject]
	}
	stepWeights := p.runWeighers(traceLog, filteredRequest)
	outWeights := p.applyWeights(stepWeights, remainingWeights)
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
	return result, nil
}
