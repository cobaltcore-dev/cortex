// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package plugins_test

import (
	"encoding/json"
	"math/rand/v2"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	_ "github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/compute"
	_ "github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/storage"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/extractor"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/inputs"
	"sigs.k8s.io/yaml"
)

// Helm templates containing the knowledges deployed with cortex.
var knowledgeFiles = []string{
	"../../../../helm/bundles/cortex-nova/templates/knowledges.yaml",
	"../../../../helm/bundles/cortex-nova/templates/knowledges_kvm.yaml",
	"../../../../helm/bundles/cortex-manila/templates/knowledges.yaml",
}

// Go extractors that the expression knowledges must be equivalent to.
var expressionEquivalents = map[string]string{
	"host-capabilities":                    "host_capabilities_extractor",
	"host-utilization":                     "host_utilization_extractor",
	"host-az":                              "host_az_extractor",
	"kvm-libvirt-domain-cpu-steal-pct":     "kvm_libvirt_domain_cpu_steal_pct_extractor",
	"vmware-resolved-hostsystems":          "vrops_hostsystem_resolver",
	"vmware-project-noisiness":             "vrops_project_noisiness_extractor",
	"vmware-long-term-contended-hosts":     "vrops_hostsystem_contention_long_term_extractor",
	"vmware-short-term-contended-hosts":    "vrops_hostsystem_contention_short_term_extractor",
	"netapp-storage-pool-cpu-usage-manila": "netapp_storage_pool_cpu_usage_extractor",
}

// Load the expression knowledges from the helm templates, skipping lines
// with template directives.
func loadExpressionKnowledges(t *testing.T) map[string]v1alpha1.Knowledge {
	t.Helper()
	knowledges := map[string]v1alpha1.Knowledge{}
	for _, file := range knowledgeFiles {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var lines []string
		for line := range strings.SplitSeq(string(raw), "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "{{") {
				lines = append(lines, line)
			}
		}
		for doc := range strings.SplitSeq(strings.Join(lines, "\n"), "\n---") {
			var k v1alpha1.Knowledge
			if err := yaml.Unmarshal([]byte(doc), &k); err != nil {
				t.Fatalf("failed to parse knowledge in %s: %v", file, err)
			}
			if k.Kind == "Knowledge" && k.Spec.Expression != nil {
				knowledges[k.Name] = k
			}
		}
	}
	return knowledges
}

func TestExpressionKnowledgesMatchGoExtractors(t *testing.T) {
	const seeds = 1000
	knowledges := loadExpressionKnowledges(t)
	for name := range knowledges {
		if _, ok := expressionEquivalents[name]; !ok {
			t.Errorf("expression knowledge %q has no equivalent go extractor to test against", name)
		}
	}
	for name, extractorName := range expressionEquivalents {
		t.Run(name, func(t *testing.T) {
			k, ok := knowledges[name]
			if !ok {
				t.Fatalf("expression knowledge %q not found in helm templates", name)
			}
			nonEmpty := 0
			for seed := range uint64(seeds) {
				r := rand.New(rand.NewPCG(seed, 0)) //nolint:gosec // test data
				goExtractor := extractor.Index[extractorName]()
				in := goExtractor.NewInputs()
				env := randomInputs(t, r, in)
				want, err := goExtractor.Extract(t.Context(), in)
				if err != nil {
					t.Fatalf("seed %d: go extractor failed: %v", seed, err)
				}
				got, err := extractor.EvaluateExpression(t.Context(), *k.Spec.Expression, env)
				if err != nil {
					t.Fatalf("seed %d: expression failed: %v", seed, err)
				}
				if g, w := canonical(t, got), canonical(t, want); !slices.Equal(g, w) {
					t.Fatalf("seed %d: expression differs from go extractor\nexpression: %v\ngo:         %v", seed, g, w)
				}
				if len(want) > 0 {
					nonEmpty++
				}
			}
			// Guard against the random inputs rarely producing any features.
			if nonEmpty < seeds*15/100 {
				t.Errorf("only %d of %d random inputs produced features", nonEmpty, seeds)
			}
		})
	}
}

// Fill the input fields of the inputs struct with random objects and return
// them keyed by their var, as passed to expressions.
func randomInputs(t *testing.T, r *rand.Rand, in any) map[string]any {
	t.Helper()
	val := reflect.ValueOf(in).Elem()
	fields, err := inputs.Fields(val.Type())
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]any{}
	for v, idx := range fields {
		field := val.Field(idx)
		objs := reflect.MakeSlice(field.Type(), 0, 0)
		seen := map[string]bool{}
		for range r.IntN(40) {
			obj := reflect.New(field.Type().Elem()).Elem()
			fillRandom(r, obj)
			// Objects with the same primary key can't be stored twice.
			if key, ok := primaryKey(obj); ok {
				if seen[key] {
					continue
				}
				seen[key] = true
			}
			objs = reflect.Append(objs, obj)
		}
		field.Set(objs)
		env[v] = objs.Interface()
	}
	return env
}

// Small value pools, so that joins across inputs match frequently.
var (
	randomStrings = []string{"", "a", "b", "c", "MEMORY_MB", "VCPU", "DISK_GB"}
	randomFloats  = []float64{0, 0.5, 1, 1.5, 4, 33.3}
)

func fillRandom(r *rand.Rand, v reflect.Value) {
	switch v.Kind() {
	case reflect.String:
		v.SetString(randomStrings[r.IntN(len(randomStrings))])
	case reflect.Int, reflect.Int32, reflect.Int64:
		v.SetInt(int64(r.IntN(10)))
	case reflect.Uint, reflect.Uint32, reflect.Uint64:
		v.SetUint(r.Uint64N(10))
	case reflect.Float32, reflect.Float64:
		v.SetFloat(randomFloats[r.IntN(len(randomFloats))])
	case reflect.Bool:
		v.SetBool(r.IntN(2) == 0)
	case reflect.Pointer:
		if r.IntN(3) == 0 {
			v.SetZero()
			return
		}
		p := reflect.New(v.Type().Elem())
		fillRandom(r, p.Elem())
		v.Set(p)
	case reflect.Struct:
		if v.Type() == reflect.TypeFor[time.Time]() {
			v.Set(reflect.ValueOf(time.Unix(int64(r.IntN(1000)), 0)))
			return
		}
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				fillRandom(r, v.Field(i))
			}
		}
	}
}

// Primary key of a stored model, from the fields tagged as primarykey.
func primaryKey(obj reflect.Value) (string, bool) {
	var parts []string
	for i := range obj.NumField() {
		if strings.Contains(obj.Type().Field(i).Tag.Get("db"), ",primarykey") {
			parts = append(parts, obj.Field(i).String())
		}
	}
	return strings.Join(parts, "\x00"), len(parts) > 0
}

// Canonical form of features for comparison, ignoring their order and
// floating point differences from summing in different orders.
func canonical[F any](t *testing.T, features []F) []string {
	t.Helper()
	out := make([]string, len(features))
	for i, f := range features {
		raw, err := json.Marshal(f)
		if err != nil {
			t.Fatal(err)
		}
		var generic any
		if err := json.Unmarshal(raw, &generic); err != nil {
			t.Fatal(err)
		}
		raw, err = json.Marshal(roundFloats(generic))
		if err != nil {
			t.Fatal(err)
		}
		out[i] = string(raw)
	}
	slices.Sort(out)
	return out
}

func roundFloats(v any) any {
	switch x := v.(type) {
	case float64:
		return strconv.FormatFloat(x, 'g', 10, 64)
	case map[string]any:
		for k, e := range x {
			x[k] = roundFloats(e)
		}
	case []any:
		for i, e := range x {
			x[i] = roundFloats(e)
		}
	}
	return v
}
