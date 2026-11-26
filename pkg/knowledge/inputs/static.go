// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package inputs

import (
	"context"
	"fmt"
	"reflect"
)

// Static resolves inputs from fixed lists of objects keyed by var, e.g. to
// run extractors or kpis on objects that are already in memory.
type Static map[string]any

// Resolve fills the fields of target, a pointer to a struct, that are tagged
// with `input:"<var>"` with the objects of that var. All tagged fields must
// have objects of the same type, and all objects must be consumed.
func (s Static) Resolve(_ context.Context, target any) error {
	val := reflect.ValueOf(target)
	if val.Kind() != reflect.Pointer || val.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("expected pointer to struct, got %T", target)
	}
	fields, err := Fields(val.Elem().Type())
	if err != nil {
		return err
	}
	for v := range s {
		if _, ok := fields[v]; !ok {
			return fmt.Errorf("%w: %q", ErrUnused, v)
		}
	}
	for v, idx := range fields {
		objs, ok := s[v]
		if !ok {
			return fmt.Errorf("%w: %q", ErrMissing, v)
		}
		field := val.Elem().Field(idx)
		objsVal := reflect.ValueOf(objs)
		if !objsVal.IsValid() {
			field.SetZero()
			continue
		}
		if objsVal.Type() != field.Type() {
			return fmt.Errorf("%w: %q provides %s, expected %s", ErrTypeMismatch, v, objsVal.Type(), field.Type())
		}
		field.Set(objsVal)
	}
	return nil
}
