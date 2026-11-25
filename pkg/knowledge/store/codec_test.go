// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

type codecModel struct {
	ID       string     `json:"id" db:"id,primarykey"`
	Value    float64    `json:"value" db:"value"`
	Optional *float64   `json:"optional" db:"optional"`
	Created  time.Time  `json:"created" db:"created"`
	Hidden   string     `json:"-" db:"hidden"`
	Ignored  string     `db:"-"`
	Nested   *string    `json:"nested" db:"nested"`
	Count    uint64     `json:"count" db:"count"`
	unexp    int        //nolint:unused // must be ignored by the codec
	Small    float32    `db:"small"`
	PtrSmall *float32   `db:"ptr_small"`
	When     *time.Time `db:"when"`
}

func (codecModel) TableName() string            { return "codec_models" }
func (codecModel) Indexes() map[string][]string { return nil }

// Mirrors upstream api models whose json marshalers drop persisted fields.
func (m codecModel) MarshalJSON() ([]byte, error) { return json.Marshal(map[string]string{"id": m.ID}) }

type compositeModel struct {
	A string `db:"a,primarykey"`
	B int    `db:"b,primarykey"`
}

func (compositeModel) TableName() string            { return "composite_models" }
func (compositeModel) Indexes() map[string][]string { return nil }

type contentModel struct {
	Host  string  `db:"host"`
	Value float64 `db:"value"`
}

func (contentModel) TableName() string            { return "content_models" }
func (contentModel) Indexes() map[string][]string { return nil }

type partitionedModel struct {
	Name  string `db:"name"`
	Value int    `db:"value"`
}

func (partitionedModel) TableName() string            { return "partitioned_models" }
func (partitionedModel) Indexes() map[string][]string { return nil }
func (partitionedModel) PartitionColumn() string      { return "name" }

func roundTrip[T Model](t *testing.T, scope Scope, objs []T) []T {
	t.Helper()
	entries, err := Encode(scope, objs)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	data := make([][]byte, len(entries))
	for i, e := range entries {
		data[i] = e.Data
	}
	list, err := Decode(scope, data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	var out []T
	if err := SetList(scope, &out, list); err != nil {
		t.Fatalf("set list: %v", err)
	}
	return out
}

func TestCodecRoundTrip(t *testing.T) {
	created := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	objs := []codecModel{
		{
			ID: "a", Value: 1.5, Optional: new(math.Inf(-1)), Created: created, Hidden: "kept",
			Ignored: "dropped", Nested: new("n"), Count: math.MaxUint64, Small: 0.25,
			PtrSmall: new(float32(2)), When: &created,
		},
		{ID: "b", Value: math.NaN(), Small: float32(math.Inf(1))},
	}
	out := roundTrip(t, Scope{Model: codecModel{}}, objs)
	if len(out) != 2 {
		t.Fatalf("expected 2 objects, got %d", len(out))
	}
	byID := map[string]codecModel{out[0].ID: out[0], out[1].ID: out[1]}
	a, b := byID["a"], byID["b"]
	if a.Hidden != "kept" || !a.Created.Equal(created) || !a.When.Equal(created) ||
		a.Value != 1.5 || !math.IsInf(*a.Optional, -1) || *a.Nested != "n" || a.Count != math.MaxUint64 ||
		a.Small != 0.25 || *a.PtrSmall != 2 || a.Ignored != "" {
		t.Errorf("unexpected object a: %+v", a)
	}
	if !math.IsNaN(b.Value) || !math.IsInf(float64(b.Small), 1) || b.Optional != nil || b.When != nil {
		t.Errorf("unexpected object b: %+v", b)
	}
}

func TestEncodeKeys(t *testing.T) {
	tests := []struct {
		name    string
		scope   Scope
		objs    any
		keys    []string
		wantErr string
	}{
		{
			name:  "string primary key",
			scope: Scope{Model: codecModel{}},
			objs:  []codecModel{{ID: "uuid-1"}},
			keys:  []string{"uuid-1"},
		},
		{
			name:  "composite primary key",
			scope: Scope{Model: compositeModel{}},
			objs:  []compositeModel{{A: "x/y", B: 1}},
			keys:  []string{`["x/y",1]`},
		},
		{
			name:    "duplicate primary key",
			scope:   Scope{Model: codecModel{}},
			objs:    []codecModel{{ID: "a", Value: 1}, {ID: "a", Value: 2}},
			wantErr: "duplicate primary key",
		},
		{
			name:  "identical objects without primary key are merged",
			scope: Scope{Model: contentModel{}},
			objs:  []contentModel{{Host: "h", Value: 1}, {Host: "h", Value: 1}, {Host: "h", Value: 2}},
		},
		{
			name:    "wrong slice type",
			scope:   Scope{Model: codecModel{}},
			objs:    []contentModel{},
			wantErr: "expected []store.codecModel",
		},
		{
			name:    "object of another partition",
			scope:   Scope{Model: partitionedModel{}, Partition: "p1"},
			objs:    []partitionedModel{{Name: "p1"}, {Name: "p2"}},
			wantErr: `belongs to partition "p2"`,
		},
		{
			name:    "partitioned model without partition",
			scope:   Scope{Model: partitionedModel{}},
			objs:    []partitionedModel{},
			wantErr: "no partition is given",
		},
		{
			name:    "partition on unpartitioned model",
			scope:   Scope{Model: codecModel{}, Partition: "p1"},
			objs:    []codecModel{},
			wantErr: "is not partitioned",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries, err := Encode(tt.scope, tt.objs)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.keys == nil {
				if len(entries) != 2 {
					t.Fatalf("expected 2 merged entries, got %d", len(entries))
				}
				return
			}
			for i, key := range tt.keys {
				if entries[i].Key != key {
					t.Errorf("expected key %q, got %q", key, entries[i].Key)
				}
			}
		})
	}
}

func TestDecodeToleratesSchemaChanges(t *testing.T) {
	data := [][]byte{[]byte(`{"id":"a","unknown_column":1}`)}
	list, err := Decode(Scope{Model: codecModel{}}, data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := list.([]codecModel); len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("unexpected objects: %+v", got)
	}
}
