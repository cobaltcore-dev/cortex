// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/cobaltcore-dev/cortex/pkg/conf"
	"github.com/cobaltcore-dev/cortex/pkg/multicluster"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// HypervisorOvercommitMapping maps hypervisor types to their desired
// overcommit ratios. This mapping will be loaded from a configmap
// that is mounted into the controller pod.
type HypervisorOvercommitMapping struct {
	// Overcommit is the overcommit ratio to set for hypervisors by resource name.
	// Values must be set to something >= 1.0, otherwise the controller will
	// ignore them.
	Overcommit map[hv1.ResourceName]float64 `json:"overcommit"`

	// HasTrait specifies a trait that a hypervisor may have, and that, if present,
	// triggers the controller to set the overcommit ratio specified in the
	// overcommit field for that hypervisor.
	HasTrait *string `json:"hasTrait,omitempty"`

	// HasntTrait specifies a trait that a hypervisor may have, and that, if
	// NOT present, triggers the controller to set the overcommit ratio
	// specified in the overcommit field for that hypervisor.
	HasntTrait *string `json:"hasntTrait,omitempty"`
}

// Validate the provided HypervisorOvercommitMapping, returning an error if the
// mapping is invalid.
func (m *HypervisorOvercommitMapping) Validate() error {
	for resource, overcommit := range m.Overcommit {
		if overcommit < 1.0 {
			return errors.New("invalid overcommit ratio in config, must be >= 1.0. " +
				"Invalid value for resource " + string(resource) + ": " +
				fmt.Sprintf("%f", overcommit))
		}
	}
	// Has trait and hasn't trait are mutually exclusive, so if both are set
	// we return an error.
	if m.HasTrait != nil && m.HasntTrait != nil {
		return errors.New("invalid overcommit mapping, hasTrait and hasntTrait are mutually exclusive")
	}
	// At least one of has trait and hasn't trait must be set,
	// otherwise we don't know when to apply this mapping.
	if m.HasTrait == nil && m.HasntTrait == nil {
		return errors.New("invalid overcommit mapping, at least one of hasTrait and hasntTrait must be set")
	}
	return nil
}

// HypervisorOvercommitConfig holds the configuration for the
// HypervisorOvercommitController and is loaded from a configmap that is mounted
// into the controller pod.
type HypervisorOvercommitConfig struct {
	// OvercommitMappings is a list of mappings that map hypervisor traits to
	// overcommit ratios. Note that this list is applied in order, so if there
	// are multiple mappings applying to the same hypervisors, the last mapping
	// in this list will override the previous ones.
	OvercommitMappings []HypervisorOvercommitMapping `json:"overcommitMappings"`
}

// Validate the provided HypervisorOvercommitConfig, returning an error if the
// config is invalid.
func (c *HypervisorOvercommitConfig) Validate() error {
	// Check that all the individual mappings are valid.
	for _, mapping := range c.OvercommitMappings {
		if err := mapping.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// HypervisorOvercommitController is a controller that reconciles on the
// hypervisor crd and sets desired overcommit ratios based on the hypervisor
// type.
type HypervisorOvercommitController struct {
	client.Client

	// config holds the configuration for the controller, which is loaded from a
	// configmap that is mounted into the controller pod.
	config HypervisorOvercommitConfig
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.20.2/pkg/reconcile
//
// For more details about the method shape, read up here:
// - https://ahmet.im/blog/controller-pitfalls/#reconcile-method-shape
func (c *HypervisorOvercommitController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling resource")

	obj := new(hv1.Hypervisor)
	if err := c.Get(ctx, req.NamespacedName, obj); err != nil {
		if apierrors.IsNotFound(err) {
			// If the custom resource is not found then it usually means
			// that it was deleted or not created.
			log.Info("Resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get resource")
		return ctrl.Result{}, err
	}

	// Build desired overcommit ratios by iterating mappings in order.
	// Later mappings override earlier ones for the same resource, preserving
	// non-overlapping resources from previous mappings.
	desiredOvercommit := make(map[hv1.ResourceName]float64)
	for _, mapping := range c.config.OvercommitMappings {
		log.Info("Processing overcommit mapping",
			"mapping", mapping,
			"hypervisorTraits", obj.Status.Traits)
		var applyMapping bool
		switch {
		// These are mutually exclusive.
		case mapping.HasTrait != nil:
			applyMapping = slices.Contains(obj.Status.Traits, *mapping.HasTrait)
		case mapping.HasntTrait != nil:
			applyMapping = !slices.Contains(obj.Status.Traits, *mapping.HasntTrait)
		default:
			// This should never happen due to validation, but we check it just in case.
			log.Info("Skipping overcommit mapping with no trait specified",
				"overcommit", mapping.Overcommit)
			continue
		}
		if !applyMapping {
			continue
		}
		log.Info("Applying overcommit mapping on hypervisor",
			"overcommit", mapping.Overcommit)
		maps.Copy(desiredOvercommit, mapping.Overcommit)
	}
	log.Info("Desired overcommit ratios based on traits",
		"desiredOvercommit", desiredOvercommit)
	if maps.Equal(desiredOvercommit, obj.Spec.Overcommit) {
		log.Info("Overcommit ratios are up to date, no update needed")
		return ctrl.Result{}, nil
	}

	// Update the desired overcommit ratios on the hypervisor spec.
	orig := obj.DeepCopy()
	obj.Spec.Overcommit = desiredOvercommit
	if err := c.Patch(ctx, obj, client.MergeFrom(orig)); err != nil {
		log.Error(err, "Failed to update hypervisor overcommit ratios")
		return ctrl.Result{}, err
	}
	log.Info("Updated hypervisor with new overcommit ratios",
		"overcommit", desiredOvercommit)

	return ctrl.Result{}, nil
}

// handleRemoteHypervisor is called by watches in remote clusters and triggers
// a reconcile on the hypervisor resource that was changed in the remote cluster.
func (c *HypervisorOvercommitController) handleRemoteHypervisor() handler.EventHandler {
	handler := handler.Funcs{}
	handler.CreateFunc = func(ctx context.Context, evt event.CreateEvent,
		queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {

		queue.Add(ctrl.Request{NamespacedName: client.ObjectKey{
			Name: evt.Object.(*hv1.Hypervisor).Name, // cluster-scoped crd
		}})
	}
	handler.UpdateFunc = func(ctx context.Context, evt event.UpdateEvent,
		queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {

		queue.Add(ctrl.Request{NamespacedName: client.ObjectKey{
			Name: evt.ObjectOld.(*hv1.Hypervisor).Name, // cluster-scoped crd
		}})
	}
	handler.DeleteFunc = func(ctx context.Context, evt event.DeleteEvent,
		queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {

		queue.Add(ctrl.Request{NamespacedName: client.ObjectKey{
			Name: evt.Object.(*hv1.Hypervisor).Name, // cluster-scoped crd
		}})
	}
	return handler
}

// predicateRemoteHypervisor is used to filter events from remote clusters,
// so that only events for hypervisors that should be processed by this
// controller will trigger reconciliations.
func (c *HypervisorOvercommitController) predicateRemoteHypervisor() predicate.Predicate {
	// Currently we're watching all hypervisors. In this way, if a trait
	// gets removed from the hypervisor, we'll still reconcile this
	// hypervisor and update the overcommit ratios accordingly.
	return predicate.NewPredicateFuncs(func(object client.Object) bool {
		_, ok := object.(*hv1.Hypervisor)
		return ok
	})
}

// SetupWithManager sets up the controller with the Manager and a multicluster
// client. The multicluster client is used to watch for changes in the
// Hypervisor CRD across all clusters and trigger reconciliations accordingly.
func (c *HypervisorOvercommitController) SetupWithManager(mgr ctrl.Manager) (err error) {
	// This will load the config in a safe way and gracefully handle errors.
	c.config, err = conf.GetConfig[HypervisorOvercommitConfig]()
	if err != nil {
		return err
	}
	// Validate we don't have any weird values in the config.
	if err := c.config.Validate(); err != nil {
		return err
	}
	// Check that the provided client is a multicluster client, since we need
	// that to watch for hypervisors across clusters.
	mcl, ok := c.Client.(*multicluster.Client)
	if !ok {
		return errors.New("provided client must be a multicluster client")
	}
	return multicluster.
		BuildController(mcl, mgr).
		// The hypervisor crd may be distributed across multiple remote clusters.
		WatchesMulticluster(&hv1.Hypervisor{},
			c.handleRemoteHypervisor(),
			c.predicateRemoteHypervisor(),
		).
		Named("hypervisor-overcommit-controller").
		Complete(c)
}
