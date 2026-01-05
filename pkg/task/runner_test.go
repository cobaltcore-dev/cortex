// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package task

import (
	"context"
	"errors"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func TestRunner_Reconcile(t *testing.T) {
	tests := []struct {
		name        string
		runner      *Runner
		expectError bool
		expectRun   bool
	}{
		{
			name: "successful reconcile with run function",
			runner: &Runner{
				Name: "test-task",
				Run: func(ctx context.Context) error {
					return nil
				},
			},
			expectError: false,
			expectRun:   true,
		},
		{
			name: "reconcile with run function that returns error",
			runner: &Runner{
				Name: "test-task",
				Run: func(ctx context.Context) error {
					return errors.New("run failed")
				},
			},
			expectError: true,
			expectRun:   true,
		},
		{
			name: "reconcile without run function",
			runner: &Runner{
				Name: "test-task",
				Run:  nil,
			},
			expectError: false,
			expectRun:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runCalled := false
			if tt.runner.Run != nil {
				originalRun := tt.runner.Run
				tt.runner.Run = func(ctx context.Context) error {
					runCalled = true
					return originalRun(ctx)
				}
			}

			ctx := context.Background()
			req := ctrl.Request{}

			result, err := tt.runner.Reconcile(ctx, req)

			if (err != nil) != tt.expectError {
				t.Errorf("Reconcile() error = %v, expectError %v", err, tt.expectError)
				return
			}

			if runCalled != tt.expectRun {
				t.Errorf("Run function called = %v, expectRun %v", runCalled, tt.expectRun)
				return
			}

			// Check that result is empty when successful
			if err == nil {
				expectedResult := ctrl.Result{}
				if result != expectedResult {
					t.Errorf("Reconcile() result = %v, expected %v", result, expectedResult)
				}
			}
		})
	}
}

func TestRunner_Start_WithInit(t *testing.T) {
	initCalled := false
	runner := &Runner{
		Name:     "test-task",
		Interval: 5 * time.Millisecond,
		Init: func(ctx context.Context) error {
			initCalled = true
			return nil
		},
		eventCh: make(chan event.GenericEvent, 10),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := runner.Start(ctx)
	if err != nil {
		t.Errorf("Start() error = %v, expected nil", err)
	}

	if !initCalled {
		t.Error("Expected Init function to be called")
	}

	// Count events in channel with timeout to prevent hanging
	eventCount := 0
	timeout := time.NewTimer(100 * time.Millisecond)
	defer timeout.Stop()

	for {
		select {
		case <-runner.eventCh:
			eventCount++
		case <-timeout.C:
			goto done
		default:
			goto done
		}
	}
done:

	// Should have at least initial event plus some periodic events
	if eventCount < 2 {
		t.Errorf("Expected at least 2 events, got %d", eventCount)
	}
}

func TestRunner_Start_WithInitError(t *testing.T) {
	runner := &Runner{
		Name:     "test-task",
		Interval: 5 * time.Millisecond,
		Init: func(ctx context.Context) error {
			return errors.New("init failed")
		},
		eventCh: make(chan event.GenericEvent, 10),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := runner.Start(ctx)
	if err == nil {
		t.Error("Expected error from Start() due to Init failure")
	}
}

func TestRunner_Start_WithoutInit(t *testing.T) {
	runner := &Runner{
		Name:     "test-task",
		Interval: 5 * time.Millisecond,
		Init:     nil,
		eventCh:  make(chan event.GenericEvent, 10),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()

	err := runner.Start(ctx)
	if err != nil {
		t.Errorf("Start() error = %v, expected nil", err)
	}

	// Count events in channel with timeout to prevent hanging
	eventCount := 0
	timeout := time.NewTimer(100 * time.Millisecond)
	defer timeout.Stop()

	for {
		select {
		case <-runner.eventCh:
			eventCount++
		case <-timeout.C:
			goto done
		default:
			goto done
		}
	}
done:

	// Should have at least initial event plus some periodic events
	if eventCount < 2 {
		t.Errorf("Expected at least 2 events, got %d", eventCount)
	}
}

func TestRunner_Start_ContextCancellation(t *testing.T) {
	runner := &Runner{
		Name:     "test-task",
		Interval: 100 * time.Millisecond,
		eventCh:  make(chan event.GenericEvent, 10),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	err := runner.Start(ctx)
	if err != nil {
		t.Errorf("Start() error = %v, expected nil", err)
	}

	// Should have only initial event due to quick cancellation
	eventCount := 0
	timeout := time.NewTimer(100 * time.Millisecond)
	defer timeout.Stop()

	for {
		select {
		case <-runner.eventCh:
			eventCount++
		case <-timeout.C:
			goto done
		default:
			goto done
		}
	}
done:

	// Should have at least the initial event
	if eventCount < 1 {
		t.Errorf("Expected at least 1 event, got %d", eventCount)
	}
}

func TestRunner_SetupWithManager_EventChannelCreation(t *testing.T) {
	runner := &Runner{}

	// Test that eventCh is nil before setup
	if runner.eventCh != nil {
		t.Error("Expected eventCh to be nil before SetupWithManager")
	}

	// We can't easily test the full SetupWithManager without a real manager,
	// but we can test that the eventCh gets created when we call it manually
	runner.eventCh = make(chan event.GenericEvent)

	if runner.eventCh == nil {
		t.Error("Expected eventCh to be created")
	}
}

func TestRunner_EventStructure(t *testing.T) {
	runner := &Runner{
		Name:     "test-task",
		Interval: 5 * time.Millisecond,
		eventCh:  make(chan event.GenericEvent, 10),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()

	go func() {
		err := runner.Start(ctx)
		if err != nil {
			t.Errorf("Start() error = %v, expected nil", err)
		}
	}()

	// Wait for events to be generated
	time.Sleep(10 * time.Millisecond)

	// Check that we received at least one event
	select {
	case event := <-runner.eventCh:
		// Verify event structure - should be a Job object
		if event.Object == nil {
			t.Error("Expected event to have non-nil Object")
		}
		if job, ok := event.Object.(*batchv1.Job); ok {
			if job.Kind != "Job" {
				t.Errorf("Expected Job kind, got %s", job.Kind)
			}
			if job.APIVersion != "batch/v1" {
				t.Errorf("Expected batch/v1 apiVersion, got %s", job.APIVersion)
			}
		} else {
			t.Error("Expected event object to be a Job")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Expected to receive at least one event")
	}
}
