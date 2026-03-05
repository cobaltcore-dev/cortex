// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
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
	corev1 "k8s.io/api/core/v1"
)

const (
	// RequeueIntervalActive is the interval for requeueing active reservations for verification.
	RequeueIntervalActive = 5 * time.Minute
	// RequeueIntervalRetry is the interval for requeueing when retrying after knowledge is not ready.
	RequeueIntervalRetry = 1 * time.Minute
)

// Endpoints for the reservations operator.
type EndpointsConfig struct {
	// The nova external scheduler endpoint.
	NovaExternalScheduler string `json:"novaExternalScheduler"`
}

type Config struct {
	// The endpoint where to find the nova external scheduler endpoint.
	Endpoints EndpointsConfig `json:"endpoints"`

	// Secret ref to SSO credentials stored in a k8s secret, if applicable.
	SSOSecretRef *corev1.SecretReference `json:"ssoSecretRef"`

	// Secret ref to keystone credentials stored in a k8s secret.
	KeystoneSecretRef corev1.SecretReference `json:"keystoneSecretRef"`

	// Secret ref to the database credentials for querying VM state.
	DatabaseSecretRef *corev1.SecretReference `json:"databaseSecretRef,omitempty"`
}

// ReservationReconciler reconciles a Reservation object
type ReservationReconciler struct {
	// Client for the kubernetes API.
	client.Client
	// Kubernetes scheme to use for the reservations.
	Scheme *runtime.Scheme
	// Configuration for the controller.
	Conf Config
	// Database connection for querying VM state from Knowledge cache.
	DB *db.DB
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// Note: Failover reservations are filtered out at the watch level by the predicate
// in SetupWithManager, so this function only handles non-failover reservations.
func (r *ReservationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	// Fetch the reservation object.
	var res v1alpha1.Reservation
	if err := r.Get(ctx, req.NamespacedName, &res); err != nil {
		// Ignore not-found errors, since they can't be fixed by an immediate requeue
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if meta.IsStatusConditionTrue(res.Status.Conditions, v1alpha1.ReservationConditionReady) {
		log.Info("reservation is active, verifying allocations", "reservation", req.Name)

		// Verify all allocations in Spec against actual VM state from database
		if err := r.reconcileAllocations(ctx, &res); err != nil {
			log.Error(err, "failed to reconcile allocations")
			return ctrl.Result{}, err
		}

		// Requeue periodically to keep verifying allocations
		return ctrl.Result{RequeueAfter: RequeueIntervalActive}, nil
	}

	// TODO trigger re-placement of unused reservations over time

	// Check if this is a pre-allocated reservation with allocations
	if res.Spec.CommittedResourceReservation != nil &&
		len(res.Spec.CommittedResourceReservation.Allocations) > 0 &&
		res.Spec.TargetHost != "" {
		// mark as ready without calling the placement API
		log.Info("detected pre-allocated reservation",
			"reservation", req.Name,
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
				log.Error(err, "failed to patch pre-allocated reservation status")
				return ctrl.Result{}, err
			}
			// Object was deleted, no need to continue
			return ctrl.Result{}, nil
		}

		log.Info("marked pre-allocated reservation as ready", "reservation", req.Name, "host", res.Status.Host)
		// Requeue immediately to run verification in next reconcile loop
		return ctrl.Result{Requeue: true}, nil
	}

	// Sync Spec values to Status fields for non-pre-allocated reservations
	// This ensures the observed state reflects the desired state from Spec
	needsStatusUpdate := false
	if res.Spec.TargetHost != "" && res.Status.Host != res.Spec.TargetHost {
		res.Status.Host = res.Spec.TargetHost
		needsStatusUpdate = true
	}
	if needsStatusUpdate {
		old := res.DeepCopy()
		patch := client.MergeFrom(old)
		if err := r.Status().Patch(ctx, &res, patch); err != nil {
			// Ignore not-found errors during background deletion
			if client.IgnoreNotFound(err) != nil {
				log.Error(err, "failed to sync spec to status")
				return ctrl.Result{}, err
			}
			// Object was deleted, no need to continue
			return ctrl.Result{}, nil
		}
		log.Info("synced spec to status", "reservation", req.Name, "host", res.Status.Host)
	}

	// filter for CR reservations
	resourceName := ""
	if res.Spec.CommittedResourceReservation != nil {
		resourceName = res.Spec.CommittedResourceReservation.ResourceName
	}
	if resourceName == "" {
		log.Info("reservation has no resource name, skipping", "reservation", req.Name)
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
				log.Error(err, "failed to patch reservation status")
				return ctrl.Result{}, err
			}
			// Object was deleted, no need to continue
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, nil // Don't need to requeue.
	}

	// Convert resource.Quantity to integers for the API
	var memoryMB uint64
	if memory, ok := res.Spec.Resources["memory"]; ok {
		memoryValue := memory.ScaledValue(resource.Mega)
		if memoryValue < 0 {
			return ctrl.Result{}, fmt.Errorf("invalid memory value: %d", memoryValue)
		}
		memoryMB = uint64(memoryValue)
	}

	var cpu uint64
	if cpuQuantity, ok := res.Spec.Resources["cpu"]; ok {
		cpuValue := cpuQuantity.ScaledValue(resource.Milli)
		if cpuValue < 0 {
			return ctrl.Result{}, fmt.Errorf("invalid cpu value: %d", cpuValue)
		}
		cpu = uint64(cpuValue)
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
		log.Info("flavor knowledge not ready, requeueing",
			"resourceName", resourceName,
			"error", err)
		return ctrl.Result{RequeueAfter: RequeueIntervalRetry}, nil
	}

	// Search for the flavor across all flavor groups
	var flavorDetails *compute.FlavorInGroup
	for _, fg := range flavorGroups {
		for _, flavor := range fg.Flavors {
			if flavor.Name == resourceName {
				flavorDetails = &flavor
				break
			}
		}
		if flavorDetails != nil {
			break
		}
	}

	// Check if flavor was found
	if flavorDetails == nil {
		log.Error(errors.New("flavor not found"), "flavor not found in any flavor group",
			"resourceName", resourceName)
		return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
	}

	// Call the external scheduler delegation API to get a host for the reservation.
	// Cortex will fetch candidate hosts and weights itself from its knowledge state.
	externalSchedulerRequest := schedulerdelegationapi.ExternalSchedulerRequest{
		Reservation: true,
		Spec: schedulerdelegationapi.NovaObject[schedulerdelegationapi.NovaSpec]{
			Data: schedulerdelegationapi.NovaSpec{
				InstanceUUID:     res.Name,
				NumInstances:     1, // One for each reservation.
				ProjectID:        projectID,
				AvailabilityZone: availabilityZone,
				Flavor: schedulerdelegationapi.NovaObject[schedulerdelegationapi.NovaFlavor]{
					Data: schedulerdelegationapi.NovaFlavor{
						Name:       flavorDetails.Name,
						MemoryMB:   memoryMB, // take the memory from the reservation spec, not from the flavor - reservation might be bigger
						VCPUs:      cpu,      // take the cpu from the reservation spec, not from the flavor - reservation might be bigger
						ExtraSpecs: flavorDetails.ExtraSpecs,
						// Disk is currently not considered.

					},
				},
			},
		},
	}
	httpClient := http.Client{}
	url := r.Conf.Endpoints.NovaExternalScheduler
	reqBody, err := json.Marshal(externalSchedulerRequest)
	if err != nil {
		log.Error(err, "failed to marshal external scheduler request")
		return ctrl.Result{}, err
	}
	response, err := httpClient.Post(url, "application/json", strings.NewReader(string(reqBody)))
	if err != nil {
		log.Error(err, "failed to send external scheduler request", "url", url)
		return ctrl.Result{}, err
	}
	defer response.Body.Close()

	// Check HTTP status code before attempting to decode JSON
	if response.StatusCode != http.StatusOK {
		err := fmt.Errorf("unexpected HTTP status code: %d", response.StatusCode)
		log.Error(err, "external scheduler returned non-OK status",
			"url", url,
			"statusCode", response.StatusCode,
			"status", response.Status)
		return ctrl.Result{}, err
	}

	var externalSchedulerResponse schedulerdelegationapi.ExternalSchedulerResponse
	if err := json.NewDecoder(response.Body).Decode(&externalSchedulerResponse); err != nil {
		log.Error(err, "failed to decode external scheduler response",
			"url", url,
			"statusCode", response.StatusCode)
		return ctrl.Result{}, err
	}
	if len(externalSchedulerResponse.Hosts) == 0 {
		log.Info("no hosts found for reservation", "reservation", req.Name)
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
				log.Error(err, "failed to patch reservation status")
				return ctrl.Result{}, err
			}
			// Object was deleted, no need to continue
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, nil // No need to requeue, we didn't find a host.
	}

	// Update the reservation with the found host (idx 0)
	host := externalSchedulerResponse.Hosts[0]
	log.Info("found host for reservation", "reservation", req.Name, "host", host)
	old := res.DeepCopy()
	meta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
		Type:    v1alpha1.ReservationConditionReady,
		Status:  metav1.ConditionTrue,
		Reason:  "ReservationActive",
		Message: "reservation is successfully scheduled",
	})
	res.Status.Host = host
	patch := client.MergeFrom(old)
	if err := r.Status().Patch(ctx, &res, patch); err != nil {
		// Ignore not-found errors during background deletion
		if client.IgnoreNotFound(err) != nil {
			log.Error(err, "failed to patch reservation status")
			return ctrl.Result{}, err
		}
		// Object was deleted, no need to continue
		return ctrl.Result{}, nil
	}
	return ctrl.Result{}, nil // No need to requeue, the reservation is now active.
}

// reconcileAllocations verifies all allocations in Spec against actual Nova VM state.
// It updates Status.Allocations based on the actual host location of each VM.
func (r *ReservationReconciler) reconcileAllocations(ctx context.Context, res *v1alpha1.Reservation) error {
	log := logf.FromContext(ctx)

	// Skip if no CommittedResourceReservation
	if res.Spec.CommittedResourceReservation == nil {
		return nil
	}

	// TODO trigger migrations of unused reservations (to PAYG VMs)

	// Skip if no allocations to verify
	if len(res.Spec.CommittedResourceReservation.Allocations) == 0 {
		log.Info("no allocations to verify", "reservation", res.Name)
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

			log.Info("verified VM allocation",
				"vm", vmUUID,
				"reservation", res.Name,
				"actualHost", actualHost,
				"expectedHost", res.Status.Host)
		} else {
			// VM not found in database
			log.Info("VM not found in database",
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

	log.Info("reconciled allocations",
		"reservation", res.Name,
		"specAllocations", len(res.Spec.CommittedResourceReservation.Allocations),
		"statusAllocations", len(newStatusAllocations))

	return nil
}

// Init initializes the reconciler with required clients and DB connection.
func (r *ReservationReconciler) Init(ctx context.Context, client client.Client, conf Config) error {
	// Initialize database connection if DatabaseSecretRef is provided.
	if conf.DatabaseSecretRef != nil {
		var err error
		r.DB, err = db.Connector{Client: client}.FromSecretRef(ctx, *conf.DatabaseSecretRef)
		if err != nil {
			return fmt.Errorf("failed to initialize database connection: %w", err)
		}
		logf.FromContext(ctx).Info("database connection initialized for reservation controller")
	}

	return nil
}

func (r *ReservationReconciler) listServersByProjectID(ctx context.Context, projectID string) (map[string]*nova.Server, error) {
	if r.DB == nil {
		return nil, errors.New("database connection not initialized")
	}

	log := logf.FromContext(ctx)

	// Query servers from the database cache.
	var servers []nova.Server
	_, err := r.DB.Select(&servers,
		"SELECT * FROM openstack_servers WHERE tenant_id = $1",
		projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to query servers from database: %w", err)
	}

	log.V(1).Info("queried servers from database",
		"projectID", projectID,
		"serverCount", len(servers))

	// Build lookup map for O(1) access by VM UUID.
	serverMap := make(map[string]*nova.Server, len(servers))
	for i := range servers {
		serverMap[servers[i].ID] = &servers[i]
	}

	return serverMap, nil
}

// notFailoverReservationPredicate filters out failover reservations at the watch level.
// This prevents the controller from being notified about failover reservations,
// which are managed by the separate failover controller.
// Failover reservations are identified by the label "cortex.sap.com/type": "failover".
var notFailoverReservationPredicate = predicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool {
		res, ok := e.Object.(*v1alpha1.Reservation)
		if !ok {
			return false
		}
		return res.Labels["cortex.sap.com/type"] != "failover"
	},
	UpdateFunc: func(e event.UpdateEvent) bool {
		res, ok := e.ObjectNew.(*v1alpha1.Reservation)
		if !ok {
			return false
		}
		return res.Labels["cortex.sap.com/type"] != "failover"
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		res, ok := e.Object.(*v1alpha1.Reservation)
		if !ok {
			return false
		}
		return res.Labels["cortex.sap.com/type"] != "failover"
	},
	GenericFunc: func(e event.GenericEvent) bool {
		res, ok := e.Object.(*v1alpha1.Reservation)
		if !ok {
			return false
		}
		return res.Labels["cortex.sap.com/type"] != "failover"
	},
}

// SetupWithManager sets up the controller with the Manager.
func (r *ReservationReconciler) SetupWithManager(mgr ctrl.Manager, mcl *multicluster.Client) error {
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
		WithEventFilter(notFailoverReservationPredicate).
		Named("reservation").
		WithOptions(controller.Options{
			// We want to process reservations one at a time to avoid overbooking.
			MaxConcurrentReconciles: 1,
		}).
		Complete(r)
}
