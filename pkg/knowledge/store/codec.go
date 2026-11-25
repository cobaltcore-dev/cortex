// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// Entry is an encoded object, identified by its key within its scope.
type Entry struct {
	Key  string
	Data []byte
}

// Key identifying the scope in a store, e.g. "openstack_servers_v4" or
// "kvm_libvirt_domain_metrics/kvm_libvirt_domain_steal_pct".
func (s Scope) Key() string {
	if s.Partition == "" {
		return s.Model.TableName()
	}
	return s.Model.TableName() + "/" + s.Partition
}

// Validate that the scope's partition matches its model.
func (s Scope) Validate() error {
	if s.Model == nil {
		return errors.New("scope has no model")
	}
	if _, ok := s.Model.(PartitionedModel); !ok {
		if s.Partition != "" {
			return fmt.Errorf("model %T is not partitioned", s.Model)
		}
		return nil
	}
	if s.Partition == "" {
		return fmt.Errorf("model %T is partitioned, but no partition is given", s.Model)
	}
	return nil
}

// Encode the objects of a scope, which must be a slice of the scope's model.
//
// Objects are encoded as compact json keyed by their `db` columns, so that
// custom json (un)marshalers of the models, which typically mirror an
// upstream api response, don't drop persisted fields.
func Encode(scope Scope, objs any) ([]Entry, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	modelType := reflect.TypeOf(scope.Model)
	val := reflect.ValueOf(objs)
	if val.Kind() != reflect.Slice || val.Type().Elem() != modelType {
		return nil, fmt.Errorf("expected []%s, got %T", modelType, objs)
	}
	c, err := codecFor(modelType)
	if err != nil {
		return nil, err
	}
	partitionField := -1
	if p, ok := scope.Model.(PartitionedModel); ok {
		idx, ok := c.columns[p.PartitionColumn()]
		if !ok || c.shadow.Field(idx).Type.Kind() != reflect.String {
			return nil, fmt.Errorf("model %s has no string field for partition column %q", modelType, p.PartitionColumn())
		}
		partitionField = c.fields[idx]
	}
	entries := make([]Entry, 0, val.Len())
	seen := make(map[string]struct{}, val.Len())
	for i := range val.Len() {
		obj := val.Index(i)
		// Ensure that replacing one partition never writes into another one.
		if partitionField >= 0 {
			if got := obj.Field(partitionField).String(); got != scope.Partition {
				return nil, fmt.Errorf("object %d belongs to partition %q, expected %q", i, got, scope.Partition)
			}
		}
		entry, err := c.encode(obj)
		if err != nil {
			return nil, fmt.Errorf("failed to encode object %d: %w", i, err)
		}
		if _, ok := seen[entry.Key]; ok {
			if len(c.primaryKeys) > 0 {
				return nil, fmt.Errorf("duplicate primary key %q in %s", entry.Key, scope.Key())
			}
			continue // Identical object of a model without primary key.
		}
		seen[entry.Key] = struct{}{}
		entries = append(entries, entry)
	}
	return entries, nil
}

// Decode encoded objects of a scope into a new slice of the scope's model.
func Decode(scope Scope, data [][]byte) (any, error) {
	modelType := reflect.TypeOf(scope.Model)
	c, err := codecFor(modelType)
	if err != nil {
		return nil, err
	}
	list := reflect.MakeSlice(reflect.SliceOf(modelType), len(data), len(data))
	for i, d := range data {
		if err := c.decode(d, list.Index(i)); err != nil {
			return nil, fmt.Errorf("failed to decode object of %s: %w", scope.Key(), err)
		}
	}
	return list.Interface(), nil
}

// CheckList checks that out is a pointer to a slice of the scope's model.
func CheckList(scope Scope, out any) error {
	modelType := reflect.TypeOf(scope.Model)
	outType := reflect.TypeOf(out)
	if outType == nil || outType.Kind() != reflect.Pointer || outType.Elem().Kind() != reflect.Slice ||
		outType.Elem().Elem() != modelType {
		return fmt.Errorf("expected *[]%s, got %T", modelType, out)
	}
	return nil
}

// SetList copies list, a slice of the scope's model, into out, a pointer to
// a slice of the scope's model. The copy prevents callers from altering the
// list itself, the objects are shared.
func SetList(scope Scope, out, list any) error {
	if err := CheckList(scope, out); err != nil {
		return err
	}
	src := reflect.ValueOf(list)
	dstType := reflect.TypeOf(out).Elem()
	if !src.IsValid() || src.Type() != dstType {
		return fmt.Errorf("expected %s, got %T", dstType, list)
	}
	dst := reflect.MakeSlice(dstType, src.Len(), src.Len())
	reflect.Copy(dst, src)
	reflect.ValueOf(out).Elem().Set(dst)
	return nil
}

// Encoding of one model type.
type codec struct {
	// Struct mirroring the persisted fields of the model with their columns
	// as json names.
	shadow reflect.Type
	// Model field index by shadow field index.
	fields []int
	// Shadow field index by column name.
	columns map[string]int
	// Model field indexes of the primary key, in declaration order.
	primaryKeys []int
}

var codecs sync.Map // map[reflect.Type]*codec

func codecFor(t reflect.Type) (*codec, error) {
	if c, ok := codecs.Load(t); ok {
		return c.(*codec), nil
	}
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("model %s is not a struct", t)
	}
	c := &codec{columns: map[string]int{}}
	var shadowFields []reflect.StructField
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("db")
		if tag == "-" {
			continue
		}
		column, opts, _ := strings.Cut(tag, ",")
		if column == "" {
			column = f.Name
		}
		if _, ok := c.columns[column]; ok {
			return nil, fmt.Errorf("model %s has duplicate column %q", t, column)
		}
		if slices.Contains(strings.Split(opts, ","), "primarykey") {
			c.primaryKeys = append(c.primaryKeys, i)
		}
		c.columns[column] = len(shadowFields)
		c.fields = append(c.fields, i)
		shadowFields = append(shadowFields, reflect.StructField{
			Name: "F" + strconv.Itoa(len(shadowFields)),
			Type: shadowType(f.Type),
			Tag:  reflect.StructTag(`json:"` + column + `"`),
		})
	}
	c.shadow = reflect.StructOf(shadowFields)
	actual, _ := codecs.LoadOrStore(t, c)
	return actual.(*codec), nil
}

func (c *codec) encode(obj reflect.Value) (Entry, error) {
	shadow := reflect.New(c.shadow).Elem()
	for i, fieldIdx := range c.fields {
		shadow.Field(i).Set(obj.Field(fieldIdx).Convert(c.shadow.Field(i).Type))
	}
	data, err := json.Marshal(shadow.Interface())
	if err != nil {
		return Entry{}, err
	}
	key, err := c.key(obj, data)
	if err != nil {
		return Entry{}, err
	}
	return Entry{Key: key, Data: data}, nil
}

func (c *codec) decode(data []byte, obj reflect.Value) error {
	shadow := reflect.New(c.shadow)
	if err := json.Unmarshal(data, shadow.Interface()); err != nil {
		return err
	}
	for i, fieldIdx := range c.fields {
		field := obj.Field(fieldIdx)
		field.Set(shadow.Elem().Field(i).Convert(field.Type()))
	}
	return nil
}

// Key of an object: its primary key, encoded as json array for composite
// keys, or a hash of its content if the model has no primary key.
func (c *codec) key(obj reflect.Value, data []byte) (string, error) {
	switch len(c.primaryKeys) {
	case 0:
		sum := sha256.Sum256(data)
		return hex.EncodeToString(sum[:16]), nil
	case 1:
		if v := obj.Field(c.primaryKeys[0]); v.Kind() == reflect.String {
			return v.String(), nil
		}
	}
	values := make([]any, len(c.primaryKeys))
	for i, idx := range c.primaryKeys {
		values[i] = obj.Field(idx).Interface()
	}
	var key []byte
	var err error
	if len(values) == 1 {
		key, err = json.Marshal(values[0])
	} else {
		key, err = json.Marshal(values)
	}
	return string(key), err
}

// Floats which also encode NaN and infinities, which are not valid json
// numbers but may be returned e.g. by prometheus.
type (
	jsonFloat64 float64
	jsonFloat32 float32
)

func shadowType(t reflect.Type) reflect.Type {
	switch t {
	case reflect.TypeFor[float64]():
		return reflect.TypeFor[jsonFloat64]()
	case reflect.TypeFor[float32]():
		return reflect.TypeFor[jsonFloat32]()
	case reflect.TypeFor[*float64]():
		return reflect.TypeFor[*jsonFloat64]()
	case reflect.TypeFor[*float32]():
		return reflect.TypeFor[*jsonFloat32]()
	}
	return t
}

func marshalFloat(f float64, bitSize int) []byte {
	switch {
	case math.IsNaN(f):
		return []byte(`"NaN"`)
	case math.IsInf(f, 1):
		return []byte(`"+Inf"`)
	case math.IsInf(f, -1):
		return []byte(`"-Inf"`)
	}
	return strconv.AppendFloat(nil, f, 'g', -1, bitSize)
}

func unmarshalFloat(data []byte, bitSize int) (float64, error) {
	return strconv.ParseFloat(string(bytes.Trim(data, `"`)), bitSize)
}

func (f jsonFloat64) MarshalJSON() ([]byte, error) { return marshalFloat(float64(f), 64), nil }
func (f jsonFloat32) MarshalJSON() ([]byte, error) { return marshalFloat(float64(f), 32), nil }

func (f *jsonFloat64) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	v, err := unmarshalFloat(data, 64)
	*f = jsonFloat64(v)
	return err
}

func (f *jsonFloat32) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	v, err := unmarshalFloat(data, 32)
	*f = jsonFloat32(v)
	return err
}
