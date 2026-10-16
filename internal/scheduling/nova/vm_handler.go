// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	vmv1alpha1 "github.com/cobaltcore-dev/vm-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// VirtualMachineHandler maps a processed scheduling decision into a
// vm-operator VirtualMachine resource and upserts it. The VirtualMachine is
// routed to the cluster matching its availability zone by the multicluster
// client's VirtualMachineRouter.
type VirtualMachineHandler struct {
	client.Client
}

// Upsert creates or patches the VirtualMachine resource described by the
// decision and request. Region and AZ are immutable on the CRD and only written
// on create; mutable fields (target host, cortex spec, labels) are patched on
// updates.
func (h *VirtualMachineHandler) Upsert(ctx context.Context, decision *v1alpha1.Decision, request api.ExternalSchedulerRequest) error {
	log := ctrl.LoggerFrom(ctx)

	if decision.Status.Result == nil || decision.Status.Result.TargetHost == nil {
		log.V(1).Info("skipping virtual machine upsert, no target host in decision")
		return nil
	}

	// We derive hierarchical location information (region and az) from the
	// target host (i.e. the first host in the list). This assumes that all
	// hosts in the list share the same region and availability zone.
	targetHost := *decision.Status.Result.TargetHost
	targetHv := &hv1.Hypervisor{}
	if err := h.Get(ctx, client.ObjectKey{Name: targetHost}, targetHv); err != nil {
		return fmt.Errorf("failed to get hypervisor %q: %w", targetHost, err)
	}
	targetHvRegion, ok := targetHv.Labels[corev1.LabelTopologyRegion]
	if !ok || targetHvRegion == "" {
		return fmt.Errorf("hypervisor %q has no %s label", targetHost, corev1.LabelTopologyRegion)
	}
	targetHvAZ, ok := targetHv.Labels[corev1.LabelTopologyZone]
	// Sanity check: the availability zone inside the request should match the
	// hypervisor's zone if not explicitly set.
	if !ok || request.Spec.Data.AvailabilityZone != targetHvAZ {
		return fmt.Errorf("availability zone mismatch for virtual machine %q: request has %q, hypervisor has %q", request.Spec.Data.InstanceUUID, request.Spec.Data.AvailabilityZone, targetHvAZ)
	}

	// Start building up the virtual machine resource with metadata.
	vm := &vmv1alpha1.VirtualMachine{}
	vm.ObjectMeta = metav1.ObjectMeta{
		Name: request.Spec.Data.InstanceUUID,
		Labels: map[string]string{
			vmv1alpha1.LabelRegion:     targetHvRegion,
			vmv1alpha1.LabelAZ:         targetHvAZ,
			vmv1alpha1.LabelTargetHost: targetHv.Name,
		},
	}

	// Add hierarchical location information to the virtual machine spec.
	vm.Spec.Region = targetHvRegion
	vm.Spec.AZ = targetHvAZ
	vm.Spec.TargetHost = &vmv1alpha1.HostRef{
		APIVersion: hv1.GroupVersion.String(),
		Kind:       "Hypervisor",
		Name:       targetHost,
	}

	// Derive the hypervisor type from the flavor extra spec hypervisor type.
	if hvType, ok := request.Spec.Data.Flavor.
		Data.ExtraSpecs["capabilities:hypervisor_type"]; ok {
		switch {
		case strings.EqualFold(hvType, "CH"):
			vm.Spec.HypervisorType = new(vmv1alpha1.HypervisorTypeCH)
		case strings.EqualFold(hvType, "QEMU"):
			vm.Spec.HypervisorType = new(vmv1alpha1.HypervisorTypeQEMU)
		default:
			return fmt.Errorf("unsupported hypervisor type %q", hvType)
		}
	} else {
		return errors.New("hypervisor type not specified in flavor extra specs")
	}

	// Derive the hypervisor resources from the flavor size.
	flavor := request.Spec.Data.Flavor.Data
	vm.Spec.Resources = map[vmv1alpha1.ResourceName]resource.Quantity{
		vmv1alpha1.ResourceCPU:    *resource.NewQuantity(int64(flavor.VCPUs), resource.DecimalSI),             //nolint:gosec
		vmv1alpha1.ResourceMemory: *resource.NewQuantity(int64(flavor.MemoryMB)*1024*1024, resource.BinarySI), //nolint:gosec
	}

	// Build the nova information from the request data
	vm.Spec.Nova.Ownership = vmv1alpha1.Ownership{
		Project: request.Spec.Data.ProjectID,
		User:    request.Spec.Data.UserID,
	}
	vm.Spec.Nova.Flavor = vmv1alpha1.Flavor{
		Name:       request.Spec.Data.Flavor.Data.Name,
		FlavorID:   request.Spec.Data.Flavor.Data.FlavorID,
		ExtraSpecs: request.Spec.Data.Flavor.Data.ExtraSpecs,
	}
	props := make(map[string]string) // Needs conversion from map[string]any
	for k, v := range request.Spec.Data.Image.Data.Properties.Data {
		if vStr, ok := v.(string); ok {
			props[k] = vStr
		} else {
			log.V(1).Info("skipping non-string image property",
				"key", k, "value", v)
		}
	}
	vm.Spec.Nova.Image = vmv1alpha1.Image{
		UUID:       request.Spec.Data.Image.Data.ID,
		Properties: props,
	}
	if request.Spec.Data.InstanceGroup != nil {
		group := request.Spec.Data.InstanceGroup.Data
		vm.Spec.Nova.InstanceGroup = &vmv1alpha1.InstanceGroup{
			UUID:   group.UUID,
			Policy: group.Policy,
		}
	}

	// Needed to populate the Cortex intent field and the status condition.
	intent, err := request.GetIntent()
	if err != nil {
		return fmt.Errorf("failed to get intent: %w", err)
	}

	// Write the cortex spec (our scheduling result).
	vm.Spec.Cortex = &vmv1alpha1.CortexSpec{}
	for _, host := range decision.Status.Result.OrderedHosts {
		vm.Spec.Cortex.HostCandidates = append(vm.Spec.Cortex.HostCandidates, vmv1alpha1.HostRef{
			APIVersion: hv1.GroupVersion.String(),
			Kind:       "Hypervisor",
			Name:       host,
		})
	}
	vm.Spec.Cortex.Explanation = decision.Status.Explanation
	vm.Spec.Cortex.Intent = string(intent)

	// Write the ConditionTypeScheduled condition to indicate that the VM has
	// been successfully scheduled. For that we need to extract the scheduling
	// intent from request and verify that the operation
	var reason string
	switch intent {
	case api.LiveMigrationIntent:
		reason = vmv1alpha1.ConditionReasonScheduledLiveMigration
	case api.RebuildIntent:
		reason = vmv1alpha1.ConditionReasonScheduledRebuild
	case api.ResizeIntent:
		reason = vmv1alpha1.ConditionReasonScheduledResize
	case api.EvacuateIntent:
		reason = vmv1alpha1.ConditionReasonScheduledEvacuation
	case api.CreateIntent:
		reason = vmv1alpha1.ConditionReasonScheduledInitialPlacement
	default:
		reason = "Unknown intent: " + string(intent)
	}
	meta.SetStatusCondition(&vm.Status.Conditions, metav1.Condition{
		Type:    vmv1alpha1.ConditionTypeScheduled,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: "VM has been successfully scheduled by Cortex",
	})

	// If the vm resource doesn't exist yet, create it.
	vmInCluster := &vmv1alpha1.VirtualMachine{}
	err = h.Get(ctx, types.NamespacedName{
		Namespace: vm.Namespace,
		Name:      vm.Name,
	}, vmInCluster)
	if err != nil && apierrors.IsNotFound(err) {
		// Create ignores the status subresource and writes the server response
		// (with an empty status) back into vm, so snapshot the conditions and
		// restore them before persisting the status.
		conditions := vm.Status.Conditions
		if err := h.Create(ctx, vm); err != nil {
			return fmt.Errorf("failed to create VM: %w", err)
		}
		vm.Status.Conditions = conditions
		if err := h.Status().Update(ctx, vm); err != nil {
			return fmt.Errorf("failed to update VM status: %w", err)
		}
		log.Info("created virtual machine resource", "instanceUUID", vm.Name, "targetHost", targetHost, "intent", intent)
		return nil
	} else if err != nil {
		return fmt.Errorf("error talking to apiserver: %w", err)
	}

	// The vm already exists, so update the existing resource with the new
	// object meta, spec, and status. For map data like the labels, we preserve
	// existing map entries that are not inside the new object, and only
	// store/override the entries that are present in the new object. For status
	// conditions, we only update the conditions that are present in the new
	// object. In contrast, for the host candidates we completely replace the
	// existing list with the new list from the object. For the remaining spec
	// fields, we follow the same approach as for labels: preserve existing
	// entries that are not present in the new object, and only store/override
	// the entries that are present in the new object.
	if vmInCluster.Labels == nil {
		vmInCluster.Labels = map[string]string{}
	}
	maps.Copy(vmInCluster.Labels, vm.Labels)
	// Region and AZ are immutable and are only ever written on create, so we
	// leave vmInCluster.Spec.Region and vmInCluster.Spec.AZ untouched.
	vmInCluster.Spec.TargetHost = vm.Spec.TargetHost
	vmInCluster.Spec.HypervisorType = vm.Spec.HypervisorType
	if vmInCluster.Spec.Resources == nil {
		vmInCluster.Spec.Resources = map[vmv1alpha1.ResourceName]resource.Quantity{}
	}
	maps.Copy(vmInCluster.Spec.Resources, vm.Spec.Resources)
	vmInCluster.Spec.Nova = vm.Spec.Nova
	if vmInCluster.Spec.Cortex == nil {
		vmInCluster.Spec.Cortex = &vmv1alpha1.CortexSpec{}
	}
	vmInCluster.Spec.Cortex.HostCandidates = vm.Spec.Cortex.HostCandidates
	vmInCluster.Spec.Cortex.Explanation = vm.Spec.Cortex.Explanation
	vmInCluster.Spec.Cortex.Intent = vm.Spec.Cortex.Intent
	if err := h.Update(ctx, vmInCluster); err != nil {
		return fmt.Errorf("failed to update VM: %w", err)
	}
	for _, cond := range vm.Status.Conditions {
		meta.SetStatusCondition(&vmInCluster.Status.Conditions, cond)
	}
	if err := h.Status().Update(ctx, vmInCluster); err != nil {
		return fmt.Errorf("failed to update VM status: %w", err)
	}
	log.Info("updated virtual machine resource", "instanceUUID", vm.Name, "targetHost", targetHost, "intent", intent)
	return nil
}
