// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package manila

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"

	corev1 "k8s.io/api/core/v1"
)

func TestCleanupManila(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add corev1 scheme: %v", err)
	}

	tests := []struct {
		name             string
		histories        []v1alpha1.History
		expectError      bool
		authError        bool
		endpointError    bool
		mockServerError  bool
		emptySharesError bool
		mockShares       []mockShare
		expectedDeleted  []string
	}{
		{
			name:        "handle authentication error",
			histories:   []v1alpha1.History{},
			authError:   true,
			expectError: true,
		},
		{
			name:          "handle endpoint error",
			histories:     []v1alpha1.History{},
			endpointError: true,
			expectError:   true,
		},
		{
			name:            "handle server error",
			histories:       []v1alpha1.History{},
			mockServerError: true,
			expectError:     true,
		},
		{
			name:             "handle empty shares case",
			histories:        []v1alpha1.History{},
			emptySharesError: true,
			expectError:      true,
		},
		{
			name: "delete history for non-existent shares",
			histories: []v1alpha1.History{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "history-existing-share",
					},
					Spec: v1alpha1.HistorySpec{
						SchedulingDomain: v1alpha1.SchedulingDomainManila,
						ResourceID:       "share-exists",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "history-deleted-share",
					},
					Spec: v1alpha1.HistorySpec{
						SchedulingDomain: v1alpha1.SchedulingDomainManila,
						ResourceID:       "share-deleted",
					},
				},
			},
			mockShares: []mockShare{
				{ID: "share-exists"},
			},
			expectedDeleted: []string{"history-deleted-share"},
			expectError:     false,
		},
		{
			name: "keep history for existing shares",
			histories: []v1alpha1.History{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "history-share-1",
					},
					Spec: v1alpha1.HistorySpec{
						SchedulingDomain: v1alpha1.SchedulingDomainManila,
						ResourceID:       "share-1",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "history-share-2",
					},
					Spec: v1alpha1.HistorySpec{
						SchedulingDomain: v1alpha1.SchedulingDomainManila,
						ResourceID:       "share-2",
					},
				},
			},
			mockShares: []mockShare{
				{ID: "share-1"},
				{ID: "share-2"},
			},
			expectedDeleted: []string{},
			expectError:     false,
		},
		{
			name: "skip non-manila histories",
			histories: []v1alpha1.History{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "history-other-type",
					},
					Spec: v1alpha1.HistorySpec{
						SchedulingDomain: v1alpha1.SchedulingDomainCinder,
						ResourceID:       "some-resource",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "history-other-operator",
					},
					Spec: v1alpha1.HistorySpec{
						SchedulingDomain: "other-operator",
						ResourceID:       "share-1",
					},
				},
			},
			mockShares:      []mockShare{{ID: "dummy-share"}}, // Add at least one share to avoid "no shares found" error
			expectedDeleted: []string{},
			expectError:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := make([]client.Object, len(tt.histories))
			for i := range tt.histories {
				objects[i] = &tt.histories[i]
			}

			// Create mock Manila server
			manilaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.mockServerError {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}

				// Handle shares list endpoint
				if r.URL.Path == "/shares" || r.URL.Path == "/shares/detail" {
					w.Header().Set("Content-Type", "application/json")
					if tt.emptySharesError {
						// Return empty shares list
						sharesResponse := map[string]any{
							"shares": []mockShare{},
						}
						err := json.NewEncoder(w).Encode(sharesResponse)
						if err != nil {
							t.Errorf("Failed to encode shares response: %v", err)
						}
						return
					}

					sharesResponse := map[string]any{
						"shares": tt.mockShares,
					}
					err := json.NewEncoder(w).Encode(sharesResponse)
					if err != nil {
						t.Errorf("Failed to encode shares response: %v", err)
					}
					return
				}

				// Handle root path for service discovery
				if r.URL.Path == "/" {
					w.Header().Set("Content-Type", "application/json")
					versionResponse := map[string]any{
						"versions": []map[string]any{
							{
								"status": "CURRENT",
								"id":     "v2.0",
								"links": []map[string]any{
									{
										"href": "http://" + r.Host + "/v2/",
										"rel":  "self",
									},
								},
							},
						},
					}
					err := json.NewEncoder(w).Encode(versionResponse)
					if err != nil {
						t.Errorf("Failed to encode version response: %v", err)
					}
					return
				}

				// Default response for other endpoints
				w.WriteHeader(http.StatusNotFound)
			}))
			defer manilaServer.Close()

			// Create mock Keystone server
			keystoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.authError {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}

				w.Header().Set("Content-Type", "application/json")

				// Handle different Keystone API endpoints
				switch r.URL.Path {
				case "/", "/v3", "/v3/":
					// Handle version discovery
					versionResponse := map[string]any{
						"versions": map[string]any{
							"values": []map[string]any{
								{
									"status": "stable",
									"id":     "v3.0",
									"links": []map[string]any{
										{
											"href": "http://" + r.Host + "/v3/",
											"rel":  "self",
										},
									},
								},
							},
						},
					}
					err := json.NewEncoder(w).Encode(versionResponse)
					if err != nil {
						t.Errorf("Failed to encode version response: %v", err)
					}
				case "/v3/auth/tokens":
					w.WriteHeader(http.StatusCreated)

					// Mock token response
					tokenResponse := map[string]any{
						"token": map[string]any{
							"methods":    []string{"password"},
							"expires_at": "2030-01-01T00:00:00Z",
							"project": map[string]any{
								"domain": map[string]any{
									"id":   "default",
									"name": "default",
								},
								"id":   "test-project-id",
								"name": "test-project",
							},
							"catalog": []map[string]any{
								{
									"type": "sharev2",
									"id":   "manila-service-id",
									"name": "manilav2",
									"endpoints": []map[string]any{
										{
											"region_id": "RegionOne",
											"url":       manilaServer.URL,
											"region":    "RegionOne",
											"interface": "public",
											"id":        "manila-endpoint-id",
										},
									},
								},
							},
							"user": map[string]any{
								"domain": map[string]any{
									"id":   "default",
									"name": "default",
								},
								"id":   "test-user-id",
								"name": "test-user",
							},
						},
					}
					if tt.endpointError {
						// Don't include sharev2 service in catalog
						tokenResponse["token"].(map[string]any)["catalog"] = []map[string]any{}
					}

					// Set the token in the header
					w.Header().Set("X-Subject-Token", "mock-token-id")
					err := json.NewEncoder(w).Encode(tokenResponse)
					if err != nil {
						t.Errorf("Failed to encode token response: %v", err)
					}
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer keystoneServer.Close()

			// Add the keystone secret object
			objects = append(objects, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "keystone-secret",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"url":               []byte(keystoneServer.URL),
					"availability":      []byte("public"),
					"username":          []byte("test-user"),
					"password":          []byte("test-password"),
					"projectName":       []byte("test-project"),
					"userDomainName":    []byte("default"),
					"projectDomainName": []byte("default"),
				},
			})
			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				Build()
			config := HistoryCleanupConfig{
				KeystoneSecretRef: corev1.SecretReference{
					Name:      "keystone-secret",
					Namespace: "default",
				},
			}
			err := HistoryCleanup(context.Background(), client, config)

			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			if !tt.expectError {
				// Verify expected history entries were deleted
				for _, expectedDeleted := range tt.expectedDeleted {
					var history v1alpha1.History
					err := client.Get(context.Background(),
						types.NamespacedName{Name: expectedDeleted}, &history)
					if err == nil {
						t.Errorf("Expected history %s to be deleted but it still exists", expectedDeleted)
					}
				}

				// Verify other histories still exist
				for _, originalHistory := range tt.histories {
					shouldBeDeleted := false
					for _, expectedDeleted := range tt.expectedDeleted {
						if originalHistory.Name == expectedDeleted {
							shouldBeDeleted = true
							break
						}
					}
					if !shouldBeDeleted {
						var history v1alpha1.History
						err := client.Get(context.Background(),
							types.NamespacedName{Name: originalHistory.Name}, &history)
						if err != nil {
							t.Errorf("Expected history %s to still exist but got error: %v",
								originalHistory.Name, err)
						}
					}
				}
			}
		})
	}
}

func TestCleanupManilaHistoryCancel(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add corev1 scheme: %v", err)
	}

	objects := []client.Object{
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "keystone-secret",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"url":               []byte("http://invalid-keystone-url"),
				"availability":      []byte("public"),
				"username":          []byte("test-user"),
				"password":          []byte("test-password"),
				"projectName":       []byte("test-project"),
				"userDomainName":    []byte("default"),
				"projectDomainName": []byte("default"),
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		Build()

	config := HistoryCleanupConfig{
		KeystoneSecretRef: corev1.SecretReference{
			Name:      "keystone-secret",
			Namespace: "default",
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()

	// This should exit quickly due to context cancellation
	if err := HistoryCleanup(ctx, client, config); err != nil {
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Unexpected error during cleanup: %v", err)
		}
	}

	// If we reach here without hanging, the test passed
}

type mockShare struct {
	ID string `json:"id"`
}
