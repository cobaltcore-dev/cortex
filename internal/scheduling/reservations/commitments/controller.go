// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import (
	"context"
	"errors"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	schedulerdelegationapi "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/datasources/plugins/openstack/nova"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	"github.com/go-logr/logr"
)

// CommitmentReservationController reconciles commitment Reservation objects
type CommitmentReservationController struct {
	// Client for the kubernetes API.
	client.Client
	// Kubernetes scheme to use for the reservations.
	Scheme *runtime.Scheme
	// Configuration for the controller.
	Conf Config
	// Database connection for querying VM state from Knowledge cache.
	DB *db.DB
	// SchedulerClient for making scheduler API calls.
	SchedulerClient *reservations.SchedulerClient
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// Note: This controller only handles commitment reservations, as filtered by the predicate.
func (r *CommitmentReservationController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Fetch the reservation object first to check for creator request ID.
	var res v1alpha1.Reservation
	if err := r.Get(ctx, req.NamespacedName, &res); err != nil {
		// Ignore not-found errors, since they can't be fixed by an immediate requeue
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Use creator request ID from annotation for end-to-end traceability if available,
	// otherwise generate a new one for this reconcile loop.
	if creatorReq := res.Annotations[v1alpha1.AnnotationCreatorRequestID]; creatorReq != "" {
		ctx = WithGlobalRequestID(ctx, creatorReq)
	} else {
		ctx = WithNewGlobalRequestID(ctx)
	}
	logger := LoggerFromContext(ctx).WithValues("component", "controller", "reservation", req.Name)

	// filter for CR reservations
	resourceName := ""
	if res.Spec.CommittedResourceReservation != nil {
		resourceName = res.Spec.CommittedResourceReservation.ResourceName
	}
	if resourceName == "" {
		logger.Info("reservation has no resource name, skipping")
		old := res.DeepCopy()
		meta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.ReservationConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "MissingResourceName",
			Message: "reservation has no resource name",
		})
		patch := client.MergeFrom(old)
		if err := r.Status().Patch(ctx, &res, patch); err != nil {
			// Ignore not-found errors during background deletion
			if client.IgnoreNotFound(err) != nil {
				logger.Error(err, "failed to patch reservation status")
				return ctrl.Result{}, err
			}
			// Object was deleted, no need to continue
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, nil // Don't need to requeue.
	}

	if meta.IsStatusConditionTrue(res.Status.Conditions, v1alpha1.ReservationConditionReady) {
		logger.V(1).Info("reservation is active, verifying allocations")

		// Verify all allocations in Spec against actual VM state from database
		if err := r.reconcileAllocations(ctx, &res); err != nil {
			logger.Error(err, "failed to reconcile allocations")
			return ctrl.Result{}, err
		}

		// Requeue periodically to keep verifying allocations
		return ctrl.Result{RequeueAfter: r.Conf.RequeueIntervalActive}, nil
	}

	// TODO trigger re-placement of unused reservations over time

	// Check if this is a pre-allocated reservation with allocations
	if res.Spec.CommittedResourceReservation != nil &&
		len(res.Spec.CommittedResourceReservation.Allocations) > 0 &&
		res.Spec.TargetHost != "" {
		// mark as ready without calling the placement API
		logger.Info("detected pre-allocated reservation",
			"targetHost", res.Spec.TargetHost,
			"allocatedVMs", len(res.Spec.CommittedResourceReservation.Allocations))

		old := res.DeepCopy()
		res.Status.Host = res.Spec.TargetHost
		meta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.ReservationConditionReady,
			Status:  metav1.ConditionTrue,
			Reason:  "PreAllocated",
			Message: "reservation pre-allocated with VM allocations",
		})
		patch := client.MergeFrom(old)
		if err := r.Status().Patch(ctx, &res, patch); err != nil {
			// Ignore not-found errors during background deletion
			if client.IgnoreNotFound(err) != nil {
				logger.Error(err, "failed to patch pre-allocated reservation status")
				return ctrl.Result{}, err
			}
			// Object was deleted, no need to continue
			return ctrl.Result{}, nil
		}

		logger.Info("marked pre-allocated reservation as ready", "host", res.Status.Host)
		// Requeue immediately to run verification in next reconcile loop
		return ctrl.Result{Requeue: true}, nil
	}

	// Sync Spec values to Status fields for non-pre-allocated reservations
	// This ensures the observed state reflects the desired state from Spec
	// When TargetHost is set in Spec but not synced to Status, this means
	// the scheduler found a host and we need to mark the reservation as ready.
	if res.Spec.TargetHost != "" && res.Status.Host != res.Spec.TargetHost {
		old := res.DeepCopy()
		res.Status.Host = res.Spec.TargetHost
		meta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.ReservationConditionReady,
			Status:  metav1.ConditionTrue,
			Reason:  "ReservationActive",
			Message: "reservation is successfully scheduled",
		})
		patch := client.MergeFrom(old)
		if err := r.Status().Patch(ctx, &res, patch); err != nil {
			// Ignore not-found errors during background deletion
			if client.IgnoreNotFound(err) != nil {
				logger.Error(err, "failed to sync spec to status")
				return ctrl.Result{}, err
			}
			// Object was deleted, no need to continue
			return ctrl.Result{}, nil
		}
		logger.Info("synced spec to status and marked ready", "host", res.Status.Host)
		// Return and let next reconcile handle allocation verification
		return ctrl.Result{}, nil
	}

	// Get project ID from CommittedResourceReservation spec if available.
	projectID := ""
	if res.Spec.CommittedResourceReservation != nil {
		projectID = res.Spec.CommittedResourceReservation.ProjectID
	}

	// Get AvailabilityZone from reservation if available
	availabilityZone := ""
	if res.Spec.AvailabilityZone != "" {
		availabilityZone = res.Spec.AvailabilityZone
	}

	// Get flavor details from flavor group knowledge CRD
	knowledge := &reservations.FlavorGroupKnowledgeClient{Client: r.Client}
	flavorGroups, err := knowledge.GetAllFlavorGroups(ctx, nil)
	if err != nil {
		logger.Info("flavor knowledge not ready, requeueing",
			"resourceName", resourceName,
			"error", err)
		return ctrl.Result{RequeueAfter: r.Conf.RequeueIntervalRetry}, nil
	}

	// Search for the flavor across all flavor groups
	// Also capture the flavor group name for pipeline selection
	var flavorDetails *compute.FlavorInGroup
	var flavorGroupName string
	for groupName, fg := range flavorGroups {
		for _, flavor := range fg.Flavors {
			if flavor.Name == resourceName {
				flavorDetails = &flavor
				flavorGroupName = groupName
				break
			}
		}
		if flavorDetails != nil {
			break
		}
	}

	// Check if flavor was found
	if flavorDetails == nil {
		logger.Error(errors.New("flavor not found"), "flavor not found in any flavor group",
			"resourceName", resourceName)
		return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
	}

	// Get hypervisors from the cluster
	var hypervisorList hv1.HypervisorList
	if err := r.List(ctx, &hypervisorList); err != nil {
		logger.Error(err, "failed to list hypervisors")
		return ctrl.Result{}, err
	}

	// Build list of eligible hosts
	eligibleHosts := make([]schedulerdelegationapi.ExternalSchedulerHost, 0, len(hypervisorList.Items))
	for _, hv := range hypervisorList.Items {
		eligibleHosts = append(eligibleHosts, schedulerdelegationapi.ExternalSchedulerHost{
			ComputeHost: hv.Name,
		})
	}

	if len(eligibleHosts) == 0 {
		logger.Info("no hypervisors available for scheduling")
		old := res.DeepCopy()
		meta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.ReservationConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "NoHostsAvailable",
			Message: "no hypervisors available for scheduling",
		})
		patch := client.MergeFrom(old)
		if err := r.Status().Patch(ctx, &res, patch); err != nil {
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
		return ctrl.Result{RequeueAfter: r.Conf.RequeueIntervalRetry}, nil
	}

	// Select appropriate pipeline based on flavor group
	pipelineName := r.getPipelineForFlavorGroup(flavorGroupName, logger)
	logger.Info("selected pipeline for CR reservation",
		"flavorName", resourceName,
		"flavorGroup", flavorGroupName,
		"pipeline", pipelineName)

	// Use the SchedulerClient to schedule the reservation
	scheduleReq := reservations.ScheduleReservationRequest{
		InstanceUUID:     res.Name,
		ProjectID:        projectID,
		FlavorName:       flavorDetails.Name,
		FlavorExtraSpecs: flavorDetails.ExtraSpecs,
		MemoryMB:         flavorDetails.MemoryMB,
		VCPUs:            flavorDetails.VCPUs,
		EligibleHosts:    eligibleHosts,
		Pipeline:         pipelineName,
		AvailabilityZone: availabilityZone,
	}

	scheduleResp, err := r.SchedulerClient.ScheduleReservation(ctx, scheduleReq)
	if err != nil {
		logger.Error(err, "failed to schedule reservation")
		return ctrl.Result{}, err
	}

	if len(scheduleResp.Hosts) == 0 {
		logger.Info("no hosts found for reservation", "reservation", res.Name, "flavorName", resourceName)
		old := res.DeepCopy()
		meta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.ReservationConditionReady,
			Status:  metav1.ConditionFalse,
			Reason:  "NoHostsFound",
			Message: "no hosts found for reservation",
		})
		patch := client.MergeFrom(old)
		if err := r.Status().Patch(ctx, &res, patch); err != nil {
			// Ignore not-found errors during background deletion
			if client.IgnoreNotFound(err) != nil {
				logger.Error(err, "failed to patch reservation status")
				return ctrl.Result{}, err
			}
			// Object was deleted, no need to continue
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, nil // No need to requeue, we didn't find a host.
	}

	// Update the reservation Spec with the found host (idx 0)
	// Only update Spec here - the Status will be synced in the next reconcile cycle
	// This avoids race conditions from doing two patches in one reconcile
	host := scheduleResp.Hosts[0]
	logger.Info("found host for reservation", "host", host)

	old := res.DeepCopy()
	res.Spec.TargetHost = host
	if err := r.Patch(ctx, &res, client.MergeFrom(old)); err != nil {
		// Ignore not-found errors during background deletion
		if client.IgnoreNotFound(err) != nil {
			logger.Error(err, "failed to patch reservation spec")
			return ctrl.Result{}, err
		}
		// Object was deleted, no need to continue
		return ctrl.Result{}, nil
	}

	// The Spec patch will trigger a re-reconcile, which will sync Status in the
	// "Sync Spec values to Status" section above
	return ctrl.Result{}, nil
}

// reconcileAllocations verifies all allocations in Spec against actual Nova VM state.
// It updates Status.Allocations based on the actual host location of each VM.
func (r *CommitmentReservationController) reconcileAllocations(ctx context.Context, res *v1alpha1.Reservation) error {
	logger := LoggerFromContext(ctx).WithValues("component", "controller")

	// Skip if no CommittedResourceReservation
	if res.Spec.CommittedResourceReservation == nil {
		return nil
	}

	// TODO trigger migrations of unused reservations (to PAYG VMs)

	// Skip if no allocations to verify
	if len(res.Spec.CommittedResourceReservation.Allocations) == 0 {
		logger.V(1).Info("no allocations to verify", "reservation", res.Name)
		return nil
	}

	// Query all VMs for this project from the database
	projectID := res.Spec.CommittedResourceReservation.ProjectID
	serverMap, err := r.listServersByProjectID(ctx, projectID)
	if err != nil {
		return fmt.Errorf("failed to list servers for project %s: %w", projectID, err)
	}

	// initialize
	if res.Status.CommittedResourceReservation == nil {
		res.Status.CommittedResourceReservation = &v1alpha1.CommittedResourceReservationStatus{}
	}

	// Build new Status.Allocations map based on actual VM locations
	newStatusAllocations := make(map[string]string)

	for vmUUID := range res.Spec.CommittedResourceReservation.Allocations {
		server, exists := serverMap[vmUUID]
		if exists {
			// VM found - record its actual host location
			actualHost := server.OSEXTSRVATTRHost
			newStatusAllocations[vmUUID] = actualHost

			logger.V(1).Info("verified VM allocation",
				"vm", vmUUID,
				"actualHost", actualHost,
				"expectedHost", res.Status.Host)
		} else {
			// VM not found in database
			logger.Info("VM not found in database",
				"vm", vmUUID,
				"reservation", res.Name,
				"projectID", projectID)

			// TODO handle entering and leave event
		}
	}

	// Patch the reservation status
	old := res.DeepCopy()

	// Update Status.Allocations
	res.Status.CommittedResourceReservation.Allocations = newStatusAllocations

	patch := client.MergeFrom(old)
	if err := r.Status().Patch(ctx, res, patch); err != nil {
		// Ignore not-found errors during background deletion
		if client.IgnoreNotFound(err) == nil {
			// Object was deleted, no need to continue
			return nil
		}
		return fmt.Errorf("failed to patch reservation status: %w", err)
	}

	logger.V(1).Info("reconciled allocations",
		"specAllocations", len(res.Spec.CommittedResourceReservation.Allocations),
		"statusAllocations", len(newStatusAllocations))

	return nil
}

// getPipelineForFlavorGroup returns the pipeline name for a given flavor group.
func (r *CommitmentReservationController) getPipelineForFlavorGroup(flavorGroupName string, logger logr.Logger) string {
	// Try exact match first (e.g., "2152" -> "kvm-cr-hana")
	if pipeline, ok := r.Conf.FlavorGroupPipelines[flavorGroupName]; ok {
		return pipeline
	}

	// Try wildcard fallback
	if pipeline, ok := r.Conf.FlavorGroupPipelines["*"]; ok {
		return pipeline
	}

	logger.Info("no pipeline configured for flavor group, using default", "flavorGroup", flavorGroupName, "defaultPipeline", r.Conf.PipelineDefault)
	return r.Conf.PipelineDefault
}

// Init initializes the reconciler with required clients and DB connection.
func (r *CommitmentReservationController) Init(ctx context.Context, client client.Client, conf Config) error {
	// Initialize database connection if DatabaseSecretRef is provided.
	if conf.DatabaseSecretRef != nil {
		var err error
		r.DB, err = db.Connector{Client: client}.FromSecretRef(ctx, *conf.DatabaseSecretRef)
		if err != nil {
			return fmt.Errorf("failed to initialize database connection: %w", err)
		}
		logf.FromContext(ctx).Info("database connection initialized for commitment reservation controller")
	}

	// Initialize scheduler client
	r.SchedulerClient = reservations.NewSchedulerClient(conf.SchedulerURL)
	logf.FromContext(ctx).Info("scheduler client initialized for commitment reservation controller", "url", conf.SchedulerURL)

	return nil
}

func (r *CommitmentReservationController) listServersByProjectID(ctx context.Context, projectID string) (map[string]*nova.Server, error) {
	if r.DB == nil {
		return nil, errors.New("database connection not initialized")
	}

	logger := LoggerFromContext(ctx).WithValues("component", "controller")

	// Query servers from the database cache.
	var servers []nova.Server
	_, err := r.DB.Select(&servers,
		"SELECT * FROM openstack_servers WHERE tenant_id = $1",
		projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to query servers from database: %w", err)
	}

	logger.V(1).Info("queried servers from database",
		"projectID", projectID,
		"serverCount", len(servers))

	// Build lookup map for O(1) access by VM UUID.
	serverMap := make(map[string]*nova.Server, len(servers))
	for i := range servers {
		serverMap[servers[i].ID] = &servers[i]
	}

	return serverMap, nil
}

// commitmentReservationPredicate filters to only watch commitment reservations.
// This controller explicitly handles only commitment reservations (CR reservations),
// while failover reservations are handled by the separate failover controller.
var commitmentReservationPredicate = predicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool {
		res, ok := e.Object.(*v1alpha1.Reservation)
		if !ok {
			return false
		}
		return res.Spec.Type == v1alpha1.ReservationTypeCommittedResource
	},
	UpdateFunc: func(e event.UpdateEvent) bool {
		res, ok := e.ObjectNew.(*v1alpha1.Reservation)
		if !ok {
			return false
		}
		return res.Spec.Type == v1alpha1.ReservationTypeCommittedResource
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		res, ok := e.Object.(*v1alpha1.Reservation)
		if !ok {
			return false
		}
		return res.Spec.Type == v1alpha1.ReservationTypeCommittedResource
	},
	GenericFunc: func(e event.GenericEvent) bool {
		res, ok := e.Object.(*v1alpha1.Reservation)
		if !ok {
			return false
		}
		return res.Spec.Type == v1alpha1.ReservationTypeCommittedResource
	},
}

// SetupWithManager sets up the controller with the Manager.
func (r *CommitmentReservationController) SetupWithManager(mgr ctrl.Manager, mcl *multicluster.Client) error {
	if err := mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		if err := r.Init(ctx, mgr.GetClient(), r.Conf); err != nil {
			return err
		}
		return nil
	})); err != nil {
		return err
	}
	return multicluster.BuildController(mcl, mgr).
		For(&v1alpha1.Reservation{}).
		WithEventFilter(commitmentReservationPredicate).
		Named("commitment-reservation").
		WithOptions(controller.Options{
			// We want to process reservations one at a time to avoid overbooking.
			MaxConcurrentReconciles: 1,
		}).
		Complete(r)
}
