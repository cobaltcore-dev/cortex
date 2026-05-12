// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/nova/plugins/filters"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/nova/plugins/weighers"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// The decision pipeline controller takes decision resources containing a
// placement request spec and runs the scheduling pipeline to make a decision.
// This decision is then written back to the decision resource status.
//
// Additionally, the controller watches for pipeline and step changes to
// reconfigure the pipelines as needed.
type FilterWeigherPipelineController struct {
	// Toolbox shared between all pipeline controllers.
	lib.BasePipelineController[lib.FilterWeigherPipeline[api.ExternalSchedulerRequest]]

	// Mutex to only allow one process at a time
	processMu sync.Mutex

	// Monitor to pass down to all pipelines.
	Monitor lib.FilterWeigherPipelineMonitor
	// Candidate gatherer to get all placement candidates if needed.
	gatherer CandidateGatherer
}

// The type of pipeline this controller manages.
func (c *FilterWeigherPipelineController) PipelineType() v1alpha1.PipelineType {
	return v1alpha1.PipelineTypeFilterWeigher
}

// Callback executed when kubernetes asks to reconcile a decision resource.
func (c *FilterWeigherPipelineController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	c.processMu.Lock()
	defer c.processMu.Unlock()

	decision := &v1alpha1.Decision{}
	if err := c.Get(ctx, req.NamespacedName, decision); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	old := decision.DeepCopy()
	if _, err := c.process(ctx, decision); err != nil {
		return ctrl.Result{}, err
	}
	patch := client.MergeFrom(old)
	if err := c.Status().Patch(ctx, decision, patch); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// Process the decision from the API. Should create and return the updated decision.
func (c *FilterWeigherPipelineController) ProcessNewDecisionFromAPI(ctx context.Context, decision *v1alpha1.Decision) error {
	// Early check before acquiring the mutex — no need to hold the lock just to fail.
	pipelineConf, ok := c.PipelineConfigs[decision.Spec.PipelineRef.Name]
	if !ok {
		return fmt.Errorf("pipeline %s not configured", decision.Spec.PipelineRef.Name)
	}

	c.processMu.Lock()
	defer c.processMu.Unlock()

	request, err := c.process(ctx, decision)
	if err != nil {
		meta.SetStatusCondition(&decision.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.DecisionConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "PipelineRunFailed",
			Message: "pipeline run failed: " + err.Error(),
		})
	} else {
		meta.SetStatusCondition(&decision.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.DecisionConditionReady,
			Status:  metav1.ConditionTrue,
			Reason:  "PipelineRunSucceeded",
			Message: "pipeline run succeeded",
		})
	}
	if pipelineConf.Spec.CreateHistory {
		c.upsertHistory(ctx, decision, request, err)
		if err == nil && decision.Status.Result != nil && request != nil {
			if decision.Status.Result.TargetHost != nil && isUserVMPlacement(decision.Spec.Intent) {
				c.recordCRAllocation(ctx, decision, *request)
			}
			if decision.Status.Result.TargetHost == nil {
				c.logNoHostFound(ctx, decision, *request)
			}
		}
	}
	return err
}

// isUserVMPlacement returns true for intents that represent actual VM
// placements from Nova. Returns false for Cortex-internal synthetic requests
// (failover and CR reservation scheduling), which must not update allocations.
func isUserVMPlacement(intent v1alpha1.SchedulingIntent) bool {
	switch intent {
	case api.ReserveForCommittedResourceIntent, api.ReserveForFailoverIntent:
		return false
	default:
		return true
	}
}

// recordCRAllocation writes the placed VM UUID into the matching Reservation
// Spec.CommittedResourceReservation.Allocations after a successful Nova placement.
// Best-effort: any failure is logged but never propagated to the caller.
func (c *FilterWeigherPipelineController) recordCRAllocation(ctx context.Context, decision *v1alpha1.Decision, request api.ExternalSchedulerRequest) {
	log := ctrl.LoggerFrom(ctx)

	instanceUUID := request.Spec.Data.InstanceUUID
	projectID := request.Context.ProjectID
	flavorName := request.Spec.Data.Flavor.Data.Name
	selectedHost := *decision.Status.Result.TargetHost

	// Resolve flavor → flavor group. Flavors not in any group are PAYG — nothing to do.
	fgClient := reservations.FlavorGroupKnowledgeClient{Client: c.Client}
	flavorGroups, err := fgClient.GetAllFlavorGroups(ctx, nil)
	if err != nil {
		log.Error(err, "CR allocation: failed to get flavor groups", "instanceUUID", instanceUUID)
		return
	}
	flavorGroupName, flavorInGroup, err := reservations.FindFlavorInGroups(flavorName, flavorGroups)
	if err != nil {
		log.V(1).Info("CR allocation: flavor not in any group, PAYG placement", "flavor", flavorName)
		return
	}

	// List all CR reservations and filter to candidates matching this placement.
	var reservationList v1alpha1.ReservationList
	if err := c.List(ctx, &reservationList,
		client.MatchingLabels{v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelCommittedResource},
	); err != nil {
		log.Error(err, "CR allocation: failed to list reservations", "instanceUUID", instanceUUID)
		return
	}

	var candidates []v1alpha1.Reservation
	for _, res := range reservationList.Items {
		cr := res.Spec.CommittedResourceReservation
		if cr == nil {
			continue
		}
		if res.Spec.TargetHost != selectedHost || cr.ProjectID != projectID || cr.ResourceGroup != flavorGroupName {
			continue
		}
		// Idempotency: if this VM UUID is already recorded, the work is done.
		if _, exists := cr.Allocations[instanceUUID]; exists {
			log.Info("CR allocation: VM UUID already in reservation, skipping",
				"instanceUUID", instanceUUID, "reservation", res.Name)
			return
		}
		candidates = append(candidates, res)
	}

	if len(candidates) == 0 {
		log.V(1).Info("CR allocation: no matching reservation slot, PAYG placement",
			"instanceUUID", instanceUUID, "host", selectedHost,
			"projectID", projectID, "flavorGroup", flavorGroupName)
		return
	}

	vmMemoryBytes := int64(flavorInGroup.MemoryMB) * 1024 * 1024 //nolint:gosec // flavor memory bounded by specs
	vmCPUs := int64(flavorInGroup.VCPUs)                         //nolint:gosec // VCPUs bounded by specs

	slotName := pickReservationSlot(candidates, vmMemoryBytes)
	if slotName == "" {
		log.Error(nil, "CR allocation: no reservation slot has sufficient remaining capacity",
			"instanceUUID", instanceUUID, "vmMemoryBytes", vmMemoryBytes,
			"host", selectedHost, "candidates", len(candidates))
		return
	}

	log.Info("CR allocation: writing VM UUID into reservation",
		"instanceUUID", instanceUUID, "reservation", slotName,
		"projectID", projectID, "flavorGroup", flavorGroupName, "host", selectedHost)

	vmResources := map[hv1.ResourceName]resource.Quantity{
		hv1.ResourceMemory: *resource.NewQuantity(vmMemoryBytes, resource.BinarySI),
		hv1.ResourceCPU:    *resource.NewQuantity(vmCPUs, resource.DecimalSI),
	}
	if retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &v1alpha1.Reservation{}
		if err := c.Get(ctx, client.ObjectKey{Name: slotName}, latest); err != nil {
			return err
		}
		if latest.Spec.CommittedResourceReservation.Allocations == nil {
			latest.Spec.CommittedResourceReservation.Allocations = make(map[string]v1alpha1.CommittedResourceAllocation)
		}
		latest.Spec.CommittedResourceReservation.Allocations[instanceUUID] = v1alpha1.CommittedResourceAllocation{
			CreationTimestamp: metav1.Now(),
			Resources:         vmResources,
		}
		return c.Update(ctx, latest)
	}); retryErr != nil {
		log.Error(retryErr, "CR allocation: failed to patch reservation",
			"reservation", slotName, "instanceUUID", instanceUUID)
		return
	}

	log.Info("CR allocation: done", "instanceUUID", instanceUUID, "reservation", slotName)
}

// pickReservationSlot selects the reservation slot with the least remaining
// memory that can still fully fit vmMemoryBytes.
// Tiebreaks: least remaining CPU, then reservation name (lexicographic).
// Returns the slot name, or "" if no slot fits.
func pickReservationSlot(candidates []v1alpha1.Reservation, vmMemoryBytes int64) string {
	bestName := ""
	var bestRemMem, bestRemCPU int64

	for _, res := range candidates {
		cr := res.Spec.CommittedResourceReservation

		totalMemQ := res.Spec.Resources[hv1.ResourceMemory]
		totalCPUQ := res.Spec.Resources[hv1.ResourceCPU]
		totalMem := totalMemQ.Value()
		totalCPU := totalCPUQ.Value()

		var usedMem, usedCPU int64
		for _, alloc := range cr.Allocations {
			memQ := alloc.Resources[hv1.ResourceMemory]
			cpuQ := alloc.Resources[hv1.ResourceCPU]
			usedMem += memQ.Value()
			usedCPU += cpuQ.Value()
		}

		remMem := max(totalMem-usedMem, 0)
		remCPU := max(totalCPU-usedCPU, 0)

		if remMem < vmMemoryBytes {
			continue // Slot doesn't have enough remaining capacity.
		}

		if bestName == "" ||
			remMem < bestRemMem ||
			(remMem == bestRemMem && remCPU < bestRemCPU) ||
			(remMem == bestRemMem && remCPU == bestRemCPU && res.Name < bestName) {
			bestName = res.Name
			bestRemMem = remMem
			bestRemCPU = remCPU
		}
	}

	return bestName
}

// logNoHostFound logs the context needed to classify no-host-found failures
// by CR coverage (cases A/B/C/D from ticket #345).
// TODO(#345): replace with CommittedResource CRD lookup and metric emission.
func (c *FilterWeigherPipelineController) logNoHostFound(ctx context.Context, decision *v1alpha1.Decision, request api.ExternalSchedulerRequest) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("no-host-found for nova scheduling request",
		"instanceUUID", request.Spec.Data.InstanceUUID,
		"projectID", request.Context.ProjectID,
		"flavor", request.Spec.Data.Flavor.Data.Name,
		"intent", decision.Spec.Intent,
		"pipeline", decision.Spec.PipelineRef.Name,
	)
}

func (c *FilterWeigherPipelineController) upsertHistory(ctx context.Context, decision *v1alpha1.Decision, request *api.ExternalSchedulerRequest, pipelineErr error) {
	log := ctrl.LoggerFrom(ctx)

	var az *string
	if request != nil {
		azStr := request.Spec.Data.AvailabilityZone
		az = &azStr
	}

	if upsertErr := c.HistoryManager.CreateOrUpdateHistory(ctx, decision, az, pipelineErr); upsertErr != nil {
		log.Error(upsertErr, "failed to create/update history")
	}
}

func (c *FilterWeigherPipelineController) process(ctx context.Context, decision *v1alpha1.Decision) (*api.ExternalSchedulerRequest, error) {
	log := ctrl.LoggerFrom(ctx)
	startedAt := time.Now() // So we can measure sync duration.

	pipeline, ok := c.Pipelines[decision.Spec.PipelineRef.Name]
	if !ok {
		log.Error(nil, "pipeline not found or not ready", "pipelineName", decision.Spec.PipelineRef.Name)
		return nil, errors.New("pipeline not found or not ready")
	}
	if decision.Spec.NovaRaw == nil {
		log.Error(nil, "skipping decision, no novaRaw spec defined")
		return nil, errors.New("no novaRaw spec defined")
	}
	var request api.ExternalSchedulerRequest
	if err := json.Unmarshal(decision.Spec.NovaRaw.Raw, &request); err != nil {
		log.Error(err, "failed to unmarshal novaRaw spec")
		return nil, err
	}

	if intent, err := request.GetIntent(); err != nil {
		log.Error(err, "failed to get intent from nova request, using Unknown")
		decision.Spec.Intent = v1alpha1.SchedulingIntentUnknown
	} else {
		decision.Spec.Intent = intent
	}

	// If necessary gather all placement candidates before filtering.
	// This will override the hosts and weights in the nova request.
	pipelineConf, ok := c.PipelineConfigs[decision.Spec.PipelineRef.Name]
	if !ok {
		log.Error(nil, "pipeline config not found", "pipelineName", decision.Spec.PipelineRef.Name)
		return nil, errors.New("pipeline config not found")
	}
	if pipelineConf.Spec.IgnorePreselection {
		log.Info("gathering all placement candidates before filtering")
		if err := c.gatherer.MutateWithAllCandidates(ctx, &request); err != nil {
			log.Error(err, "failed to gather all placement candidates")
			return nil, err
		}
		log.Info("gathered all placement candidates", "numHosts", len(request.Hosts))
	}

	result, err := pipeline.Run(request)
	if err != nil {
		log.Error(err, "failed to run pipeline")
		return nil, err
	}
	decision.Status.Result = &result
	meta.SetStatusCondition(&decision.Status.Conditions, metav1.Condition{
		Type:    v1alpha1.DecisionConditionReady,
		Status:  metav1.ConditionTrue,
		Reason:  "PipelineRunSucceeded",
		Message: "pipeline run succeeded",
	})
	log.Info("decision processed successfully", "duration", time.Since(startedAt))
	return &request, nil
}

// The base controller will delegate the pipeline creation down to this method.
func (c *FilterWeigherPipelineController) InitPipeline(
	ctx context.Context,
	p v1alpha1.Pipeline,
) lib.PipelineInitResult[lib.FilterWeigherPipeline[api.ExternalSchedulerRequest]] {

	return lib.InitNewFilterWeigherPipeline(
		ctx, c.Client, p.Name,
		filters.Index, p.Spec.Filters,
		weighers.Index, p.Spec.Weighers,
		c.Monitor,
	)
}

func (c *FilterWeigherPipelineController) SetupWithManager(mgr manager.Manager, mcl *multicluster.Client) error {
	c.Initializer = c
	c.SchedulingDomain = v1alpha1.SchedulingDomainNova
	c.HistoryManager = lib.HistoryClient{Client: mcl, Recorder: mcl.GetEventRecorder("cortex-nova-scheduler")}
	c.gatherer = &candidateGatherer{Client: mcl}
	if err := mgr.Add(manager.RunnableFunc(c.InitAllPipelines)); err != nil {
		return err
	}
	bldr := multicluster.BuildController(mcl, mgr)
	// Watch pipeline changes so that we can reconfigure pipelines as needed.
	bldr, err := bldr.WatchesMulticluster(
		&v1alpha1.Pipeline{},
		handler.Funcs{
			CreateFunc: c.HandlePipelineCreated,
			UpdateFunc: c.HandlePipelineUpdated,
			DeleteFunc: c.HandlePipelineDeleted,
		},
		predicate.NewPredicateFuncs(func(obj client.Object) bool {
			pipeline := obj.(*v1alpha1.Pipeline)
			// Only react to pipelines matching the scheduling domain.
			if pipeline.Spec.SchedulingDomain != v1alpha1.SchedulingDomainNova {
				return false
			}
			return pipeline.Spec.Type == c.PipelineType()
		}),
	)
	if err != nil {
		return err
	}
	// Watch knowledge changes so that we can reconfigure pipelines as needed.
	bldr, err = bldr.WatchesMulticluster(
		&v1alpha1.Knowledge{},
		handler.Funcs{
			CreateFunc: c.HandleKnowledgeCreated,
			UpdateFunc: c.HandleKnowledgeUpdated,
			DeleteFunc: c.HandleKnowledgeDeleted,
		},
		predicate.NewPredicateFuncs(func(obj client.Object) bool {
			knowledge := obj.(*v1alpha1.Knowledge)
			// Only react to knowledge matching the scheduling domain.
			return knowledge.Spec.SchedulingDomain == v1alpha1.SchedulingDomainNova
		}),
	)
	if err != nil {
		return err
	}
	// Watch hypervisor changes so the cache gets updated.
	bldr, err = bldr.WatchesMulticluster(&hv1.Hypervisor{}, handler.Funcs{})
	if err != nil {
		return err
	}
	// Watch reservation changes so the cache gets updated.
	bldr, err = bldr.WatchesMulticluster(&v1alpha1.Reservation{}, handler.Funcs{})
	if err != nil {
		return err
	}
	// Watch decision changes across all clusters.
	bldr, err = bldr.WatchesMulticluster(
		&v1alpha1.Decision{},
		&handler.EnqueueRequestForObject{},
		predicate.NewPredicateFuncs(func(obj client.Object) bool {
			decision := obj.(*v1alpha1.Decision)
			if decision.Spec.SchedulingDomain != v1alpha1.SchedulingDomainNova {
				return false
			}
			// Ignore already decided schedulings.
			if decision.Status.Result != nil {
				return false
			}
			return true
		}),
	)
	if err != nil {
		return err
	}
	return bldr.Named("cortex-nova-decisions").
		Complete(c)
}
