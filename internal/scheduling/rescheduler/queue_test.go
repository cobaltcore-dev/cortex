// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package rescheduler

import (
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func key(name string) client.ObjectKey { return client.ObjectKey{Name: name} }

func TestFIFOQueue_Order(t *testing.T) {
	q := NewFIFOQueue()
	q.Add(key("a"))
	q.Add(key("b"))
	q.Add(key("c"))

	for _, want := range []string{"a", "b", "c"} {
		if got, ok := q.Pop(); !ok || got.Name != want {
			t.Fatalf("expected pop %s, got %v ok=%v", want, got, ok)
		}
	}
}

func TestFIFOQueue_PopBlocksUntilAdd(t *testing.T) {
	q := NewFIFOQueue()
	done := make(chan client.ObjectKey, 1)
	go func() {
		got, _ := q.Pop() // blocks until Add
		done <- got
	}()

	select {
	case <-done:
		t.Fatal("Pop returned before any item was added")
	case <-time.After(20 * time.Millisecond):
	}

	q.Add(key("a"))
	select {
	case got := <-done:
		if got.Name != "a" {
			t.Fatalf("expected a, got %v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("Pop did not return after Add")
	}
}

func TestFIFOQueue_CloseUnblocksPop(t *testing.T) {
	q := NewFIFOQueue()
	done := make(chan bool, 1)
	go func() {
		_, ok := q.Pop()
		done <- ok
	}()

	q.Close()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("expected Pop to report closed (ok=false)")
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not unblock Pop")
	}
	// Add after close is a no-op, and Pop keeps reporting closed.
	q.Add(key("a"))
	if _, ok := q.Pop(); ok {
		t.Fatal("expected no items after close")
	}
}
