// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package extractor

import (
	"context"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/store"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/store/postgres"
	testlibDB "github.com/cobaltcore-dev/cortex/pkg/knowledge/store/postgres/testing"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const testType = "test"

type testModel struct {
	Name  string `db:"name,primarykey"`
	Value int    `db:"value"`
}

func (testModel) TableName() string            { return "test_model" }
func (testModel) Indexes() map[string][]string { return nil }

type testFeature struct {
	Name  string `json:"name"`
	Total int    `json:"total"`
}

type testInputs struct {
	Objs  []testModel   `input:"objs"`
	Feats []testFeature `input:"feats"`
}

type testExtractor struct{ BaseExtractor[struct{}] }

func (e *testExtractor) Extract(_ context.Context, in testInputs) ([]testFeature, error) {
	out := make([]testFeature, 0, len(in.Objs))
	for _, o := range in.Objs {
		out = append(out, testFeature{Name: o.Name, Total: o.Value + len(in.Feats)})
	}
	return out, nil
}

type wrongInputs struct {
	Objs  []testFeature `input:"objs"`
	Feats []testFeature `input:"feats"`
}

type wrongExtractor struct{ BaseExtractor[struct{}] }

func (e *wrongExtractor) Extract(context.Context, wrongInputs) ([]testFeature, error) {
	return nil, nil
}

// Store handed out by the test storage backend.
var testStore store.Store

func init() {
	Register("test_extractor", func() Extractor[testInputs, testFeature] { return &testExtractor{} })
	Register("wrong_extractor", func() Extractor[wrongInputs, testFeature] { return &wrongExtractor{} })
	store.Register(testType, func(context.Context, client.Client, v1alpha1.StorageSpec) (store.Store, error) {
		return testStore, nil
	})
	datasources.RegisterScope(func(spec v1alpha1.DatasourceSpec) (store.Scope, bool) {
		return store.Scope{Model: testModel{}}, spec.Type == testType
	})
}

func TestKnowledgeReconciler_Reconcile(t *testing.T) {
	inputs := []v1alpha1.InputSpec{{Var: "objs", Datasource: "ds"}, {Var: "feats", Knowledge: "k-in"}}
	extractor := &v1alpha1.KnowledgeExtractorSpec{Name: "test_extractor"}
	expression := func(run string) *v1alpha1.KnowledgeExpressionSpec {
		return &v1alpha1.KnowledgeExpressionSpec{Run: run}
	}
	tests := []struct {
		name       string
		extractor  *v1alpha1.KnowledgeExtractorSpec
		expression *v1alpha1.KnowledgeExpressionSpec
		inputs     []v1alpha1.InputSpec
		wantReason string
		wantErr    bool
		want       []testFeature
	}{
		{
			name:       "go extractor",
			extractor:  extractor,
			inputs:     inputs,
			wantReason: "KnowledgeExtracted",
			want:       []testFeature{{"a", 2}, {"b", 3}},
		},
		{
			name:       "expression",
			expression: expression(`objs | map(let o = #; {name: o.Name, total: o.Value + len(feats) + feats[0].Total})`),
			inputs:     inputs,
			wantReason: "KnowledgeExtracted",
			want:       []testFeature{{"a", 12}, {"b", 13}},
		},
		{
			name:       "unsupported extractor",
			extractor:  &v1alpha1.KnowledgeExtractorSpec{Name: "unknown"},
			wantReason: "UnsupportedExtractor",
		},
		{
			name:       "missing input",
			extractor:  extractor,
			inputs:     inputs[:1],
			wantReason: "InputMissing",
		},
		{
			name:       "unused input",
			extractor:  extractor,
			inputs:     append([]v1alpha1.InputSpec{{Var: "other", Datasource: "ds"}}, inputs...),
			wantReason: "InputUnused",
		},
		{
			name:       "input type mismatch",
			extractor:  &v1alpha1.KnowledgeExtractorSpec{Name: "wrong_extractor"},
			inputs:     inputs,
			wantReason: "InputTypeMismatch",
		},
		{
			name:       "missing datasource",
			extractor:  extractor,
			inputs:     []v1alpha1.InputSpec{{Var: "objs", Datasource: "missing"}, inputs[1]},
			wantReason: "InputFetchFailed",
			wantErr:    true,
		},
		{
			name:       "expression with unknown field",
			expression: expression(`objs | map(.UnknownField)`),
			inputs:     inputs,
			wantReason: "ExpressionCompilationFailed",
		},
		{
			name:       "expression over generic features of an expression knowledge",
			expression: expression(`objs | map(let o = #; {name: o.Name, total: o.Value + feats[0].total, extra: true})`),
			inputs:     []v1alpha1.InputSpec{inputs[0], {Var: "feats", Knowledge: "k-expr"}},
			wantReason: "KnowledgeExtracted",
			want:       []testFeature{{"a", 21}, {"b", 22}},
		},
		{
			name:       "expression output not a list",
			expression: expression(`len(objs)`),
			inputs:     inputs,
			wantReason: "ExpressionOutputInvalid",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbEnv := testlibDB.SetupDBEnv(t)
			defer dbEnv.Close()
			testStore = postgres.NewStore(&postgres.DB{DbMap: dbEnv.DbMap})
			ctx := t.Context()
			if err := store.ReplaceAll(ctx, testStore, "", []testModel{{"a", 1}, {"b", 2}}); err != nil {
				t.Fatal(err)
			}
			raw, err := v1alpha1.BoxFeatureList([]testFeature{{"x", 10}})
			if err != nil {
				t.Fatal(err)
			}
			objs := []client.Object{
				&v1alpha1.Datasource{
					ObjectMeta: metav1.ObjectMeta{Name: "ds"},
					Spec:       v1alpha1.DatasourceSpec{Type: testType, Storage: v1alpha1.StorageSpec{Type: testType}},
				},
				&v1alpha1.Knowledge{
					ObjectMeta: metav1.ObjectMeta{Name: "k-expr"},
					Spec:       v1alpha1.KnowledgeSpec{Expression: expression(`[]`)},
					Status:     v1alpha1.KnowledgeStatus{Raw: runtime.RawExtension{Raw: []byte(`{"features":[{"name":"y","total":20}]}`)}},
				},
				&v1alpha1.Knowledge{
					ObjectMeta: metav1.ObjectMeta{Name: "k-in"},
					Spec:       v1alpha1.KnowledgeSpec{Extractor: extractor},
					Status:     v1alpha1.KnowledgeStatus{Raw: raw},
				},
				&v1alpha1.Knowledge{
					ObjectMeta: metav1.ObjectMeta{Name: "k"},
					Spec: v1alpha1.KnowledgeSpec{
						Extractor:  tt.extractor,
						Expression: tt.expression,
						Inputs:     tt.inputs,
					},
				},
			}
			c := newFakeClient(t, objs...)
			r := &KnowledgeReconciler{Client: c, Monitor: NewMonitor()}

			_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "k"}})
			if (err != nil) != tt.wantErr {
				t.Fatalf("expected error %v, got %v", tt.wantErr, err)
			}
			var k v1alpha1.Knowledge
			if err := c.Get(ctx, types.NamespacedName{Name: "k"}, &k); err != nil {
				t.Fatal(err)
			}
			cond := meta.FindStatusCondition(k.Status.Conditions, v1alpha1.KnowledgeConditionReady)
			if cond == nil || cond.Reason != tt.wantReason {
				t.Fatalf("expected reason %q, got %+v", tt.wantReason, cond)
			}
			if tt.want == nil {
				return
			}
			got, err := v1alpha1.UnboxFeatureList[testFeature](k.Status.Raw)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("expected %v, got %v", tt.want, got)
				}
			}
			if k.Status.RawLength != len(tt.want) || k.Status.LastContentChange.IsZero() {
				t.Errorf("expected status to be updated, got %+v", k.Status)
			}
		})
	}
}

func TestKnowledgeReconciler_Reconcile_SkipRecentExtraction(t *testing.T) {
	k := &v1alpha1.Knowledge{
		ObjectMeta: metav1.ObjectMeta{Name: "k"},
		Spec: v1alpha1.KnowledgeSpec{
			Extractor: &v1alpha1.KnowledgeExtractorSpec{Name: "unknown"},
			Recency:   metav1.Duration{Duration: time.Hour},
		},
		Status: v1alpha1.KnowledgeStatus{LastExtracted: metav1.NewTime(time.Now())},
	}
	c := newFakeClient(t, k)
	r := &KnowledgeReconciler{Client: c, Monitor: NewMonitor()}
	result, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "k"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.RequeueAfter <= 0 || result.RequeueAfter > time.Hour {
		t.Errorf("expected requeue within the recency, got %v", result.RequeueAfter)
	}
	var updated v1alpha1.Knowledge
	if err := c.Get(t.Context(), types.NamespacedName{Name: "k"}, &updated); err != nil {
		t.Fatal(err)
	}
	if len(updated.Status.Conditions) != 0 {
		t.Errorf("expected no extraction, got conditions %v", updated.Status.Conditions)
	}
}

func TestFeaturesChanged(t *testing.T) {
	tests := []struct {
		name     string
		old, new string
		want     bool
	}{
		{"no previous features", ``, `{"features":[]}`, true},
		{"same order", `{"features":[{"a":1},{"a":2}]}`, `{"features":[{"a":1},{"a":2}]}`, false},
		{"different order", `{"features":[{"a":2},{"a":1}]}`, `{"features":[{"a":1},{"a":2}]}`, false},
		{"different key order", `{"features":[{"a":1,"b":2}]}`, `{"features":[{"b":2,"a":1}]}`, false},
		{"different values", `{"features":[{"a":1}]}`, `{"features":[{"a":2}]}`, true},
		{"different length", `{"features":[{"a":1},{"a":1}]}`, `{"features":[{"a":1}]}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := featuresChanged([]byte(tt.old), []byte(tt.new)); got != tt.want {
				t.Errorf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func newFakeClient(t *testing.T, objs ...client.Object) client.Client {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&v1alpha1.Knowledge{}).
		Build()
}
