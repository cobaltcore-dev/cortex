// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package valkey

import (
	"errors"
	"strconv"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/store"
	"github.com/valkey-io/valkey-go"
)

type testModel struct {
	ID    string `db:"id,primarykey"`
	Value int    `db:"value"`
}

func (testModel) TableName() string            { return "test_models" }
func (testModel) Indexes() map[string][]string { return nil }

func setup(t *testing.T) (*miniredis.Miniredis, *Store) {
	t.Helper()
	mr := miniredis.RunT(t)
	client, err := valkey.NewClient(valkey.ClientOption{
		InitAddress: []string{mr.Addr()}, DisableCache: true, ForceSingleClient: true,
	})
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	t.Cleanup(client.Close)
	return mr, New(client)
}

func objects(n, value int) []testModel {
	objs := make([]testModel, n)
	for i := range objs {
		objs[i] = testModel{ID: strconv.Itoa(i), Value: value}
	}
	return objs
}

func TestStore_ReplaceAndList(t *testing.T) {
	mr, s := setup(t)
	ctx := t.Context()
	scope := store.Scope{Model: testModel{}}

	if _, err := store.ListAll[testModel](ctx, s, ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if ok, err := s.Exists(ctx, scope); err != nil || ok {
		t.Fatalf("expected scope to not exist, got %v, %v", ok, err)
	}

	// More objects than one batch, so that writes and scans are paginated.
	if err := store.ReplaceAll(ctx, s, "", objects(2*batchSize+1, 1)); err != nil {
		t.Fatalf("replace: %v", err)
	}
	firstGen := mr.Keys()
	got, err := store.ListAll[testModel](ctx, s, "")
	if err != nil || len(got) != 2*batchSize+1 {
		t.Fatalf("expected %d objects, got %d, %v", 2*batchSize+1, len(got), err)
	}
	// Single objects are addressable by their key.
	if v := mr.HGet(firstGen[1], "42"); v != `{"id":"42","value":1}` {
		t.Fatalf("unexpected stored object %q in %v", v, firstGen)
	}

	if err := store.ReplaceAll(ctx, s, "", objects(1, 2)); err != nil {
		t.Fatalf("replace: %v", err)
	}
	got, err = store.ListAll[testModel](ctx, s, "")
	if err != nil || len(got) != 1 || got[0].Value != 2 {
		t.Fatalf("expected replaced object, got %+v, %v", got, err)
	}
	// The previous generation stays readable for a grace period only.
	if ttl := mr.TTL(firstGen[1]); ttl != graceTTL {
		t.Fatalf("expected previous generation to expire in %v, got %v", graceTTL, ttl)
	}
	mr.FastForward(graceTTL)
	if mr.Exists(firstGen[1]) {
		t.Fatal("expected previous generation to be expired")
	}

	// Empty lists are stored, too.
	if err := store.ReplaceAll(ctx, s, "", []testModel{}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	got, err = store.ListAll[testModel](ctx, s, "")
	if err != nil || len(got) != 0 {
		t.Fatalf("expected empty list, got %+v, %v", got, err)
	}
}

func TestStore_CacheIsInvalidatedOnRestart(t *testing.T) {
	mr, s := setup(t)
	ctx := t.Context()
	if err := store.ReplaceAll(ctx, s, "", objects(3, 1)); err != nil {
		t.Fatalf("replace: %v", err)
	}
	for range 2 {
		if got, err := store.ListAll[testModel](ctx, s, ""); err != nil || len(got) != 3 {
			t.Fatalf("expected 3 objects, got %d, %v", len(got), err)
		}
	}
	// Mutating a listed slice must not affect the cache.
	got, err := store.ListAll[testModel](ctx, s, "")
	if err != nil {
		t.Fatal(err)
	}
	got[0] = testModel{ID: "mutated"}
	again, err := store.ListAll[testModel](ctx, s, "")
	if err != nil || again[0].ID == "mutated" {
		t.Fatalf("cache was mutated through a listed slice: %v", err)
	}

	mr.FlushAll()
	if _, err := store.ListAll[testModel](ctx, s, ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after data loss, got %v", err)
	}
	if ok, err := s.Exists(ctx, store.Scope{Model: testModel{}}); err != nil || ok {
		t.Fatalf("expected scope to not exist after data loss, got %v, %v", ok, err)
	}
}

func TestStore_ListDetectsExpiredGeneration(t *testing.T) {
	mr, s := setup(t)
	ctx := t.Context()
	if err := store.ReplaceAll(ctx, s, "", objects(3, 1)); err != nil {
		t.Fatalf("replace: %v", err)
	}
	// Simulate that the generation vanished, e.g. a reader that took longer
	// than the grace period.
	for _, k := range mr.Keys() {
		if k != currentKey(store.Scope{Model: testModel{}}) {
			mr.Del(k)
		}
	}
	if _, err := store.ListAll[testModel](ctx, s, ""); err == nil {
		t.Fatal("expected error for a vanished generation")
	}
}
