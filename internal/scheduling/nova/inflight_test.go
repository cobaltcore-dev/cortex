// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"cmp"
	"context"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	resv "github.com/cobaltcore-dev/cortex/internal/scheduling/reservations"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/reservations/inflight"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	vmv1alpha1 "github.com/cobaltcore-dev/vm-operator/api/v1alpha1"
	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func inflightTestRequest() api.ExternalSchedulerRequest {
	request := api.ExternalSchedulerRequest{}
	request.Spec.Data.InstanceUUID = "vm-1"
	request.Spec.Data.UserID = "user-1"
	request.Spec.Data.ProjectID = "project-1"
	request.Spec.Data.Flavor.Data = api.NovaFlavor{VCPUs: 4, MemoryMB: 8192}
	return request
}

func inflightTestClient(t *testing.T, objects ...client.Object) client.WithWatch {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{v1alpha1.AddToScheme, hv1.AddToScheme, vmv1alpha1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	return fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.Reservation{}, &vmv1alpha1.VirtualMachine{}).
		WithObjects(objects...).Build()
}

func inflightTestHost(name, az string) *hv1.Hypervisor {
	return &hv1.Hypervisor{ObjectMeta: metav1.ObjectMeta{
		Name: name, Labels: map[string]string{corev1.LabelTopologyZone: az},
	}}
}

// stubVMSource is a resv.VMSource test double exposing only GetVM, which the
// server-DB resize fallback uses.
type stubVMSource struct {
	vm  *resv.VM
	err error
}

func (s stubVMSource) ListVMs(context.Context) ([]resv.VM, error) { return nil, nil }
func (s stubVMSource) ListVMsByProject(context.Context, string) ([]resv.VM, error) {
	return nil, nil
}
func (s stubVMSource) ListVMsOnHypervisors(context.Context, *hv1.HypervisorList, bool) ([]resv.VM, error) {
	return nil, nil
}
func (s stubVMSource) GetVM(context.Context, string) (*resv.VM, error) { return s.vm, s.err }
func (s stubVMSource) IsServerActive(context.Context, string) (bool, error) {
	return false, nil
}
func (s stubVMSource) GetDeletedVMInfo(context.Context, string) (*resv.DeletedVMInfo, error) {
	return nil, nil
}

// dbVM builds a server-DB VM with the given cpu/memory, keyed as vcpus/memory.
func dbVM(cpu, memory string) *resv.VM {
	return &resv.VM{Resources: map[string]resource.Quantity{
		"vcpus": resource.MustParse(cpu), "memory": resource.MustParse(memory),
	}}
}

// inflightTestVM builds a VM CRD named "vm-1" placed on host with the given
// cpu/memory sizes.
func inflightTestVM(host, cpu, memory string) *vmv1alpha1.VirtualMachine {
	vm := &vmv1alpha1.VirtualMachine{ObjectMeta: metav1.ObjectMeta{Name: "vm-1"}}
	if host != "" {
		vm.Status.Host = &vmv1alpha1.HostRef{Name: host}
	}
	vm.Spec.Resources = map[vmv1alpha1.ResourceName]resource.Quantity{}
	if cpu != "" {
		vm.Spec.Resources[vmv1alpha1.ResourceCPU] = resource.MustParse(cpu)
	}
	if memory != "" {
		vm.Spec.Resources[vmv1alpha1.ResourceMemory] = resource.MustParse(memory)
	}
	return vm
}

// hostWith returns a hypervisor in az-1 hosting the given instance IDs.
func hostWith(name string, instanceIDs ...string) *hv1.Hypervisor {
	hv := inflightTestHost(name, "az-1")
	for _, id := range instanceIDs {
		hv.Status.Instances = append(hv.Status.Instances, hv1.Instance{ID: id})
	}
	return hv
}

// TestInflightRequiredResources verifies the request always blocks the
// requested flavor, for every intent, and never consults a VM client.
func TestInflightRequiredResources(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mutate     func(*api.ExternalSchedulerRequest)
		wantCPU    string
		wantMemory string
		wantErr    bool
	}{
		{name: "flavor", wantCPU: "4", wantMemory: "8Gi"},
		{name: "single instance", mutate: func(r *api.ExternalSchedulerRequest) { r.Spec.Data.NumInstances = 1 }, wantCPU: "4", wantMemory: "8Gi"},
		{name: "missing VM", mutate: func(r *api.ExternalSchedulerRequest) { r.Spec.Data.InstanceUUID = "" }, wantErr: true},
		{name: "multiple instances", mutate: func(r *api.ExternalSchedulerRequest) { r.Spec.Data.NumInstances = 2 }, wantErr: true},
		{name: "zero CPU", mutate: func(r *api.ExternalSchedulerRequest) { r.Spec.Data.Flavor.Data.VCPUs = 0 }, wantErr: true},
		{name: "zero memory", mutate: func(r *api.ExternalSchedulerRequest) { r.Spec.Data.Flavor.Data.MemoryMB = 0 }, wantErr: true},
		{name: "overflow", mutate: func(r *api.ExternalSchedulerRequest) { r.Spec.Data.Flavor.Data.MemoryMB = math.MaxUint64 }, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := inflightTestRequest()
			if tc.mutate != nil {
				tc.mutate(&request)
			}
			p := inflightProducer{}
			got, err := p.requiredResources(request)
			if (err != nil) != tc.wantErr {
				t.Fatalf("requiredResources error = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			for name, want := range map[hv1.ResourceName]string{hv1.ResourceCPU: tc.wantCPU, hv1.ResourceMemory: tc.wantMemory} {
				quantity := got[name]
				if quantity.Cmp(resource.MustParse(want)) != 0 {
					t.Errorf("%s = %s, want %s", name, quantity.String(), want)
				}
			}
		})
	}
}

// TestInflightResizeSource verifies that the Hypervisor CRDs decide location,
// while the VM CRD or the server-DB fallback supplies the old size.
func TestInflightResizeSource(t *testing.T) {
	for _, tc := range []struct {
		name     string
		objects  []client.Object
		vmSource resv.VMSource
		wantKind sourceKind
		wantHost string
		wantCPU  string
		wantErr  bool
	}{
		{
			name:     "kvm with CRD uses CRD size",
			objects:  []client.Object{inflightTestVM("host-1", "2", "4Gi"), hostWith("host-1", "vm-1")},
			wantKind: sourceKVM, wantHost: "host-1", wantCPU: "2",
		},
		{
			name:     "kvm no CRD falls back to server DB",
			objects:  []client.Object{hostWith("host-1", "vm-1")},
			vmSource: stubVMSource{vm: dbVM("3", "6Gi")},
			wantKind: sourceKVM, wantHost: "host-1", wantCPU: "3",
		},
		{
			name:     "kvm no CRD without fallback is unmanaged",
			objects:  []client.Object{hostWith("host-1", "vm-1")},
			wantKind: sourceUnmanaged,
		},
		{
			name:     "kvm no CRD but server DB has no row is unmanaged",
			objects:  []client.Object{hostWith("host-1", "vm-1")},
			vmSource: stubVMSource{vm: nil},
			wantKind: sourceUnmanaged,
		},
		{
			name:     "not on any hypervisor is unmanaged",
			objects:  []client.Object{hostWith("host-1", "other")},
			wantKind: sourceUnmanaged,
		},
		{
			name:    "missing resources in CRD errors",
			objects: []client.Object{inflightTestVM("host-1", "", ""), hostWith("host-1", "vm-1")},
			wantErr: true,
		},
		{
			name:    "zero resources in CRD errors",
			objects: []client.Object{inflightTestVM("host-1", "0", "0"), hostWith("host-1", "vm-1")},
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := inflightProducer{Client: inflightTestClient(t, tc.objects...), VMSource: tc.vmSource}
			got, err := p.resizeSource(t.Context(), inflightTestRequest())
			if (err != nil) != tc.wantErr {
				t.Fatalf("resizeSource error = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if got.kind != tc.wantKind || got.host != tc.wantHost {
				t.Fatalf("kind=%v host=%q, want kind=%v host=%q", got.kind, got.host, tc.wantKind, tc.wantHost)
			}
			if tc.wantCPU != "" {
				cpu := got.oldResources[hv1.ResourceCPU]
				if cpu.Cmp(resource.MustParse(tc.wantCPU)) != 0 {
					t.Errorf("old cpu = %s, want %s", cpu.String(), tc.wantCPU)
				}
			}
		})
	}
}

// TestInflightResizeHolds verifies the hold sets for different-host, same-host
// and unmanaged-source resizes, plus the unknown-source flag.
func TestInflightResizeHolds(t *testing.T) {
	newFlavor := map[hv1.ResourceName]resource.Quantity{
		hv1.ResourceCPU: resource.MustParse("4"), hv1.ResourceMemory: resource.MustParse("8Gi"),
	}
	for _, tc := range []struct {
		name          string
		objects       []client.Object
		hosts         []string
		unknownSource string
		wantHolds     map[string]string // host -> expected cpu ("" means new flavor)
		wantUnmanaged bool
		wantErr       bool
	}{
		{
			name:      "different host kvm source",
			objects:   []client.Object{inflightTestVM("source", "2", "4Gi"), hostWith("source", "vm-1")},
			hosts:     []string{"target"},
			wantHolds: map[string]string{"target": "4", "source": "2"},
		},
		{
			name:      "same host kvm source",
			objects:   []client.Object{inflightTestVM("host-1", "2", "4Gi"), hostWith("host-1", "vm-1")},
			hosts:     []string{"host-1"},
			wantHolds: map[string]string{"host-1": "4"}, // target 4 on host-1 plus source 2 on host-1: two holds.
		},
		{
			name:          "unmanaged source skip",
			objects:       []client.Object{hostWith("elsewhere", "other")},
			hosts:         []string{"target"},
			unknownSource: "skip",
			wantHolds:     map[string]string{"target": "4"},
			wantUnmanaged: true,
		},
		{
			name:          "unmanaged source fail",
			objects:       []client.Object{hostWith("elsewhere", "other")},
			hosts:         []string{"target"},
			unknownSource: "fail",
			wantErr:       true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := inflightProducer{Client: inflightTestClient(t, tc.objects...), ResizeUnknownSource: tc.unknownSource}
			holds, err := p.resizeHolds(t.Context(), inflightTestRequest(), newFlavor, tc.hosts)
			if (err != nil) != tc.wantErr {
				t.Fatalf("resizeHolds error = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			// Same-host resize yields two holds on the same host (target + source).
			if tc.name == "same host kvm source" {
				if len(holds) != 2 {
					t.Fatalf("same-host resize holds = %d, want 2", len(holds))
				}
				for _, h := range holds {
					if h.host != "host-1" {
						t.Errorf("hold host = %q, want host-1", h.host)
					}
				}
				return
			}
			byHost := map[string]hold{}
			for _, h := range holds {
				byHost[h.host] = h
			}
			if len(byHost) != len(tc.wantHolds) {
				t.Fatalf("holds = %v, want hosts %v", holds, tc.wantHolds)
			}
			for host, wantCPU := range tc.wantHolds {
				h, ok := byHost[host]
				if !ok {
					t.Fatalf("missing hold for host %q", host)
				}
				cpu := h.resources[hv1.ResourceCPU]
				if cpu.Cmp(resource.MustParse(wantCPU)) != 0 {
					t.Errorf("hold %q cpu = %s, want %s", host, cpu.String(), wantCPU)
				}
				if host == "target" && h.unmanagedSource != tc.wantUnmanaged {
					t.Errorf("hold %q unmanagedSource = %v, want %v", host, h.unmanagedSource, tc.wantUnmanaged)
				}
			}
		})
	}
}

func TestInflightCreate(t *testing.T) {
	for _, intent := range []v1alpha1.SchedulingIntent{api.CreateIntent, api.ResizeIntent, api.LiveMigrationIntent} {
		t.Run(string(intent), func(t *testing.T) {
			source := inflightTestHost("source", "az-1")
			source.Status.Instances = []hv1.Instance{{ID: "vm-1"}}
			c := inflightTestClient(t, inflightTestHost("target-1", "az-1"), inflightTestHost("target-2", "az-1"), source)
			// Resize source resolution is covered by TestInflightResizeSource;
			// here skip an unknown source so resize yields target-only holds and
			// the create mechanics (names, operation ID, resources) stay in focus.
			p := inflightProducer{Client: c, TTL: time.Hour, ResizeUnknownSource: resizeUnknownSourceSkip}
			request := inflightTestRequest()
			request.Spec.Data.SchedulerHints = map[string]any{"source_host": []any{"source"}}
			resources := map[hv1.ResourceName]resource.Quantity{hv1.ResourceCPU: resource.MustParse("4"), hv1.ResourceMemory: resource.MustParse("8Gi")}
			hostNames := []string{"target-1", "target-2"}
			if intent != api.CreateIntent {
				hostNames = hostNames[:1]
			}
			holds, err := p.holds(t.Context(), request, intent, resources, hostNames)
			if err != nil {
				t.Fatal(err)
			}
			created, err := p.create(t.Context(), request, intent, holds)
			if err != nil {
				t.Fatal(err)
			}
			wantHosts := hostNames
			if intent == api.LiveMigrationIntent {
				wantHosts = append(slices.Clone(hostNames), "source")
			}
			list := &v1alpha1.ReservationList{}
			if err := c.List(t.Context(), list); err != nil {
				t.Fatal(err)
			}
			if len(created) != len(wantHosts) || len(list.Items) != len(wantHosts) {
				t.Fatalf("created %d, stored %d, want %d", len(created), len(list.Items), len(wantHosts))
			}
			operationID := list.Items[0].Spec.InFlightReservation.OperationID
			if id, err := uuid.Parse(operationID); err != nil || id.Version() != 7 {
				t.Fatalf("operation ID %q is not a UUIDv7", operationID)
			}
			for _, res := range list.Items {
				if !strings.HasPrefix(res.Name, "inflight-"+sanitizeHostForName(res.Spec.TargetHost)+"-") {
					t.Errorf("reservation name %q lacks inflight/host prefix", res.Name)
				}
				want := v1alpha1.InFlightReservationSpec{VMID: "vm-1", UserID: "user-1", ProjectID: "project-1", Intent: intent, OperationID: operationID}
				if !slices.Contains(wantHosts, res.Spec.TargetHost) || res.Spec.AvailabilityZone != "az-1" ||
					*res.Spec.InFlightReservation != want || !equality.Semantic.DeepEqual(res.Spec.Resources, resources) ||
					res.Spec.EndTime == nil || time.Until(res.Spec.EndTime.Time) < 59*time.Minute {
					t.Errorf("unexpected reservation: %+v", res.Spec)
				}
			}
		})
	}
}

// TestInflightCreateUnmanagedLabel verifies the unmanaged-source label is set on
// the holds of an unmanaged-source resize and absent otherwise.
func TestInflightCreateUnmanagedLabel(t *testing.T) {
	c := inflightTestClient(t, inflightTestHost("target", "az-1"))
	p := inflightProducer{Client: c, TTL: time.Hour}
	resources := map[hv1.ResourceName]resource.Quantity{hv1.ResourceCPU: resource.MustParse("4"), hv1.ResourceMemory: resource.MustParse("8Gi")}
	created, err := p.create(t.Context(), inflightTestRequest(), api.ResizeIntent, []hold{{host: "target", resources: resources, unmanagedSource: true}})
	if err != nil {
		t.Fatal(err)
	}
	if got := created[0].Labels[inflight.LabelInFlightSource]; got != inflight.LabelInFlightSourceUnmanaged {
		t.Fatalf("source label = %q, want %q", got, inflight.LabelInFlightSourceUnmanaged)
	}
	managed, err := p.create(t.Context(), inflightTestRequest(), api.ResizeIntent, []hold{{host: "target", resources: resources}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := managed[0].Labels[inflight.LabelInFlightSource]; ok {
		t.Fatal("managed resize hold carries the unmanaged source label")
	}
}

func TestInflightMigrationSource(t *testing.T) {
	for _, tc := range []struct {
		name     string
		hint     any
		observed string
		inSource bool
		target   string
		wantErr  bool
	}{
		{name: "hint", hint: "source", inSource: true},
		{name: "observed host fallback", observed: "source", inSource: true},
		{name: "hint matches observed", hint: "source", observed: "source", inSource: true},
		{name: "hint differs from observed", hint: "source", observed: "other", inSource: true, wantErr: true},
		{name: "no source", inSource: true, wantErr: true},
		{name: "bad hint", hint: 42, inSource: true, wantErr: true},
		{name: "not in source inventory", hint: "source", wantErr: true},
		{name: "source equals target", hint: "source", inSource: true, target: "source", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := inflightTestHost("source", "az-1")
			if tc.inSource {
				source.Status.Instances = []hv1.Instance{{ID: "vm-1"}}
			}
			objects := []client.Object{source, inflightTestHost("target", "az-1")}
			if tc.observed != "" {
				vm := &vmv1alpha1.VirtualMachine{ObjectMeta: metav1.ObjectMeta{Name: "vm-1"}}
				vm.Status.Host = &vmv1alpha1.HostRef{Name: tc.observed}
				objects = append(objects, vm)
			}
			c := inflightTestClient(t, objects...)
			p := inflightProducer{Client: c, TTL: time.Hour}
			request := inflightTestRequest()
			if tc.hint != nil {
				request.Spec.Data.SchedulerHints = map[string]any{"source_host": tc.hint}
			}
			target := cmp.Or(tc.target, "target")
			resources := map[hv1.ResourceName]resource.Quantity{hv1.ResourceCPU: resource.MustParse("4"), hv1.ResourceMemory: resource.MustParse("8Gi")}
			holds, err := p.holds(t.Context(), request, api.LiveMigrationIntent, resources, []string{target})
			if err == nil {
				_, err = p.create(t.Context(), request, api.LiveMigrationIntent, holds)
			}
			if (err != nil) != tc.wantErr {
				t.Fatalf("holds/create error = %v, wantErr %v", err, tc.wantErr)
			}
			list := &v1alpha1.ReservationList{}
			if err := c.List(t.Context(), list); err != nil {
				t.Fatal(err)
			}
			if tc.wantErr && len(list.Items) != 0 {
				t.Fatalf("validation error left %d reservations", len(list.Items))
			}
		})
	}
}

func TestInflightCreateValidatesBeforeWriting(t *testing.T) {
	c := inflightTestClient(t, inflightTestHost("target", "az-1"), inflightTestHost("no-az", ""))
	p := inflightProducer{Client: c, TTL: time.Hour}
	resources := map[hv1.ResourceName]resource.Quantity{hv1.ResourceCPU: resource.MustParse("4"), hv1.ResourceMemory: resource.MustParse("8Gi")}
	for _, hostNames := range [][]string{{"target", "missing"}, {"target", "no-az"}} {
		holds := make([]hold, 0, len(hostNames))
		for _, h := range hostNames {
			holds = append(holds, hold{host: h, resources: resources})
		}
		if _, err := p.create(t.Context(), inflightTestRequest(), api.CreateIntent, holds); err == nil {
			t.Fatalf("hosts %v: expected error", hostNames)
		}
	}
	request := inflightTestRequest()
	request.Spec.Data.AvailabilityZone = "az-2"
	if _, err := p.create(t.Context(), request, api.CreateIntent, []hold{{host: "target", resources: resources}}); err == nil {
		t.Fatal("expected AZ mismatch error")
	}
	list := &v1alpha1.ReservationList{}
	if err := c.List(t.Context(), list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 0 {
		t.Fatalf("validation errors left %d reservations", len(list.Items))
	}
}

// resolverStub is a generic OwnerResolver test double, independent of any
// concrete owner CRD, so the producer's owner wiring can be tested in isolation.
type resolverStub struct {
	owner metav1.OwnerReference
	az    string
	ok    bool
}

func (s resolverStub) Resolve(context.Context, string) (metav1.OwnerReference, string, bool) {
	return s.owner, s.az, s.ok
}

func TestInflightCreateAttachesOwner(t *testing.T) {
	owner := metav1.OwnerReference{APIVersion: "example.com/v1", Kind: "Widget", Name: "w-1", UID: "uid-1"}
	for _, tc := range []struct {
		name      string
		resolver  OwnerResolver
		wantOwner bool
	}{
		{name: "owner resolved and AZ matches is attached inline", resolver: resolverStub{owner: owner, az: "az-1", ok: true}, wantOwner: true},
		{name: "owner in a different AZ is not attached", resolver: resolverStub{owner: owner, az: "az-2", ok: true}},
		{name: "owner not resolvable yet is left unset", resolver: resolverStub{}},
		{name: "nil resolver leaves owner unset", resolver: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := inflightTestClient(t, inflightTestHost("target", "az-1"))
			p := inflightProducer{Client: c, OwnerResolver: tc.resolver, TTL: time.Hour}
			resources := map[hv1.ResourceName]resource.Quantity{hv1.ResourceCPU: resource.MustParse("4"), hv1.ResourceMemory: resource.MustParse("8Gi")}
			created, err := p.create(t.Context(), inflightTestRequest(), api.CreateIntent, []hold{{host: "target", resources: resources}})
			if err != nil {
				t.Fatal(err)
			}
			refs := created[0].OwnerReferences
			if tc.wantOwner {
				if len(refs) != 1 || refs[0].UID != owner.UID {
					t.Fatalf("expected owner reference %v, got %v", owner, refs)
				}
			} else if len(refs) != 0 {
				t.Fatalf("expected no owner reference, got %v", refs)
			}
		})
	}
}

func TestVMOwnerResolver(t *testing.T) {
	vm := &vmv1alpha1.VirtualMachine{ObjectMeta: metav1.ObjectMeta{Name: "vm-1", UID: "vm-uid"}}
	vm.Spec.AZ = "az-1"
	r := vmOwnerResolver{Client: inflightTestClient(t, vm)}
	owner, az, ok := r.Resolve(t.Context(), "vm-1")
	if !ok || az != "az-1" || owner.Kind != "VirtualMachine" || owner.Name != "vm-1" || owner.UID != "vm-uid" {
		t.Fatalf("unexpected resolve result: owner=%+v az=%q ok=%v", owner, az, ok)
	}
	if _, _, ok := (vmOwnerResolver{Client: inflightTestClient(t)}).Resolve(t.Context(), "missing"); ok {
		t.Fatal("expected ok=false for missing VM")
	}
}
