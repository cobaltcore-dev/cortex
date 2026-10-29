// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	resv "github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations/inflight"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	vmv1alpha1 "github.com/cobaltcore-dev/vm-operator/api/v1alpha1"
	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// InFlightConfig configures scheduling-time in-flight reservation creation.
type InFlightConfig struct {
	// TTLSeconds must cover the longest permitted operation, including resize confirmation.
	TTLSeconds int `json:"inFlightTTLSeconds,omitempty"`
	// MaxCandidates limits initial-placement candidates; zero defaults to three.
	MaxCandidates int `json:"inFlightMaxCandidates,omitempty"`
	// ResizeUnknownSource selects how to handle a resize whose source size cannot
	// be determined. "fail" (default) rejects the request; "skip" creates only
	// the target hold and no source hold.
	ResizeUnknownSource string `json:"inFlightResizeUnknownSource,omitempty"`
	// ResizeServerDBFallback, when true, reads the old size of a KVM resize from
	// the Nova server DB when the VM has no CRD yet. Requires DatasourceName.
	ResizeServerDBFallback bool `json:"inFlightResizeServerDBFallback,omitempty"`
	// DatasourceName names the Datasource CRD backing the server-DB fallback.
	DatasourceName string `json:"inFlightDatasourceName,omitempty"`
}

// Values for InFlightConfig.ResizeUnknownSource.
const (
	resizeUnknownSourceFail = "fail"
	resizeUnknownSourceSkip = "skip"
)

// inflightIntents are the scheduling intents that create in-flight reservations.
var inflightIntents = []v1alpha1.SchedulingIntent{api.CreateIntent, api.ResizeIntent, api.LiveMigrationIntent}

// OwnerResolver resolves the Kubernetes owner reference for an in-flight
// reservation's subject, so the reservation is garbage collected with its
// owner. It keeps ownership generic: the producer and controller are agnostic
// to the owner kind (today a VirtualMachine, later any CRD).
type OwnerResolver interface {
	// Resolve returns the owner reference for the given subject and the
	// availability zone the owner lives in (owner references only work within
	// one cluster, and reservations are routed by AZ). ok is false when no owner
	// can be attached yet, in which case the reservation relies on the TTL
	// backstop until a later reconcile.
	Resolve(ctx context.Context, subjectID string) (owner metav1.OwnerReference, az string, ok bool)
}

// vmOwnerResolver resolves a VirtualMachine CRD as the owner of a reservation.
type vmOwnerResolver struct {
	client.Client
}

// Resolve implements OwnerResolver for the VirtualMachine CRD.
func (r vmOwnerResolver) Resolve(ctx context.Context, subjectID string) (metav1.OwnerReference, string, bool) {
	vm := &vmv1alpha1.VirtualMachine{}
	if err := r.Get(ctx, client.ObjectKey{Name: subjectID}, vm); err != nil || vm.UID == "" {
		ctrl.LoggerFrom(ctx).Error(err, "cannot resolve in-flight owner, VM not found", "vmID", subjectID)
		return metav1.OwnerReference{}, "", false
	}
	owner := metav1.OwnerReference{APIVersion: vmv1alpha1.GroupVersion.String(), Kind: "VirtualMachine", Name: vm.Name, UID: vm.UID}
	return owner, vm.Spec.AZ, true
}

// inflightProducer creates the in-flight reservations of one scheduling call.
type inflightProducer struct {
	client.Client
	// OwnerResolver attaches an owner reference to created reservations. When it
	// cannot resolve the owner yet (e.g. the VM CRD is created only later for an
	// initial placement), the reservation is written without an owner and the
	// caller attaches it once the owner exists.
	OwnerResolver OwnerResolver
	TTL           time.Duration
	// ResizeUnknownSource mirrors InFlightConfig.ResizeUnknownSource.
	ResizeUnknownSource string
	// VMSource reads the old size of a KVM resize from the Nova server DB when
	// the VM has no CRD yet. Nil disables the server-DB fallback.
	VMSource resv.VMSource
}

// requiredResources returns the resources to block for the request: always the
// requested flavor. The pipeline scores the flavor exactly as Nova sent it;
// resize no longer widens the request to an envelope of the current and new
// size. For resize the source hold (the old size) is resolved separately from
// the VM CRD (see resizeSource).
func (p *inflightProducer) requiredResources(request api.ExternalSchedulerRequest) (map[hv1.ResourceName]resource.Quantity, error) {
	spec := request.Spec.Data
	if spec.InstanceUUID == "" || spec.NumInstances > 1 {
		return nil, errors.New("in-flight reservations require exactly one VM")
	}
	flavor := spec.Flavor.Data
	if flavor.VCPUs == 0 || flavor.MemoryMB == 0 || flavor.VCPUs > math.MaxInt64 || flavor.MemoryMB > math.MaxInt64/(1024*1024) {
		return nil, errors.New("in-flight reservations require positive, representable flavor CPU and memory")
	}
	return map[hv1.ResourceName]resource.Quantity{
		hv1.ResourceCPU:    *resource.NewQuantity(int64(flavor.VCPUs), resource.DecimalSI),
		hv1.ResourceMemory: *resource.NewQuantity(int64(flavor.MemoryMB)*1024*1024, resource.BinarySI),
	}, nil
}

// sourceKind classifies a resize's source instance (ADR 8, UC2). The Hypervisor
// CRDs are the source of truth for where a VM runs; the VM CRD or the server DB
// only supply its old size.
type sourceKind int

const (
	// sourceKVM: the VM is in some Hypervisor Status.Instances, so it is a
	// KVM instance managed by this infrastructure.
	sourceKVM sourceKind = iota
	// sourceUnmanaged: the VM is in no Hypervisor Status.Instances (e.g. a
	// VMware instance).
	sourceUnmanaged
)

// sourceInfo describes a resize's source instance.
type sourceInfo struct {
	kind sourceKind
	// host is the source hypervisor for sourceKVM; empty otherwise.
	host string
	// oldResources is the VM's current size, set only for sourceKVM.
	oldResources map[hv1.ResourceName]resource.Quantity
}

// resizeSource classifies the resize source and, for a KVM source, returns its
// host and old size. The source host is always taken from the Hypervisor CRDs
// (the core infrastructure inventory). The old size comes from the VM CRD
// Spec.Resources when the CRD exists; otherwise, if the server-DB fallback is
// configured, from the Nova server DB. The vm-operator syncs the CRD from Nova
// and this sync may lag, which is accepted. Unlike migrationSource, the source
// host may equal a target host (a same-host resize).
func (p *inflightProducer) resizeSource(ctx context.Context, request api.ExternalSchedulerRequest) (sourceInfo, error) {
	vmID := request.Spec.Data.InstanceUUID
	host, onHypervisor, err := p.hypervisorHosting(ctx, vmID)
	if err != nil {
		return sourceInfo{}, err
	}
	if !onHypervisor {
		// Not on any KVM hypervisor (e.g. a VMware source).
		return sourceInfo{kind: sourceUnmanaged}, nil
	}

	vm := &vmv1alpha1.VirtualMachine{}
	vmErr := p.Get(ctx, client.ObjectKey{Name: vmID}, vm)
	switch {
	case vmErr == nil:
		old, err := oldResourcesFromCRD(vm)
		if err != nil {
			return sourceInfo{}, err
		}
		return sourceInfo{kind: sourceKVM, host: host, oldResources: old}, nil
	case !apierrors.IsNotFound(vmErr):
		return sourceInfo{}, fmt.Errorf("get resize VM %q: %w", vmID, vmErr)
	}

	// No VM CRD yet: fall back to the server DB when configured.
	if p.VMSource == nil {
		return sourceInfo{kind: sourceUnmanaged}, nil
	}
	old, err := p.oldResourcesFromServerDB(ctx, vmID)
	if err != nil {
		return sourceInfo{}, err
	}
	if old == nil {
		// The VM is on a hypervisor but the server DB has no usable size; let
		// the caller apply the unknown-source policy.
		return sourceInfo{kind: sourceUnmanaged}, nil
	}
	return sourceInfo{kind: sourceKVM, host: host, oldResources: old}, nil
}

// hypervisorHosting returns the host whose Hypervisor Status.Instances contains
// the VM, if any.
func (p *inflightProducer) hypervisorHosting(ctx context.Context, vmID string) (host string, found bool, err error) {
	hvs := &hv1.HypervisorList{}
	if err := p.List(ctx, hvs); err != nil {
		return "", false, fmt.Errorf("list hypervisors: %w", err)
	}
	for _, hv := range hvs.Items {
		if slices.ContainsFunc(hv.Status.Instances, func(i hv1.Instance) bool { return i.ID == vmID }) {
			return hv.Name, true, nil
		}
	}
	return "", false, nil
}

// oldResourcesFromCRD converts the VM CRD Spec.Resources (keyed by
// vmv1alpha1.ResourceName) to hv1.ResourceName, validating cpu and memory are
// present and positive.
func oldResourcesFromCRD(vm *vmv1alpha1.VirtualMachine) (map[hv1.ResourceName]resource.Quantity, error) {
	mapping := map[vmv1alpha1.ResourceName]hv1.ResourceName{
		vmv1alpha1.ResourceCPU:    hv1.ResourceCPU,
		vmv1alpha1.ResourceMemory: hv1.ResourceMemory,
	}
	out := make(map[hv1.ResourceName]resource.Quantity, len(mapping))
	for from, to := range mapping {
		qty, ok := vm.Spec.Resources[from]
		if !ok || qty.Sign() <= 0 {
			return nil, fmt.Errorf("resize source VM CRD requires positive %s", to)
		}
		out[to] = qty.DeepCopy()
	}
	return out, nil
}

// oldResourcesFromServerDB reads the VM's current size from the Nova server DB.
// It returns nil (not an error) when the VM is unknown to the DB or its size is
// not positive, so the caller can apply the unknown-source policy. The server
// DB keys resources as "vcpus"/"memory".
func (p *inflightProducer) oldResourcesFromServerDB(ctx context.Context, vmID string) (map[hv1.ResourceName]resource.Quantity, error) {
	vm, err := p.VMSource.GetVM(ctx, vmID)
	if err != nil {
		return nil, fmt.Errorf("get resize VM %q from server DB: %w", vmID, err)
	}
	if vm == nil {
		return nil, nil
	}
	mapping := map[string]hv1.ResourceName{"vcpus": hv1.ResourceCPU, "memory": hv1.ResourceMemory}
	out := make(map[hv1.ResourceName]resource.Quantity, len(mapping))
	for from, to := range mapping {
		qty, ok := vm.Resources[from]
		if !ok || qty.Sign() <= 0 {
			return nil, nil
		}
		out[to] = qty.DeepCopy()
	}
	return out, nil
}

// hostNameMaxLen caps the host segment of a reservation's GenerateName so the
// full name stays well within the 253-char Kubernetes limit even with the
// operator-added random suffix. Hypervisor names are already DNS-1123 object
// names (they are fetched by name), so sanitization is defensive only.
const hostNameMaxLen = 40

var nameInvalidCharsRE = regexp.MustCompile(`[^a-z0-9-]+`)

// sanitizeHostForName turns a host name into a DNS-1123-safe, length-capped
// segment usable inside a GenerateName. Hypervisor names are already safe in
// practice (e.g. "node001-bb086"); this only guards against unexpected input.
func sanitizeHostForName(host string) string {
	out := nameInvalidCharsRE.ReplaceAllString(strings.ToLower(host), "-")
	out = strings.Trim(out, "-")
	if len(out) > hostNameMaxLen {
		out = strings.Trim(out[:hostNameMaxLen], "-")
	}
	if out == "" {
		return "host"
	}
	return out
}

// hold is one in-flight reservation to create: a host and the resources to
// block there. A resize produces a target hold (new flavor) and, for a KVM
// source, a source hold (old size); a create or live migration produces one
// target hold per candidate host. unmanagedSource marks a resize whose source
// is not KVM-managed so the controller releases it on landing (ADR 8, UC2).
type hold struct {
	host            string
	resources       map[hv1.ResourceName]resource.Quantity
	unmanagedSource bool
}

// create writes one in-flight reservation per hold, all sharing a fresh
// operation ID. The holds are built by holds() beforehand; create itself adds no
// source. The whole group is built and validated before the first write. The
// owner (e.g. the VM CRD) is expected to already exist so it is attached inline;
// when it cannot be resolved the reservation is written without an owner and
// relies on the TTL backstop. It returns the created reservations.
func (p *inflightProducer) create(ctx context.Context, request api.ExternalSchedulerRequest, intent v1alpha1.SchedulingIntent, holds []hold) ([]*v1alpha1.Reservation, error) {
	vmID := request.Spec.Data.InstanceUUID
	id, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	operationID := id.String()
	deadline := metav1.NewTime(time.Now().Add(p.TTL))

	// Resolve the owner once for the whole group: the subject (VM) is shared by
	// all reservations. When it already exists (resize, live migration) we set
	// the reference inline so the reservation is never orphaned.
	var owner *metav1.OwnerReference
	var ownerAZ string
	if p.OwnerResolver != nil {
		if ref, az, ok := p.OwnerResolver.Resolve(ctx, vmID); ok {
			owner, ownerAZ = &ref, az
		}
	}

	reservations := make([]*v1alpha1.Reservation, 0, len(holds))
	for _, h := range holds {
		hv := &hv1.Hypervisor{}
		if err := p.Get(ctx, client.ObjectKey{Name: h.host}, hv); err != nil {
			return nil, fmt.Errorf("get hypervisor %q: %w", h.host, err)
		}
		az := hv.Labels[corev1.LabelTopologyZone]
		if az == "" {
			return nil, fmt.Errorf("hypervisor %q has no availability zone", h.host)
		}
		if requested := request.Spec.Data.AvailabilityZone; requested != "" && requested != az {
			return nil, fmt.Errorf("hypervisor %q availability zone %q differs from request %q", h.host, az, requested)
		}
		labels := map[string]string{v1alpha1.LabelReservationType: v1alpha1.ReservationTypeLabelInFlight}
		if h.unmanagedSource {
			labels[inflight.LabelInFlightSource] = inflight.LabelInFlightSourceUnmanaged
		}
		res := &v1alpha1.Reservation{
			ObjectMeta: metav1.ObjectMeta{
				// inflight-<host>-: readable per host; the operator appends a
				// random suffix for uniqueness. The host is also in spec.targetHost.
				GenerateName: fmt.Sprintf("inflight-%s-", sanitizeHostForName(h.host)),
				Labels:       labels,
			},
			Spec: v1alpha1.ReservationSpec{
				Type:             v1alpha1.ReservationTypeInFlight,
				SchedulingDomain: v1alpha1.SchedulingDomainNova,
				AvailabilityZone: az,
				Resources:        h.resources,
				TargetHost:       h.host,
				EndTime:          &deadline,
				InFlightReservation: &v1alpha1.InFlightReservationSpec{
					VMID:        vmID,
					UserID:      request.Spec.Data.UserID,
					ProjectID:   request.Spec.Data.ProjectID,
					Intent:      intent,
					OperationID: operationID,
				},
			},
		}
		// Owner references only work within one cluster; both are routed by AZ.
		if owner != nil && ownerAZ == az {
			res.OwnerReferences = []metav1.OwnerReference{*owner}
		}
		reservations = append(reservations, res)
	}
	for _, res := range reservations {
		if err := p.Create(ctx, res); err != nil {
			return nil, fmt.Errorf("create in-flight reservation: %w", err)
		}
	}
	return reservations, nil
}

// holds builds the in-flight holds for a scheduling call, including any source
// hold the intent needs. create then writes exactly these holds.
//
//   - create: one target hold per candidate host.
//   - resize: a target hold (requested flavor) per candidate host, plus a source
//     hold (old size) on the KVM source host (ADR 8, UC2). The source and target
//     may be the same host. An unmanaged source adds no source hold and marks the
//     target holds so the controller releases them on landing; an unclassifiable
//     source is rejected or skipped per ResizeUnknownSource.
//   - live migration: a target hold per candidate host, plus a source rollback
//     hold with the same resources on the migration source host.
func (p *inflightProducer) holds(ctx context.Context, request api.ExternalSchedulerRequest, intent v1alpha1.SchedulingIntent, resources map[hv1.ResourceName]resource.Quantity, hosts []string) ([]hold, error) {
	switch intent {
	case api.ResizeIntent:
		return p.resizeHolds(ctx, request, resources, hosts)
	case api.LiveMigrationIntent:
		return p.migrationHolds(ctx, request, resources, hosts)
	default:
		return targetHolds(resources, hosts, false), nil
	}
}

// targetHolds builds one target hold per host with the given resources.
func targetHolds(resources map[hv1.ResourceName]resource.Quantity, hosts []string, unmanagedSource bool) []hold {
	holds := make([]hold, 0, len(hosts))
	for _, host := range hosts {
		holds = append(holds, hold{host: host, resources: resources, unmanagedSource: unmanagedSource})
	}
	return holds
}

// resizeHolds builds the target holds plus, for a KVM source, a source hold.
func (p *inflightProducer) resizeHolds(ctx context.Context, request api.ExternalSchedulerRequest, targetResources map[hv1.ResourceName]resource.Quantity, hosts []string) ([]hold, error) {
	src, err := p.resizeSource(ctx, request)
	if err != nil {
		return nil, err
	}
	unmanaged := src.kind == sourceUnmanaged
	if unmanaged && p.ResizeUnknownSource != resizeUnknownSourceSkip {
		return nil, fmt.Errorf("resize source size of VM %q could not be determined", request.Spec.Data.InstanceUUID)
	}
	holds := targetHolds(targetResources, hosts, unmanaged)
	if src.kind == sourceKVM {
		holds = append(holds, hold{host: src.host, resources: src.oldResources})
	}
	return holds, nil
}

// migrationHolds builds the target holds plus the source rollback hold. The
// source rollback hold blocks the same resources as the target.
func (p *inflightProducer) migrationHolds(ctx context.Context, request api.ExternalSchedulerRequest, resources map[hv1.ResourceName]resource.Quantity, hosts []string) ([]hold, error) {
	source, err := p.migrationSource(ctx, request)
	if err != nil {
		return nil, err
	}
	if slices.Contains(hosts, source) {
		return nil, errors.New("live migration source must differ from target host")
	}
	holds := targetHolds(resources, hosts, false)
	return append(holds, hold{host: source, resources: resources}), nil
}

// migrationSource returns the live migration source host: Nova's source_host
// hint, falling back to the VM's observed host. The VM must be in the source
// hypervisor's inventory.
func (p *inflightProducer) migrationSource(ctx context.Context, request api.ExternalSchedulerRequest) (string, error) {
	vmID := request.Spec.Data.InstanceUUID
	source := ""
	if _, ok := request.Spec.Data.SchedulerHints["source_host"]; ok {
		hint, err := request.Spec.Data.GetSchedulerHintStr("source_host")
		if err != nil {
			return "", fmt.Errorf("get migration source hint: %w", err)
		}
		source = hint
	}
	vm := &vmv1alpha1.VirtualMachine{}
	if err := p.Get(ctx, client.ObjectKey{Name: vmID}, vm); err != nil && !apierrors.IsNotFound(err) {
		return "", fmt.Errorf("get migration VM: %w", err)
	}
	if vm.Status.Host != nil && vm.Status.Host.Name != "" {
		if source != "" && source != vm.Status.Host.Name {
			return "", fmt.Errorf("migration source %q differs from observed VM host %q", source, vm.Status.Host.Name)
		}
		source = vm.Status.Host.Name
	}
	if source == "" {
		return "", errors.New("live migration requires a source host")
	}
	hv := &hv1.Hypervisor{}
	if err := p.Get(ctx, client.ObjectKey{Name: source}, hv); err != nil {
		return "", fmt.Errorf("get source hypervisor %q: %w", source, err)
	}
	if !slices.ContainsFunc(hv.Status.Instances, func(i hv1.Instance) bool { return i.ID == vmID }) {
		return "", fmt.Errorf("migration VM %q is not in source hypervisor %q inventory", vmID, source)
	}
	return source, nil
}
