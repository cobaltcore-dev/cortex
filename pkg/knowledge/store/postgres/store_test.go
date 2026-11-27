// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"testing"

	"github.com/cobaltcore-dev/cortex/pkg/knowledge/store"
	testlibDB "github.com/cobaltcore-dev/cortex/pkg/knowledge/store/postgres/testing"
)

type partitionedMock struct {
	Name  string `db:"name,primarykey"`
	Value int    `db:"value,primarykey"`
}

func (partitionedMock) TableName() string            { return "partitioned_mock" }
func (partitionedMock) Indexes() map[string][]string { return nil }
func (partitionedMock) PartitionColumn() string      { return "name" }

func TestStore_ReplaceAllAndListAll(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	defer dbEnv.Close()
	s := NewStore(&DB{DbMap: dbEnv.DbMap})
	ctx := context.Background()

	// Unpartitioned models are replaced as a whole.
	if err := store.ReplaceAll(ctx, s, "", []MockTable{{ID: 1, Name: "a"}, {ID: 2, Name: "b"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceAll(ctx, s, "", []MockTable{{ID: 3, Name: "c"}}); err != nil {
		t.Fatal(err)
	}
	got, err := store.ListAll[MockTable](ctx, s, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != 3 {
		t.Fatalf("expected only the replaced object, got %v", got)
	}

	// Partitioned models only replace their own partition.
	if err := store.ReplaceAll(ctx, s, "p1", []partitionedMock{{"p1", 1}, {"p1", 2}}); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceAll(ctx, s, "p2", []partitionedMock{{"p2", 1}}); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceAll(ctx, s, "p1", []partitionedMock{{"p1", 3}}); err != nil {
		t.Fatal(err)
	}
	p1, err := store.ListAll[partitionedMock](ctx, s, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(p1) != 1 || p1[0].Value != 3 {
		t.Fatalf("expected p1 to be replaced, got %v", p1)
	}
	p2, err := store.ListAll[partitionedMock](ctx, s, "p2")
	if err != nil {
		t.Fatal(err)
	}
	if len(p2) != 1 || p2[0].Value != 1 {
		t.Fatalf("expected p2 to be untouched, got %v", p2)
	}
}

func TestStore_Errors(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	defer dbEnv.Close()
	s := NewStore(&DB{DbMap: dbEnv.DbMap})
	ctx := context.Background()

	tests := []struct {
		name string
		call func() error
	}{
		{"object outside partition", func() error {
			return store.ReplaceAll(ctx, s, "p1", []partitionedMock{{"p2", 1}})
		}},
		{"partitioned model without partition", func() error {
			return store.ReplaceAll(ctx, s, "", []partitionedMock{})
		}},
		{"partition on unpartitioned model", func() error {
			_, err := store.ListAll[MockTable](ctx, s, "p1")
			return err
		}},
		{"wrong list type", func() error {
			var out []partitionedMock
			return s.ListAll(ctx, store.Scope{Model: MockTable{}}, &out)
		}},
		{"wrong replace type", func() error {
			return s.ReplaceAll(ctx, store.Scope{Model: MockTable{}}, []partitionedMock{})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
