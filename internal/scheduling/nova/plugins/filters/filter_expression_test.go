// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package filters

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/go-logr/logr"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/scheduling/compute"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// hostFlag is a minimal feature used to drive the expression in tests.
type hostFlag struct {
	ComputeHost string `json:"computeHost"`
	Blocked     bool   `json:"blocked"`
}

func TestFilterExpressionOpts_Validate(t *testing.T) {
	tests := []struct {
		name      string
		opts      FilterExpressionOpts
		wantError bool
	}{
		{
			name: "valid expression without inputs",
			opts: FilterExpressionOpts{Run: "host != \"\""},
		},
		{
			name: "valid expression referencing an input",
			opts: FilterExpressionOpts{
				Run:        "none(flags, .computeHost == host && .blocked)",
				Knowledges: map[string][]string{"flags": {"host-flags"}},
			},
		},
		{
			name:      "knowledge references multiple names",
			opts:      FilterExpressionOpts{Run: "true", Knowledges: map[string][]string{"flags": {"a", "b"}}},
			wantError: true,
		},
		{
			name:      "empty expression",
			opts:      FilterExpressionOpts{Run: ""},
			wantError: true,
		},
		{
			name:      "expression does not compile",
			opts:      FilterExpressionOpts{Run: "host >"},
			wantError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.Validate()
			if (err != nil) != tt.wantError {
				t.Errorf("Validate() error = %v, wantError %v", err, tt.wantError)
			}
		})
	}
}

func TestFilterExpression_Init(t *testing.T) {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	params := []v1alpha1.Parameter{
		{Key: "run", StringValue: new("true")},
	}

	tests := []struct {
		name          string
		knowledge     *v1alpha1.Knowledge
		filterSpec    v1alpha1.FilterSpec
		wantError     bool
		errorContains string
	}{
		{
			name:       "init without inputs",
			filterSpec: v1alpha1.FilterSpec{Name: "filter_expression", Params: params},
		},
		{
			name: "init with ready knowledge input",
			knowledge: &v1alpha1.Knowledge{
				ObjectMeta: metav1.ObjectMeta{Name: "host-flags"},
				Status: v1alpha1.KnowledgeStatus{
					Conditions: []metav1.Condition{{
						Type:   v1alpha1.KnowledgeConditionReady,
						Status: metav1.ConditionTrue,
					}},
					RawLength: 1,
				},
			},
			filterSpec: v1alpha1.FilterSpec{
				Name: "filter_expression",
				Params: []v1alpha1.Parameter{
					{Key: "run", StringValue: new("true")},
					{Key: "knowledges", StringListMapValue: &map[string][]string{"flags": {"host-flags"}}},
				},
			},
		},
		{
			name: "fails when referenced knowledge is missing",
			filterSpec: v1alpha1.FilterSpec{
				Name: "filter_expression",
				Params: []v1alpha1.Parameter{
					{Key: "run", StringValue: new("true")},
				},
			},
			// Inputs set via struct below to reference a missing knowledge.
			wantError:     true,
			errorContains: "failed to get knowledge",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if tt.knowledge != nil {
				builder = builder.WithObjects(tt.knowledge)
			}
			client := builder.Build()

			step := &FilterExpression{}
			// For the missing-knowledge case, configure the input directly.
			if tt.name == "fails when referenced knowledge is missing" {
				step.Options.Knowledges = map[string][]string{"flags": {"does-not-exist"}}
			}
			err := step.Init(context.Background(), client, tt.filterSpec)
			if tt.wantError {
				if err == nil {
					t.Error("expected error, got nil")
				} else if tt.errorContains != "" && !strings.Contains(err.Error(), tt.errorContains) {
					t.Errorf("expected error containing %q, got %q", tt.errorContains, err.Error())
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestFilterExpression_Run(t *testing.T) {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	raw, err := v1alpha1.BoxFeatureList([]hostFlag{
		{ComputeHost: "host1", Blocked: false},
		{ComputeHost: "host2", Blocked: true},
		{ComputeHost: "host3", Blocked: true},
	})
	if err != nil {
		t.Fatalf("failed to box features: %v", err)
	}
	knowledge := &v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{Name: "host-flags"},
		Status: v1alpha1.KnowledgeStatus{
			Conditions: []metav1.Condition{{
				Type:   v1alpha1.KnowledgeConditionReady,
				Status: metav1.ConditionTrue,
			}},
			Raw:       raw,
			RawLength: 3,
		},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(knowledge).Build()

	step := &FilterExpression{}
	step.name = "filter_expression"
	step.Client = client
	step.Options = FilterExpressionOpts{
		// Keep hosts that are not flagged as blocked.
		Run:        "none(flags, .computeHost == host && .blocked)",
		Knowledges: map[string][]string{"flags": {"host-flags"}},
	}

	request := compute.PlacementRequest{
		Hosts: []compute.Host{
			{ComputeHost: "host1"},
			{ComputeHost: "host2"},
			{ComputeHost: "host3"},
		},
	}
	result, err := step.Run(logr.Discard(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := make([]string, 0, len(result.Activations))
	for host := range result.Activations {
		got = append(got, host)
	}
	slices.Sort(got)
	want := []string{"host1"}
	if !slices.Equal(got, want) {
		t.Fatalf("expected remaining hosts %v, got %v", want, got)
	}
}

func TestFilterExpression_RunDoesNotAddHosts(t *testing.T) {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	step := &FilterExpression{}
	step.name = "filter_expression"
	step.Client = client
	step.Options = FilterExpressionOpts{Run: "true"}

	request := compute.PlacementRequest{
		Hosts: []compute.Host{{ComputeHost: "host1"}, {ComputeHost: "host2"}},
	}
	result, err := step.Run(logr.Discard(), request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Activations) != 2 {
		t.Fatalf("expected 2 hosts, got %d", len(result.Activations))
	}
}
