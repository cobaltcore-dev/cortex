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

type FilterWeigherPipelineDecision struct {
	// The original weights provided as input to the pipeline, from the request that cortex received.
	RawInWeights map[string]float64
	// The normalized input weights after applying the normalization function.
	NormalizedInWeights map[string]float64
	// The output weights after applying the weigher step activations and multipliers.
	AggregatedOutWeights map[string]float64
	// The hosts in order of preference, with the most preferred host first.
	OrderedHosts []string
}

type FilterWeigherPipeline[RequestType FilterWeigherPipelineRequest] interface {
	// Run the scheduling pipeline with the given request.
	Run(request RequestType) (FilterWeigherPipelineDecision, error)
}

// Pipeline of scheduler steps.
type filterWeigherPipeline[RequestType FilterWeigherPipelineRequest] struct {
	// The activation function to use when combining the
	// results of the scheduler steps.
	ActivationFunction
	// The order in which filters are applied, by their step name.
	filtersOrder []string
	// The filters by their name.
	filters map[string]Filter[RequestType]
	// The order in which weighers are applied, by their step name.
	weighersOrder []string
	// The weighers by their name.
	weighers map[string]Weigher[RequestType]
	// Multipliers to apply to weigher outputs.
	weighersMultipliers map[string]float64
	// Monitor to observe the pipeline.
	monitor FilterWeigherPipelineMonitor
}

// Create a new pipeline with filters and weighers contained in the configuration.
func InitNewFilterWeigherPipeline[RequestType FilterWeigherPipelineRequest](
	ctx context.Context,
	client client.Client,
	name string,
	supportedFilters map[string]func() Filter[RequestType],
	confedFilters []v1alpha1.FilterSpec,
	supportedWeighers map[string]func() Weigher[RequestType],
	confedWeighers []v1alpha1.WeigherSpec,
	monitor FilterWeigherPipelineMonitor,
) PipelineInitResult[FilterWeigherPipeline[RequestType]] {

	pipelineMonitor := monitor.SubPipeline(name)

	// Load all filters from the configuration.
	filtersByName := make(map[string]Filter[RequestType], len(confedFilters))
	filtersOrder := []string{}
	filterErrors := make(map[string]error)
	for _, filterConfig := range confedFilters {
		slog.Info("scheduler: configuring filter", "name", filterConfig.Name)
		slog.Info("supported:", "filters", maps.Keys(supportedFilters))
		makeFilter, ok := supportedFilters[filterConfig.Name]
		if !ok {
			slog.Error("scheduler: unsupported filter", "name", filterConfig.Name)
			filterErrors[filterConfig.Name] = errors.New("unsupported filter name: " + filterConfig.Name)
			continue
		}
		filter := makeFilter()
		filter = validateFilter(filter)
		filter = monitorFilter(filter, filterConfig.Name, pipelineMonitor)
		if err := filter.Init(ctx, client, filterConfig); err != nil {
			slog.Error("scheduler: failed to initialize filter", "name", filterConfig.Name, "error", err)
			filterErrors[filterConfig.Name] = errors.New("failed to initialize filter: " + err.Error())
			continue
		}
		filtersByName[filterConfig.Name] = filter
		filtersOrder = append(filtersOrder, filterConfig.Name)
		slog.Info("scheduler: added filter", "name", filterConfig.Name)
	}

	// Load all weighers from the configuration.
	weighersByName := make(map[string]Weigher[RequestType], len(confedWeighers))
	weighersMultipliers := make(map[string]float64, len(confedWeighers))
	weighersOrder := []string{}
	weigherErrors := make(map[string]error)
	for _, weigherConfig := range confedWeighers {
		slog.Info("scheduler: configuring weigher", "name", weigherConfig.Name)
		slog.Info("supported:", "weighers", maps.Keys(supportedWeighers))
		makeWeigher, ok := supportedWeighers[weigherConfig.Name]
		if !ok {
			slog.Error("scheduler: unsupported weigher", "name", weigherConfig.Name)
			weigherErrors[weigherConfig.Name] = errors.New("unsupported weigher name: " + weigherConfig.Name)
			continue
		}
		weigher := makeWeigher()
		// Validate that the weigher doesn't unexpectedly filter out hosts.
		weigher = validateWeigher(weigher)
		weigher = monitorWeigher(weigher, weigherConfig.Name, pipelineMonitor)
		if err := weigher.Init(ctx, client, weigherConfig); err != nil {
			slog.Error("scheduler: failed to initialize weigher", "name", weigherConfig.Name, "error", err)
			weigherErrors[weigherConfig.Name] = errors.New("failed to initialize weigher: " + err.Error())
			continue
		}
		weighersByName[weigherConfig.Name] = weigher
		weighersOrder = append(weighersOrder, weigherConfig.Name)
		if weigherConfig.Multiplier == nil {
			weighersMultipliers[weigherConfig.Name] = 1.0
		} else {
			weighersMultipliers[weigherConfig.Name] = *weigherConfig.Multiplier
		}
		slog.Info("scheduler: added weigher", "name", weigherConfig.Name)
	}

	return PipelineInitResult[FilterWeigherPipeline[RequestType]]{
		FilterErrors:  filterErrors,
		WeigherErrors: weigherErrors,
		Pipeline: &filterWeigherPipeline[RequestType]{
			filtersOrder:        filtersOrder,
			filters:             filtersByName,
			weighersOrder:       weighersOrder,
			weighers:            weighersByName,
			weighersMultipliers: weighersMultipliers,
			monitor:             pipelineMonitor,
		},
	}
}

// Execute filters and collect their activations by step name.
// During this process, the request is mutated to only include the
// remaining hosts.
func (p *filterWeigherPipeline[RequestType]) runFilters(
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
		// Mutate the request to only include the remaining hosts.
		// Assume the resulting request type is the same as the input type.
		filteredRequest = filteredRequest.FilterHosts(result.Activations).(RequestType)
	}
	return filteredRequest
}

// Execute weighers and collect their activations by step name.
func (p *filterWeigherPipeline[RequestType]) runWeighers(
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

// Apply an initial weight to the hosts.
//
// Context:
// Openstack schedulers may give us very large (positive/negative) weights such as
// -99,000 or 99,000 (Nova). We want to respect these values, but still adjust them
// to a meaningful value. If the scheduler really doesn't want us to run on a host, it
// should run a filter instead of setting a weight.
func (p *filterWeigherPipeline[RequestType]) normalizeInputWeights(weights map[string]float64) map[string]float64 {
	normalizedWeights := make(map[string]float64, len(weights))
	for hostname, weight := range weights {
		normalizedWeights[hostname] = math.Tanh(weight)
	}
	return normalizedWeights
}

// Apply the step weights to the input weights.
func (p *filterWeigherPipeline[RequestType]) applyWeights(
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
		multiplier, ok := p.weighersMultipliers[weigherName]
		if !ok {
			multiplier = 1.0
		}
		outWeights = p.Apply(outWeights, weigherActivations, multiplier)
	}
	return outWeights
}

// Sort the hosts by their weights.
func (s *filterWeigherPipeline[RequestType]) sortHostsByWeights(weights map[string]float64) []string {
	// Sort the hosts (keys) by their weights.
	hosts := slices.Collect(maps.Keys(weights))
	sort.Slice(hosts, func(i, j int) bool {
		return weights[hosts[i]] > weights[hosts[j]]
	})
	return hosts
}

// Evaluate the pipeline and return a list of hosts in order of preference.
func (p *filterWeigherPipeline[RequestType]) Run(request RequestType) (FilterWeigherPipelineDecision, error) {
	slogArgs := request.GetTraceLogArgs()
	slogArgsAny := make([]any, 0, len(slogArgs))
	for _, arg := range slogArgs {
		slogArgsAny = append(slogArgsAny, arg)
	}
	traceLog := slog.With(slogArgsAny...)

	hostsIn := request.GetHosts()
	traceLog.Info("scheduler: starting pipeline", "hosts", hostsIn)

	// Normalize the input weights so we can apply step weights meaningfully.
	inWeights := p.normalizeInputWeights(request.GetWeights())
	traceLog.Info("scheduler: input weights", "weights", inWeights)

	// Run filters first to reduce the number of hosts.
	// Any weights assigned to filtered out hosts are ignored.
	filteredRequest := p.runFilters(traceLog, request)
	traceLog.Info(
		"scheduler: finished filters",
		"remainingHosts", filteredRequest.GetHosts(),
	)

	// Run weighers on the filtered hosts.
	remainingWeights := make(map[string]float64, len(filteredRequest.GetHosts()))
	for _, host := range filteredRequest.GetHosts() {
		remainingWeights[host] = inWeights[host]
	}
	stepWeights := p.runWeighers(traceLog, filteredRequest)
	outWeights := p.applyWeights(stepWeights, remainingWeights)
	traceLog.Info("scheduler: output weights", "weights", outWeights)

	hosts := p.sortHostsByWeights(outWeights)
	traceLog.Info("scheduler: sorted hosts", "hosts", hosts)

	// Collect some metrics about the pipeline execution.
	go p.monitor.observePipelineResult(request, hosts)

	result := FilterWeigherPipelineDecision{
		RawInWeights:         request.GetWeights(),
		NormalizedInWeights:  inWeights,
		AggregatedOutWeights: outWeights,
		OrderedHosts:         hosts,
	}
	return result, nil
}
