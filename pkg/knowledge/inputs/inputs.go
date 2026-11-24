// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

// Package inputs resolves the datasources and knowledges referenced by a
// knowledge or kpi into typed lists of objects.
package inputs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/datasources"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/store"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Struct tag marking fields that receive input objects, e.g.
//
//	type MyInputs struct {
//		Hypervisors []nova.Hypervisor `input:"hypervisors"`
//	}
//
// The tag value refers to the var of an input spec.
const Tag = "input"

var (
	// An input required by the extractor or kpi is not configured.
	ErrMissing = errors.New("input missing")
	// A configured input is not consumed by the extractor or kpi.
	ErrUnused = errors.New("input unused")
	// The objects of a configured input don't match the expected type.
	ErrTypeMismatch = errors.New("input type mismatch")
	// The objects of a configured input could not be fetched.
	ErrFetch = errors.New("input fetch failed")
)

// Resolver fetches the objects of inputs.
type Resolver struct {
	// Client to get the referenced datasources, knowledges and storage secrets.
	Client client.Client
}

// Resolve fills the fields of target, a pointer to a struct, that are tagged
// with `input:"<var>"` with the objects of the input spec with that var.
// All tagged fields must be slices, all of them must be configured, and all
// configured inputs must be consumed.
func (r Resolver) Resolve(ctx context.Context, specs []v1alpha1.InputSpec, target any) error {
	val := reflect.ValueOf(target)
	if val.Kind() != reflect.Pointer || val.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("expected pointer to struct, got %T", target)
	}
	fields, err := Fields(val.Elem().Type())
	if err != nil {
		return err
	}
	byVar := make(map[string]v1alpha1.InputSpec, len(specs))
	for _, spec := range specs {
		if _, ok := fields[spec.Var]; !ok {
			return fmt.Errorf("%w: %q", ErrUnused, spec.Var)
		}
		byVar[spec.Var] = spec
	}
	for v, idx := range fields {
		spec, ok := byVar[v]
		if !ok {
			return fmt.Errorf("%w: %q", ErrMissing, v)
		}
		field := val.Elem().Field(idx)
		objs, err := r.fetch(ctx, spec, field.Type().Elem())
		if err != nil {
			return err
		}
		field.Set(objs)
	}
	return nil
}

// ResolveAll fetches the objects of all input specs, keyed by their var.
// Datasource objects are typed by their registered model. Knowledge features
// are typed by the type returned by featureType, or decoded into generic maps
// if the feature type of the knowledge is unknown.
func (r Resolver) ResolveAll(
	ctx context.Context,
	specs []v1alpha1.InputSpec,
	featureType func(v1alpha1.KnowledgeSpec) (reflect.Type, bool),
) (map[string]any, error) {

	out := make(map[string]any, len(specs))
	for _, spec := range specs {
		if spec.Knowledge == "" {
			objs, err := r.fetch(ctx, spec, nil)
			if err != nil {
				return nil, err
			}
			out[spec.Var] = objs.Interface()
			continue
		}
		k := &v1alpha1.Knowledge{}
		if err := r.Client.Get(ctx, client.ObjectKey{Name: spec.Knowledge}, k); err != nil {
			return nil, fmt.Errorf("%w: knowledge %q: %w", ErrFetch, spec.Knowledge, err)
		}
		elemType, ok := featureType(k.Spec)
		if !ok {
			elemType = reflect.TypeFor[map[string]any]()
		}
		objs, err := decodeFeatures(k, elemType)
		if err != nil {
			return nil, err
		}
		out[spec.Var] = objs.Interface()
	}
	return out, nil
}

// Fields returns the struct field indexes of the input fields by their var.
func Fields(t reflect.Type) (map[string]int, error) {
	fields := map[string]int{}
	for i := range t.NumField() {
		f := t.Field(i)
		v, ok := f.Tag.Lookup(Tag)
		if !ok {
			continue
		}
		if f.Type.Kind() != reflect.Slice {
			return nil, fmt.Errorf("input field %s must be a slice, got %s", f.Name, f.Type)
		}
		if !f.IsExported() {
			return nil, fmt.Errorf("input field %s must be exported", f.Name)
		}
		if _, ok := fields[v]; ok {
			return nil, fmt.Errorf("duplicate input var %q", v)
		}
		fields[v] = i
	}
	return fields, nil
}

// References returns the names of the datasources and knowledges referenced
// by the input specs.
func References(specs []v1alpha1.InputSpec) (datasourceNames, knowledgeNames []string) {
	for _, spec := range specs {
		if spec.Datasource != "" {
			datasourceNames = append(datasourceNames, spec.Datasource)
		}
		if spec.Knowledge != "" {
			knowledgeNames = append(knowledgeNames, spec.Knowledge)
		}
	}
	return datasourceNames, knowledgeNames
}

// Fetch the objects of the input as a slice of elemType. For datasources,
// elemType may be nil to use the registered model type of the datasource.
func (r Resolver) fetch(ctx context.Context, spec v1alpha1.InputSpec, elemType reflect.Type) (reflect.Value, error) {
	switch {
	case spec.Datasource != "" && spec.Knowledge == "":
		return r.fetchDatasource(ctx, spec.Datasource, elemType)
	case spec.Knowledge != "" && spec.Datasource == "":
		return r.fetchKnowledge(ctx, spec.Knowledge, elemType)
	default:
		return reflect.Value{}, fmt.Errorf("input %q must reference exactly one of datasource or knowledge", spec.Var)
	}
}

func (r Resolver) fetchDatasource(ctx context.Context, name string, elemType reflect.Type) (reflect.Value, error) {
	ds := &v1alpha1.Datasource{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: name}, ds); err != nil {
		return reflect.Value{}, fmt.Errorf("%w: datasource %q: %w", ErrFetch, name, err)
	}
	scope, err := datasources.ScopeFor(ds.Spec)
	if err != nil {
		return reflect.Value{}, fmt.Errorf("%w: datasource %q: %w", ErrTypeMismatch, name, err)
	}
	modelType := reflect.TypeOf(scope.Model)
	if elemType == nil {
		elemType = modelType
	}
	if elemType != modelType {
		return reflect.Value{}, fmt.Errorf("%w: datasource %q provides %s, expected %s",
			ErrTypeMismatch, name, modelType, elemType)
	}
	st, err := store.Open(ctx, r.Client, ds.Spec.Storage)
	if err != nil {
		return reflect.Value{}, fmt.Errorf("%w: datasource %q: %w", ErrFetch, name, err)
	}
	out := reflect.New(reflect.SliceOf(elemType))
	if err := st.ListAll(ctx, scope, out.Interface()); err != nil {
		return reflect.Value{}, fmt.Errorf("%w: datasource %q: %w", ErrFetch, name, err)
	}
	return out.Elem(), nil
}

func (r Resolver) fetchKnowledge(ctx context.Context, name string, elemType reflect.Type) (reflect.Value, error) {
	k := &v1alpha1.Knowledge{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: name}, k); err != nil {
		return reflect.Value{}, fmt.Errorf("%w: knowledge %q: %w", ErrFetch, name, err)
	}
	return decodeFeatures(k, elemType)
}

// Decode the extracted features of the knowledge into a slice of elemType.
func decodeFeatures(k *v1alpha1.Knowledge, elemType reflect.Type) (reflect.Value, error) {
	name := k.Name
	out := reflect.New(reflect.SliceOf(elemType))
	if len(k.Status.Raw.Raw) == 0 {
		return out.Elem(), nil
	}
	var boxed struct {
		Features json.RawMessage `json:"features"`
	}
	if err := json.Unmarshal(k.Status.Raw.Raw, &boxed); err != nil {
		return reflect.Value{}, fmt.Errorf("%w: knowledge %q: %w", ErrTypeMismatch, name, err)
	}
	if len(boxed.Features) > 0 {
		if err := json.Unmarshal(boxed.Features, out.Interface()); err != nil {
			return reflect.Value{}, fmt.Errorf("%w: knowledge %q: %w", ErrTypeMismatch, name, err)
		}
	}
	return out.Elem(), nil
}
