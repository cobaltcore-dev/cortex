// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	api "github.com/cobaltcore-dev/cortex/api/external/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	hv1 "github.com/cobaltcore-dev/openstack-hypervisor-operator/api/v1"
	vmv1alpha1 "github.com/cobaltcore-dev/vm-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func newInflightControllerTest(t *testing.T) (*FilterWeigherPipelineController, api.ExternalSchedulerRequest) {
	t.Helper()
	objects := make([]client.Object, 0, 5)
	request := inflightTestRequest()
	request.Spec.Data.AvailabilityZone = "az-1"
	request.Spec.Data.Flavor.Data.ExtraSpecs = map[string]string{"capabilities:hypervisor_type": "QEMU"}
	request.Weights = make(map[string]float64)
	for i := range 5 {
		name := fmt.Sprintf("host-%d", i)
		host := inflightTestHost(name, "az-1")
		host.Labels[corev1.LabelTopologyRegion] = "region-1"
		objects = append(objects, host)
		request.Hosts = append(request.Hosts, api.ExternalSchedulerHost{ComputeHost: name, HypervisorHostname: name})
		request.Weights[name] = float64(5 - i)
	}
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{v1alpha1.AddToScheme, hv1.AddToScheme, vmv1alpha1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).
		WithStatusSubresource(&v1alpha1.Decision{}, &v1alpha1.Reservation{}, &vmv1alpha1.VirtualMachine{}).
		WithInterceptorFuncs(interceptor.Funcs{Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			// The fake client does not assign API-server UIDs needed by owner references.
			if obj.GetUID() == "" {
				obj.SetUID(types.UID(obj.GetName()))
			}
			return c.Create(ctx, obj, opts...)
		}}).Build()
	c := &FilterWeigherPipelineController{
		BasePipelineController: lib.BasePipelineController[lib.FilterWeigherPipeline[api.ExternalSchedulerRequest]]{
			Client:          kube,
			Pipelines:       make(map[string]lib.FilterWeigherPipeline[api.ExternalSchedulerRequest]),
			PipelineConfigs: make(map[string]v1alpha1.Pipeline),
		},
		FeatureGates:     FeatureGates{InFlightReservations: true},
		InFlightConfig:   InFlightConfig{TTLSeconds: 3600, MaxCandidates: 3},
		inflightProducer: &inflightProducer{Client: kube, OwnerResolver: vmOwnerResolver{Client: kube}, TTL: time.Hour},
		vmHandler:        &VirtualMachineHandler{Client: kube},
	}
	for _, name := range []string{"pipeline-1", "pipeline-2"} {
		config := v1alpha1.Pipeline{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec:       v1alpha1.PipelineSpec{Type: v1alpha1.PipelineTypeFilterWeigher, SchedulingDomain: v1alpha1.SchedulingDomainNova},
		}
		result := c.InitPipeline(t.Context(), config)
		if len(result.FilterErrors)+len(result.WeigherErrors)+len(result.UnknownFilters)+len(result.UnknownWeighers) != 0 {
			t.Fatalf("initialize pipeline: %+v", result)
		}
		c.Pipelines[name], c.PipelineConfigs[name] = result.Pipeline, config
	}
	return c, request
}

func inflightControllerDecision(t *testing.T, request api.ExternalSchedulerRequest, intent v1alpha1.SchedulingIntent, pipeline string) *v1alpha1.Decision {
	t.Helper()
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return &v1alpha1.Decision{
		ObjectMeta: metav1.ObjectMeta{Name: request.Spec.Data.InstanceUUID},
		Spec: v1alpha1.DecisionSpec{
			SchedulingDomain: v1alpha1.SchedulingDomainNova,
			Intent:           intent,
			PipelineRef:      corev1.ObjectReference{Name: pipeline},
			NovaRaw:          &runtime.RawExtension{Raw: raw},
		},
	}
}

func TestInflightController(t *testing.T) {
	type mutate func(*FilterWeigherPipelineController, *api.ExternalSchedulerRequest)
	tests := []struct {
		name        string
		intent      v1alpha1.SchedulingIntent
		mutate      mutate
		pipelineErr error
		calls       int // Number of scheduling calls; default 1.

		wantHosts     []string // Hosts of the last call, in order.
		wantHolds     int
		wantOps       int    // Distinct operation IDs across all holds.
		wantPipeline  string // "<vcpus>/<memoryMB>" the pipeline evaluated.
		wantHoldCPU   string
		wantHoldMem   string
		wantVMRequest bool // VM keeps the requested 4 CPU / 8Gi flavor.
	}{
		{
			name: "initial placement defaults to three candidates", intent: api.CreateIntent,
			wantHosts: []string{"host-0", "host-1", "host-2"}, wantHolds: 3, wantOps: 1,
			wantPipeline: "4/8192", wantHoldCPU: "4", wantHoldMem: "8Gi",
		},
		{
			name: "request limit below config limit", intent: api.CreateIntent,
			mutate:    func(_ *FilterWeigherPipelineController, r *api.ExternalSchedulerRequest) { r.Options.MaxCandidates = 2 },
			wantHosts: []string{"host-0", "host-1"}, wantHolds: 2, wantOps: 1,
		},
		{
			name: "request limit above config limit", intent: api.CreateIntent,
			mutate:    func(_ *FilterWeigherPipelineController, r *api.ExternalSchedulerRequest) { r.Options.MaxCandidates = 5 },
			wantHosts: []string{"host-0", "host-1", "host-2"}, wantHolds: 3, wantOps: 1,
		},
		{
			name: "gathered hosts are limited to the request before the candidate limit", intent: api.CreateIntent,
			mutate: func(c *FilterWeigherPipelineController, r *api.ExternalSchedulerRequest) {
				// The highest-ranked gathered host is absent from Nova's preselection.
				gathered := *r
				r.Hosts = r.Hosts[1:]
				c.gatherer = inflightControllerGatherer(func(g *api.ExternalSchedulerRequest) error {
					g.Hosts, g.Weights = gathered.Hosts, gathered.Weights
					return nil
				})
				config := c.PipelineConfigs["pipeline-1"]
				config.Spec.IgnorePreselection = true
				c.PipelineConfigs[config.Name] = config
				c.HTTPConfig.NovaLimitHostsToRequest = true
			},
			wantHosts: []string{"host-1", "host-2", "host-3"}, wantHolds: 3, wantOps: 1,
		},
		{
			name: "each call creates a new operation", intent: api.CreateIntent, calls: 2,
			wantHolds: 6, wantOps: 2,
		},
		{
			name: "resize evaluates the requested flavor and holds target plus source", intent: api.ResizeIntent,
			// KVM source on host-0 with old size 8 CPU / 4Gi; the pipeline still
			// evaluates the requested 4/8192, and two holds are created: the
			// target (new flavor) and the source (old size), both on host-0.
			mutate: func(c *FilterWeigherPipelineController, _ *api.ExternalSchedulerRequest) {
				c.FeatureGates.VirtualMachineCreation = true
				vm := inflightTestVM("host-0", "8", "4Gi")
				hv := &hv1.Hypervisor{}
				if err := c.Get(context.Background(), client.ObjectKey{Name: "host-0"}, hv); err != nil {
					panic(err)
				}
				hv.Status.Instances = []hv1.Instance{{ID: "vm-1", Active: true}}
				if err := c.Update(context.Background(), hv); err != nil {
					panic(err)
				}
				if err := c.Create(context.Background(), vm); err != nil {
					panic(err)
				}
			},
			wantHosts: []string{"host-0"}, wantHolds: 2, wantOps: 1,
			wantPipeline: "4/8192", wantVMRequest: true,
		},
		{
			name: "resize unmanaged source skips the source hold", intent: api.ResizeIntent,
			// No VM CRD and the VM is on no hypervisor: unmanaged source. With
			// skip, only the target hold is created, marked unmanaged.
			mutate: func(c *FilterWeigherPipelineController, _ *api.ExternalSchedulerRequest) {
				c.FeatureGates.VirtualMachineCreation = true
				c.inflightProducer.ResizeUnknownSource = "skip"
			},
			wantHosts: []string{"host-0"}, wantHolds: 1, wantOps: 1,
			wantPipeline: "4/8192", wantHoldCPU: "4", wantHoldMem: "8Gi", wantVMRequest: true,
		},
		{
			name: "pipeline failure stops before in-flight resources are calculated", intent: api.CreateIntent,
			// An empty VM ID is invalid for in-flights; the pipeline error must win.
			mutate: func(_ *FilterWeigherPipelineController, r *api.ExternalSchedulerRequest) {
				r.Spec.Data.InstanceUUID = ""
			},
			pipelineErr: errors.New("pipeline failed"),
		},
		// Requests that must not create holds keep all five candidates.
		{
			name: "feature disabled", intent: api.CreateIntent,
			// Setup only creates the producer when the gate is on.
			mutate:    func(c *FilterWeigherPipelineController, _ *api.ExternalSchedulerRequest) { c.inflightProducer = nil },
			wantHosts: []string{"host-0", "host-1", "host-2", "host-3", "host-4"},
		},
		{
			name: "read-only", intent: api.CreateIntent,
			mutate: func(_ *FilterWeigherPipelineController, r *api.ExternalSchedulerRequest) {
				r.Options.ReadOnly, r.Options.SkipInflight, r.Options.SkipCommittedResourceTracking = true, true, true
			},
			wantHosts: []string{"host-0", "host-1", "host-2", "host-3", "host-4"},
		},
		{
			name: "skip-inflight", intent: api.CreateIntent,
			mutate: func(_ *FilterWeigherPipelineController, r *api.ExternalSchedulerRequest) {
				r.Options.SkipInflight = true
			},
			wantHosts: []string{"host-0", "host-1", "host-2", "host-3", "host-4"},
		},
		{
			name: "non-kvm", intent: api.CreateIntent,
			mutate: func(_ *FilterWeigherPipelineController, r *api.ExternalSchedulerRequest) {
				r.Spec.Data.Flavor.Data.ExtraSpecs["capabilities:hypervisor_type"] = "VMware"
			},
			wantHosts: []string{"host-0", "host-1", "host-2", "host-3", "host-4"},
		},
		{name: "rebuild", intent: api.RebuildIntent, wantHosts: []string{"host-0", "host-1", "host-2", "host-3", "host-4"}},
		{name: "unset intent", intent: "", wantHosts: []string{"host-0", "host-1", "host-2", "host-3", "host-4"}},
		{name: "unknown intent", intent: v1alpha1.SchedulingIntentUnknown, wantHosts: []string{"host-0", "host-1", "host-2", "host-3", "host-4"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, request := newInflightControllerTest(t)
			pipeline := &inflightControllerObservedPipeline{FilterWeigherPipeline: c.Pipelines["pipeline-1"], err: tt.pipelineErr}
			c.Pipelines["pipeline-1"] = pipeline
			if tt.mutate != nil {
				tt.mutate(c, &request)
			}
			var decision *v1alpha1.Decision
			for range max(tt.calls, 1) {
				decision = inflightControllerDecision(t, request, tt.intent, "pipeline-1")
				err := c.ProcessNewDecisionFromAPI(t.Context(), decision)
				if want := tt.pipelineErr; want != nil {
					if !errors.Is(err, want) {
						t.Fatalf("error = %v, want %v", err, want)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if len(decision.Annotations) != 0 {
					t.Fatal("scheduling must not store state on the decision DTO")
				}
			}
			result := decision.Status.Result
			if tt.wantHosts != nil {
				if result == nil || !slices.Equal(result.OrderedHosts, tt.wantHosts) || *result.TargetHost != tt.wantHosts[0] {
					t.Fatalf("result = %+v, want hosts %v", result, tt.wantHosts)
				}
				for host := range result.AggregatedOutWeights {
					if !slices.Contains(tt.wantHosts, host) {
						t.Errorf("weight for dropped host %s", host)
					}
				}
			}
			if tt.wantPipeline != "" {
				flavor := pipeline.request.Spec.Data.Flavor.Data
				if got := fmt.Sprintf("%d/%d", flavor.VCPUs, flavor.MemoryMB); got != tt.wantPipeline {
					t.Errorf("pipeline flavor = %s, want %s", got, tt.wantPipeline)
				}
			}
			holds := &v1alpha1.ReservationList{}
			if err := c.List(t.Context(), holds); err != nil {
				t.Fatal(err)
			}
			if len(holds.Items) != tt.wantHolds {
				t.Fatalf("holds = %d, want %d", len(holds.Items), tt.wantHolds)
			}
			ops := map[string]bool{}
			for _, hold := range holds.Items {
				spec := hold.Spec.InFlightReservation
				ops[spec.OperationID] = true
				if spec.VMID != request.Spec.Data.InstanceUUID || spec.Intent != tt.intent ||
					hold.Spec.EndTime == nil || time.Until(hold.Spec.EndTime.Time) < 59*time.Minute {
					t.Errorf("unexpected hold: %+v", hold.Spec)
				}
				if tt.wantHoldCPU != "" {
					cpu, memory := hold.Spec.Resources[hv1.ResourceCPU], hold.Spec.Resources[hv1.ResourceMemory]
					if cpu.Cmp(resource.MustParse(tt.wantHoldCPU)) != 0 || memory.Cmp(resource.MustParse(tt.wantHoldMem)) != 0 {
						t.Errorf("hold resources = %v, want %s CPU / %s", hold.Spec.Resources, tt.wantHoldCPU, tt.wantHoldMem)
					}
				}
			}
			if len(ops) != tt.wantOps {
				t.Errorf("operations = %d, want %d", len(ops), tt.wantOps)
			}
			if tt.wantVMRequest {
				vm := &vmv1alpha1.VirtualMachine{}
				if err := c.Get(t.Context(), client.ObjectKey{Name: request.Spec.Data.InstanceUUID}, vm); err != nil {
					t.Fatal(err)
				}
				cpu, memory := vm.Spec.Resources[vmv1alpha1.ResourceCPU], vm.Spec.Resources[vmv1alpha1.ResourceMemory]
				if cpu.Cmp(resource.MustParse("4")) != 0 || memory.Cmp(resource.MustParse("8Gi")) != 0 {
					t.Errorf("VM resources = %v, want requested 4 CPU / 8 GiB", vm.Spec.Resources)
				}
			}
		})
	}
}

type inflightControllerGatherer func(*api.ExternalSchedulerRequest) error

func (g inflightControllerGatherer) MutateWithAllCandidates(_ context.Context, r *api.ExternalSchedulerRequest) error {
	return g(r)
}

func TestInflightControllerVMBeforeReservations(t *testing.T) {
	tests := []struct {
		name string
		fail bool
	}{
		{"VM then reservations", false},
		{"reservation failure after VM", true},
	}
	for _, tt := range tests {
		fail := tt.fail
		t.Run(tt.name, func(t *testing.T) {
			c, request := newInflightControllerTest(t)
			c.FeatureGates.VirtualMachineCreation = true
			writeErr := errors.New("reservation write failed")
			var writes []string
			kube := interceptor.NewClient(c.Client.(client.WithWatch), interceptor.Funcs{
				Create: func(ctx context.Context, kube client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					switch obj.(type) {
					case *v1alpha1.Reservation:
						writes = append(writes, "reservation")
						if fail {
							return writeErr
						}
					case *vmv1alpha1.VirtualMachine:
						writes = append(writes, "vm")
					}
					return kube.Create(ctx, obj, opts...)
				},
			})
			c.inflightProducer.Client, c.vmHandler.Client = kube, kube
			c.inflightProducer.OwnerResolver = vmOwnerResolver{Client: kube}
			decision := inflightControllerDecision(t, request, api.CreateIntent, "pipeline-1")
			err := c.ProcessNewDecisionFromAPI(t.Context(), decision)
			// The VM is created first so the reservations can reference it inline.
			wantWrites := []string{"vm", "reservation", "reservation", "reservation"}
			if fail {
				// A reservation write fails after the VM already exists.
				wantWrites = []string{"vm", "reservation"}
				if !errors.Is(err, writeErr) {
					t.Fatalf("error = %v, want reservation write error", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(writes, wantWrites) {
				t.Fatalf("writes = %v, want %v", writes, wantWrites)
			}
			vms := &vmv1alpha1.VirtualMachineList{}
			if err := kube.List(t.Context(), vms); err != nil {
				t.Fatal(err)
			}
			// The VM is always created (before reservations), even on failure.
			if len(vms.Items) != 1 {
				t.Fatalf("VM count = %d, want 1", len(vms.Items))
			}
			if !fail {
				var holds v1alpha1.ReservationList
				if err := kube.List(t.Context(), &holds); err != nil {
					t.Fatal(err)
				}
				for _, hold := range holds.Items {
					if len(hold.OwnerReferences) != 1 || hold.OwnerReferences[0].UID != vms.Items[0].UID {
						t.Errorf("hold %s has no matching VM owner", hold.Name)
					}
				}
			}
		})
	}
}

type inflightControllerObservedPipeline struct {
	lib.FilterWeigherPipeline[api.ExternalSchedulerRequest]
	request api.ExternalSchedulerRequest
	err     error
}

func (p *inflightControllerObservedPipeline) Run(request api.ExternalSchedulerRequest) (v1alpha1.DecisionResult, error) {
	p.request = request
	if p.err != nil {
		return v1alpha1.DecisionResult{}, p.err
	}
	return p.FilterWeigherPipeline.Run(request)
}

func TestInflightControllerSerializesPipelinesDuringCreate(t *testing.T) {
	c, request := newInflightControllerTest(t)
	entered, release := make(chan string, 2), make(chan struct{})
	var unblock sync.Once
	defer unblock.Do(func() { close(release) })
	c.inflightProducer.Client = interceptor.NewClient(c.Client.(client.WithWatch), interceptor.Funcs{
		Create: func(ctx context.Context, kube client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if hold, ok := obj.(*v1alpha1.Reservation); ok {
				entered <- hold.Spec.InFlightReservation.VMID
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return kube.Create(ctx, obj, opts...)
		},
	})
	request.Options.MaxCandidates = 1
	first := inflightControllerDecision(t, request, api.CreateIntent, "pipeline-1")
	request.Spec.Data.InstanceUUID = "vm-2"
	second := inflightControllerDecision(t, request, api.CreateIntent, "pipeline-2")
	results := make(chan error, 2)
	go func() { results <- c.ProcessNewDecisionFromAPI(t.Context(), first) }()
	select {
	case id := <-entered:
		if id != "vm-1" {
			t.Fatalf("first create = %s", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first decision did not reach reservation Create")
	}
	started := make(chan struct{})
	go func() {
		close(started)
		results <- c.ProcessNewDecisionFromAPI(t.Context(), second)
	}()
	<-started
	select {
	case id := <-entered:
		t.Fatalf("%s reached Create while the first pipeline was blocked", id)
	case err := <-results:
		t.Fatalf("decision returned before Create was released: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	unblock.Do(func() { close(release) })
	for range 2 {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("decision did not finish after releasing Create")
		}
	}
	select {
	case id := <-entered:
		if id != "vm-2" {
			t.Fatalf("second create = %s", id)
		}
	default:
		t.Fatal("second pipeline never created its reservation")
	}
}
