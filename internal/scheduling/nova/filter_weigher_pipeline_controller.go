// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/nova/crs"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/nova/plugins/filters"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/nova/plugins/weighers"
	resv "github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	cacheoverlay "github.com/cobaltcore-dev/cortex/pkg/cache"
	"github.com/cobaltcore-dev/cortex/pkg/conf"
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

	// Mutex to only allow one process at a time.
	// Read-only runs (opts.ReadOnly == true) acquire a read lock; write runs acquire the full lock.
	processMu sync.RWMutex

	// FeatureGates holds feature flags for this controller.
	FeatureGates FeatureGates
	// Monitor to pass down to all pipelines.
	Monitor lib.FilterWeigherPipelineMonitor
	// Candidate gatherer to get all placement candidates if needed.
	gatherer CandidateGatherer

	// vmHandler maps processed decisions into vm-operator VirtualMachine resources.
	vmHandler *VirtualMachineHandler

	// CRRecorder receives scheduling events and updates CR reservations and metrics.
	CRRecorder       crs.Recorder
	InFlightConfig   InFlightConfig
	HTTPConfig       HTTPAPIConfig
	inflightProducer *inflightProducer
}

// The type of pipeline this controller manages.
func (c *FilterWeigherPipelineController) PipelineType() v1alpha1.PipelineType {
	return v1alpha1.PipelineTypeFilterWeigher
}

// Callback executed when kubernetes asks to reconcile a decision resource.
func (c *FilterWeigherPipelineController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Peek at the decision before acquiring the lock so we can choose the right lock type.
	// Read-only runs can proceed concurrently; write runs need the exclusive lock.
	decision := &v1alpha1.Decision{}
	if err := c.Get(ctx, req.NamespacedName, decision); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if c.peekReadOnly(decision) {
		c.processMu.RLock()
		defer c.processMu.RUnlock()
	} else {
		c.processMu.Lock()
		defer c.processMu.Unlock()
		// Re-fetch after acquiring the exclusive lock to see consistent state.
		if err := c.Get(ctx, req.NamespacedName, decision); err != nil {
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
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
	// Read-only runs share the cached decision state; no re-fetch needed because they
	// don't observe writes from concurrent exclusive-lock runs.
	if c.peekReadOnly(decision) {
		c.processMu.RLock()
		defer c.processMu.RUnlock()
	} else {
		c.processMu.Lock()
		defer c.processMu.Unlock()
	}

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
	if err == nil && decision.Status.Result != nil && request != nil && c.FeatureGates.CommittedResourceTracking {
		if decision.Status.Result.TargetHost != nil {
			if request.Options.SkipCommittedResourceTracking {
				ctrl.LoggerFrom(ctx).V(1).Info("CR allocation tracking skipped (SkipCommittedResourceTracking=true)",
					"instanceUUID", request.Spec.Data.InstanceUUID,
					"targetHost", *decision.Status.Result.TargetHost,
					"intent", decision.Spec.Intent,
				)
			} else {
				c.CRRecorder.RecordPlacement(ctx, decision, *request)
			}
		}
		if decision.Status.Result.TargetHost == nil {
			c.CRRecorder.RecordNoHostFound(ctx, decision, *request)
		}
	}
	return err
}

func (c *FilterWeigherPipelineController) process(ctx context.Context, decision *v1alpha1.Decision) (*api.ExternalSchedulerRequest, error) {
	log := ctrl.LoggerFrom(ctx)
	startedAt := time.Now() // So we can measure sync duration.

	pipeline, ok := c.GetPipeline(decision.Spec.PipelineRef.Name)
	if !ok {
		log.Error(nil, "pipeline not found or not ready", "pipelineName", decision.Spec.PipelineRef.Name)
		return nil, fmt.Errorf("pipeline not found or not ready: %q", decision.Spec.PipelineRef.Name)
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
	originalRequest := request
	intent := decision.Spec.Intent
	createInflights := c.inflightProducer != nil && request.IsKVMRequest() &&
		!request.Options.ReadOnly && !request.Options.SkipInflight &&
		slices.Contains(inflightIntents, intent)

	// If necessary gather all placement candidates before filtering.
	// This will override the hosts and weights in the nova request.
	pipelineConf, ok := c.GetPipelineConfig(decision.Spec.PipelineRef.Name)
	if !ok {
		log.Error(nil, "pipeline config not found", "pipelineName", decision.Spec.PipelineRef.Name)
		return &request, fmt.Errorf("pipeline config not found: %q", decision.Spec.PipelineRef.Name)
	}
	if pipelineConf.Spec.IgnorePreselection {
		log.V(1).Info("gathering all placement candidates before filtering")
		if err := c.gatherer.MutateWithAllCandidates(ctx, &request); err != nil {
			log.Error(err, "failed to gather all placement candidates")
			return &request, err
		}
		log.V(1).Info("gathered all placement candidates", "numHosts", len(request.Hosts))
	}

	// The pipeline scores the flavor exactly as Nova sent it; resize no longer
	// widens the request to an envelope of the current and requested flavor
	// (ADR 8, UC2). The source hold for a KVM resize is resolved separately
	// below, before the VM upsert overwrites Spec.Resources.
	result, err := pipeline.Run(request)
	if err != nil {
		decision.Status.Explanation = lib.GenerateExplanation(nil, err)
		log.Error(err, "failed to run pipeline")
		return &request, err
	}
	c.finalizeHosts(&result, originalRequest, intent, createInflights)
	decision.Status.Result = &result
	decision.Status.Explanation = lib.GenerateExplanation(&result, nil)

	// Resolve the in-flight holds before the VM upsert: resize source resolution
	// reads the VM CRD Spec.Resources (the old size), which Upsert overwrites
	// with the new flavor.
	var holds []hold
	if createInflights && len(result.OrderedHosts) > 0 {
		resources, err := c.inflightProducer.requiredResources(request)
		if err != nil {
			return &request, err
		}
		if holds, err = c.inflightProducer.holds(ctx, request, intent, resources, result.OrderedHosts); err != nil {
			return &request, err
		}
	}

	// Create the VirtualMachine resource first so in-flight reservations created
	// below can reference it as owner inline (no orphan window). We log the
	// specific reason whenever we skip creation so it can be traced.
	instanceUUID := request.Spec.Data.InstanceUUID
	switch {
	case request.Options.ReadOnly:
		log.V(1).Info("skipping virtual machine creation, request is read-only", "instanceUUID", instanceUUID)
	case !c.FeatureGates.VirtualMachineCreation:
		log.V(1).Info("skipping virtual machine creation, feature gate disabled", "instanceUUID", instanceUUID)
	case !slices.Contains(api.NovaIntents, decision.Spec.Intent):
		log.V(1).Info("skipping virtual machine creation, intent not triggered by Nova", "instanceUUID", instanceUUID, "intent", decision.Spec.Intent)
	case !request.IsKVMRequest():
		log.V(1).Info("skipping virtual machine creation, unsupported hypervisor type", "instanceUUID", instanceUUID)
	default:
		vmUpsertErr := c.vmHandler.Upsert(ctx, decision, request)
		if (c.FeatureGates.VirtualMachineCreationMandatory || createInflights) && vmUpsertErr != nil {
			log.Error(vmUpsertErr, "failed to upsert virtual machine resource and it is mandatory", "instanceUUID", instanceUUID)
			return &request, vmUpsertErr
		}
		if vmUpsertErr != nil {
			// Non-fatal: the placement decision itself succeeded.
			log.Error(vmUpsertErr, "failed to upsert virtual machine resource", "instanceUUID", instanceUUID)
		}
	}

	// Block the returned hosts before handing control back to Nova. The VM CRD
	// now exists (upsert above), so create attaches it as owner inline. The
	// holds were resolved before the upsert (resize source reads the old size).
	if createInflights && len(holds) > 0 {
		if _, err = c.inflightProducer.create(ctx, originalRequest, intent, holds); err != nil {
			return &request, err
		}
	}

	meta.SetStatusCondition(&decision.Status.Conditions, metav1.Condition{
		Type:    v1alpha1.DecisionConditionReady,
		Status:  metav1.ConditionTrue,
		Reason:  "PipelineRunSucceeded",
		Message: "pipeline run succeeded",
	})
	log.Info("decision processed successfully", "duration", time.Since(startedAt))
	return &request, nil
}

// finalizeHosts applies the response policy to the pipeline result, so the
// returned hosts are exactly the ones in-flight reservations are created for.
func (c *FilterWeigherPipelineController) finalizeHosts(result *v1alpha1.DecisionResult, request api.ExternalSchedulerRequest, intent v1alpha1.SchedulingIntent, createInflights bool) {
	hosts := result.OrderedHosts
	if c.HTTPConfig.NovaLimitHostsToRequest {
		hosts = limitHostsToRequest(request, hosts)
	}
	// This is a hack to address the problem that Nova only uses the first host
	// in hosts for evacuation requests. Shuffle the first k hosts to ensure that
	// we do not get stuck on a single host.
	if intent == api.EvacuateIntent {
		hosts = shuffleTopHosts(hosts, c.HTTPConfig.EvacuationShuffleK)
	}
	if createInflights {
		limit := 1
		if intent == api.CreateIntent {
			limit = cmp.Or(c.InFlightConfig.MaxCandidates, 3)
			if request.Options.MaxCandidates > 0 {
				limit = min(limit, request.Options.MaxCandidates)
			}
		}
		hosts = hosts[:min(limit, len(hosts))]
	}
	// Nova uses only the first host for evacuations (ADR 8, Pattern 3); with
	// in-flight reservations enabled, return just the randomly chosen one.
	if c.inflightProducer != nil && intent == api.EvacuateIntent {
		hosts = hosts[:min(1, len(hosts))]
	}
	result.OrderedHosts = hosts
	result.TargetHost = nil
	if len(hosts) > 0 {
		result.TargetHost = &hosts[0]
	}
	for host := range result.AggregatedOutWeights {
		if !slices.Contains(hosts, host) {
			delete(result.AggregatedOutWeights, host)
		}
	}
}

// peekReadOnly determines whether a decision should use a read lock instead of
// the exclusive write lock. Defaults to false (exclusive) on any parse error.
func (c *FilterWeigherPipelineController) peekReadOnly(decision *v1alpha1.Decision) bool {
	if decision.Spec.NovaRaw == nil {
		return false
	}
	var request api.ExternalSchedulerRequest
	if err := json.Unmarshal(decision.Spec.NovaRaw.Raw, &request); err != nil {
		return false
	}
	return request.Options.ReadOnly
}

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
	if c.FeatureGates.InFlightReservations {
		if c.InFlightConfig.TTLSeconds <= 0 || c.InFlightConfig.MaxCandidates < 0 {
			return errors.New("in-flight reservations require positive TTL seconds and nonnegative max candidates")
		}
		if !c.FeatureGates.VirtualMachineCreation {
			return errors.New("in-flight reservations require virtual machine creation")
		}
		if c.HTTPConfig.forcedDestinationEnabled() {
			return errors.New("in-flight reservations require forcedDestinationEnabled=false")
		}
		cacheConfig := conf.GetConfigOrDie[cacheoverlay.RootConfig]().Cache
		if !cacheConfig.Enabled || !slices.Contains(cacheConfig.GVKs, "cortex.cloud/v1alpha1/Reservation") ||
			!slices.Contains(cacheConfig.GVKs, "cobaltcore.cloud/v1alpha1/VirtualMachine") {
			return errors.New("in-flight reservations require Reservation and VirtualMachine write-through overlays")
		}
		resizeUnknownSource := cmp.Or(c.InFlightConfig.ResizeUnknownSource, resizeUnknownSourceFail)
		if resizeUnknownSource != resizeUnknownSourceFail && resizeUnknownSource != resizeUnknownSourceSkip {
			return fmt.Errorf("inFlightResizeUnknownSource must be %q or %q", resizeUnknownSourceFail, resizeUnknownSourceSkip)
		}
		producer := &inflightProducer{
			Client:              mcl,
			OwnerResolver:       vmOwnerResolver{Client: mcl},
			TTL:                 time.Duration(c.InFlightConfig.TTLSeconds) * time.Second,
			ResizeUnknownSource: resizeUnknownSource,
		}
		if c.InFlightConfig.ResizeServerDBFallback {
			if c.InFlightConfig.DatasourceName == "" {
				return errors.New("inFlightResizeServerDBFallback requires inFlightDatasourceName")
			}
			producer.VMSource = resv.NewPostgresVMSource(mcl, c.InFlightConfig.DatasourceName)
		}
		c.inflightProducer = producer
	}
	c.Initializer = c
	c.SchedulingDomain = v1alpha1.SchedulingDomainNova
	c.gatherer = &candidateGatherer{Client: mcl}
	c.vmHandler = &VirtualMachineHandler{Client: mcl}
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
	// Watch committed resource changes so the no-host-found classifier can read them.
	bldr, err = bldr.WatchesMulticluster(&v1alpha1.CommittedResource{}, handler.Funcs{})
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
