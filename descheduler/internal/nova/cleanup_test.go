// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

import (
	"context"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/descheduler/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestCleanup_Reconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	v1alpha1.AddToScheme(scheme)

	now := metav1.Now()
	oneHourAgo := metav1.Time{Time: now.Add(-time.Hour)}
	twentyThreeHoursAgo := metav1.Time{Time: now.Add(-23 * time.Hour)}
	twentyFiveHoursAgo := metav1.Time{Time: now.Add(-25 * time.Hour)}
	twoDaysAgo := metav1.Time{Time: now.Add(-48 * time.Hour)}

	tests := []struct {
		name               string
		descheduling       *v1alpha1.Descheduling
		expectDelete       bool
		expectRequeue      bool
		expectedRequeueAge time.Duration // Approximate expected requeue duration
	}{
		{
			name: "delete old completed descheduling",
			descheduling: &v1alpha1.Descheduling{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "old-completed",
					Namespace:         "default",
					CreationTimestamp: twentyFiveHoursAgo,
				},
				Status: v1alpha1.DeschedulingStatus{
					Phase: v1alpha1.DeschedulingStatusPhaseCompleted,
				},
			},
			expectDelete:  true,
			expectRequeue: false,
		},
		{
			name: "delete old failed descheduling",
			descheduling: &v1alpha1.Descheduling{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "old-failed",
					Namespace:         "default",
					CreationTimestamp: twoDaysAgo,
				},
				Status: v1alpha1.DeschedulingStatus{
					Phase: v1alpha1.DeschedulingStatusPhaseFailed,
					Error: "migration failed",
				},
			},
			expectDelete:  true,
			expectRequeue: false,
		},
		{
			name: "delete old in-progress descheduling",
			descheduling: &v1alpha1.Descheduling{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "old-in-progress",
					Namespace:         "default",
					CreationTimestamp: twentyFiveHoursAgo,
				},
				Status: v1alpha1.DeschedulingStatus{
					Phase: v1alpha1.DeschedulingStatusPhaseInProgress,
				},
			},
			expectDelete:  true,
			expectRequeue: false,
		},
		{
			name: "requeue recent descheduling - one hour old",
			descheduling: &v1alpha1.Descheduling{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "recent-one-hour",
					Namespace:         "default",
					CreationTimestamp: oneHourAgo,
				},
				Status: v1alpha1.DeschedulingStatus{
					Phase: v1alpha1.DeschedulingStatusPhaseCompleted,
				},
			},
			expectDelete:       false,
			expectRequeue:      true,
			expectedRequeueAge: 23 * time.Hour, // Approximately 23 hours remaining
		},
		{
			name: "requeue recent descheduling - 23 hours old",
			descheduling: &v1alpha1.Descheduling{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "recent-twenty-three-hours",
					Namespace:         "default",
					CreationTimestamp: twentyThreeHoursAgo,
				},
				Status: v1alpha1.DeschedulingStatus{
					Phase: v1alpha1.DeschedulingStatusPhaseQueued,
				},
			},
			expectDelete:       false,
			expectRequeue:      true,
			expectedRequeueAge: time.Hour, // Approximately 1 hour remaining
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create fake client with the descheduling object
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.descheduling).
				Build()

			cleanup := &Cleanup{
				Client: fakeClient,
				Scheme: scheme,
			}

			ctx := context.Background()
			req := reconcile.Request{
				NamespacedName: client.ObjectKey{
					Name:      tt.descheduling.Name,
					Namespace: tt.descheduling.Namespace,
				},
			}

			result, err := cleanup.Reconcile(ctx, req)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			// Check if descheduling was deleted
			var desc v1alpha1.Descheduling
			getErr := fakeClient.Get(ctx, req.NamespacedName, &desc)

			if tt.expectDelete {
				if getErr == nil {
					t.Error("expected descheduling to be deleted, but it still exists")
				}
				if result.RequeueAfter != 0 {
					t.Errorf("expected no requeue for deleted resource, got requeue after %v", result.RequeueAfter)
				}
			} else {
				if getErr != nil {
					t.Errorf("expected descheduling to exist, but got error: %v", getErr)
				}
				if !tt.expectRequeue {
					if result.RequeueAfter != 0 {
						t.Errorf("expected no requeue, got requeue after %v", result.RequeueAfter)
					}
				} else {
					if result.RequeueAfter == 0 {
						t.Error("expected requeue but got none")
					}
					// Check that requeue time is approximately correct (within 1 minute tolerance)
					tolerance := time.Minute
					if result.RequeueAfter < tt.expectedRequeueAge-tolerance || result.RequeueAfter > tt.expectedRequeueAge+tolerance {
						t.Errorf("expected requeue after approximately %v, got %v", tt.expectedRequeueAge, result.RequeueAfter)
					}
				}
			}
		})
	}
}

func TestCleanup_Reconcile_NonexistentResource(t *testing.T) {
	scheme := runtime.NewScheme()
	v1alpha1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	cleanup := &Cleanup{
		Client: fakeClient,
		Scheme: scheme,
	}

	ctx := context.Background()
	req := reconcile.Request{
		NamespacedName: client.ObjectKey{
			Name:      "nonexistent",
			Namespace: "default",
		},
	}

	result, err := cleanup.Reconcile(ctx, req)

	if err != nil {
		t.Errorf("expected no error for nonexistent resource, got: %v", err)
	}

	if result.RequeueAfter != 0 {
		t.Errorf("expected no requeue for nonexistent resource, got requeue after %v", result.RequeueAfter)
	}
}

func TestCleanupOnStartup_Start(t *testing.T) {
	scheme := runtime.NewScheme()
	v1alpha1.AddToScheme(scheme)

	now := metav1.Now()
	oneHourAgo := metav1.Time{Time: now.Add(-time.Hour)}
	twentyFiveHoursAgo := metav1.Time{Time: now.Add(-25 * time.Hour)}

	deschedulings := []v1alpha1.Descheduling{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "old-completed",
				Namespace:         "default",
				CreationTimestamp: twentyFiveHoursAgo,
			},
			Status: v1alpha1.DeschedulingStatus{
				Phase: v1alpha1.DeschedulingStatusPhaseCompleted,
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "recent-completed",
				Namespace:         "default",
				CreationTimestamp: oneHourAgo,
			},
			Status: v1alpha1.DeschedulingStatus{
				Phase: v1alpha1.DeschedulingStatusPhaseCompleted,
			},
		},
	}

	// Convert slice to client objects
	objects := make([]client.Object, len(deschedulings))
	for i, desc := range deschedulings {
		descCopy := desc // Create a copy to avoid pointer issues
		objects[i] = &descCopy
	}

	// Create fake client with the descheduling objects
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		Build()

	cleanup := &Cleanup{
		Client: fakeClient,
		Scheme: scheme,
	}

	cleanupOnStartup := &CleanupOnStartup{
		Cleanup: cleanup,
	}

	ctx := context.Background()
	err := cleanupOnStartup.Start(ctx)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
		return
	}

	// Check that old descheduling was deleted
	var oldDesc v1alpha1.Descheduling
	err = fakeClient.Get(ctx, client.ObjectKey{
		Name:      "old-completed",
		Namespace: "default",
	}, &oldDesc)
	if err == nil {
		t.Error("expected old descheduling to be deleted, but it still exists")
	}

	// Check that recent descheduling still exists
	var recentDesc v1alpha1.Descheduling
	err = fakeClient.Get(ctx, client.ObjectKey{
		Name:      "recent-completed",
		Namespace: "default",
	}, &recentDesc)
	if err != nil {
		t.Errorf("expected recent descheduling to exist, but got error: %v", err)
	}
}

func TestCleanupOnStartup_Start_EmptyList(t *testing.T) {
	scheme := runtime.NewScheme()
	v1alpha1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	cleanup := &Cleanup{
		Client: fakeClient,
		Scheme: scheme,
	}

	cleanupOnStartup := &CleanupOnStartup{
		Cleanup: cleanup,
	}

	ctx := context.Background()
	err := cleanupOnStartup.Start(ctx)

	if err != nil {
		t.Errorf("unexpected error for empty list: %v", err)
	}
}
