// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package nova

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
	"github.com/cobaltcore-dev/cortex/pkg/conf"

	corev1 "k8s.io/api/core/v1"
)

func TestCleanupNova(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add corev1 scheme: %v", err)
	}

	tests := []struct {
		name            string
		decisions       []v1alpha1.Decision
		reservations    []v1alpha1.Reservation
		mockServers     []mockServer
		authError       bool
		endpointError   bool
		serverError     bool
		emptyServers    bool
		expectedDeleted []string
		expectError     bool
	}{
		{
			name:        "authentication error",
			decisions:   []v1alpha1.Decision{},
			authError:   true,
			expectError: true,
		},
		{
			name:          "endpoint discovery error",
			decisions:     []v1alpha1.Decision{},
			endpointError: true,
			expectError:   true,
		},
		{
			name:        "nova server error",
			decisions:   []v1alpha1.Decision{},
			serverError: true,
			expectError: true,
		},
		{
			name:         "no servers found",
			decisions:    []v1alpha1.Decision{},
			emptyServers: true,
			expectError:  false,
		},
		{
			name: "delete decisions for non-existent servers",
			decisions: []v1alpha1.Decision{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "decision-existing-server",
					},
					Spec: v1alpha1.DecisionSpec{
						Operator:   "test-operator",
						Type:       v1alpha1.DecisionTypeNovaServer,
						ResourceID: "server-exists",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "decision-deleted-server",
					},
					Spec: v1alpha1.DecisionSpec{
						Operator:   "test-operator",
						Type:       v1alpha1.DecisionTypeNovaServer,
						ResourceID: "server-deleted",
					},
				},
			},
			mockServers: []mockServer{
				{ID: "server-exists"},
			},
			expectedDeleted: []string{"decision-deleted-server"},
			expectError:     false,
		},
		{
			name: "keep decisions for existing servers",
			decisions: []v1alpha1.Decision{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "decision-server-1",
					},
					Spec: v1alpha1.DecisionSpec{
						Operator:   "test-operator",
						Type:       v1alpha1.DecisionTypeNovaServer,
						ResourceID: "server-1",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "decision-server-2",
					},
					Spec: v1alpha1.DecisionSpec{
						Operator:   "test-operator",
						Type:       v1alpha1.DecisionTypeNovaServer,
						ResourceID: "server-2",
					},
				},
			},
			mockServers: []mockServer{
				{ID: "server-1"},
				{ID: "server-2"},
			},
			expectedDeleted: []string{},
			expectError:     false,
		},
		{
			name: "skip decisions with linked reservations",
			decisions: []v1alpha1.Decision{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "decision-reserved-server",
					},
					Spec: v1alpha1.DecisionSpec{
						Operator:   "test-operator",
						Type:       v1alpha1.DecisionTypeNovaServer,
						ResourceID: "server-reserved",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "decision-unreserved-server",
					},
					Spec: v1alpha1.DecisionSpec{
						Operator:   "test-operator",
						Type:       v1alpha1.DecisionTypeNovaServer,
						ResourceID: "server-unreserved",
					},
				},
			},
			reservations: []v1alpha1.Reservation{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "server-reserved",
					},
				},
			},
			mockServers:     []mockServer{},
			expectedDeleted: []string{"decision-unreserved-server"},
			expectError:     false,
		},
		{
			name: "skip non-nova decisions",
			decisions: []v1alpha1.Decision{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "decision-cinder",
					},
					Spec: v1alpha1.DecisionSpec{
						Operator:   "test-operator",
						Type:       v1alpha1.DecisionTypeCinderVolume,
						ResourceID: "volume-1",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "decision-wrong-operator",
					},
					Spec: v1alpha1.DecisionSpec{
						Operator:   "other-operator",
						Type:       v1alpha1.DecisionTypeNovaServer,
						ResourceID: "server-1",
					},
				},
			},
			mockServers:     []mockServer{},
			expectedDeleted: []string{},
			expectError:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := make([]client.Object, 0, len(tt.decisions)+len(tt.reservations))
			for i := range tt.decisions {
				objects = append(objects, &tt.decisions[i])
			}
			for i := range tt.reservations {
				objects = append(objects, &tt.reservations[i])
			}

			novaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.serverError {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}

				if r.URL.Path == "/servers" || r.URL.Path == "/servers/detail" {
					w.Header().Set("Content-Type", "application/json")
					if tt.emptyServers {
						serversResponse := map[string]any{
							"servers": []mockServer{},
						}
						err := json.NewEncoder(w).Encode(serversResponse)
						if err != nil {
							t.Errorf("Failed to encode servers response: %v", err)
						}
						return
					}

					serversResponse := map[string]any{
						"servers": tt.mockServers,
					}
					err := json.NewEncoder(w).Encode(serversResponse)
					if err != nil {
						t.Errorf("Failed to encode servers response: %v", err)
					}
					return
				}

				w.WriteHeader(http.StatusNotFound)
			}))
			defer novaServer.Close()

			keystoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.authError {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}

				w.Header().Set("Content-Type", "application/json")

				switch r.URL.Path {
				case "/", "/v3", "/v3/":
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
									"type": "compute",
									"id":   "nova-service-id",
									"name": "nova",
									"endpoints": []map[string]any{
										{
											"region_id": "RegionOne",
											"url":       novaServer.URL,
											"region":    "RegionOne",
											"interface": "public",
											"id":        "nova-endpoint-id",
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
						tokenResponse["token"].(map[string]any)["catalog"] = []map[string]any{}
					}

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
			config := conf.Config{
				Operator: "test-operator",
				KeystoneSecretRef: corev1.SecretReference{
					Name:      "keystone-secret",
					Namespace: "default",
				},
			}
			err := Cleanup(context.Background(), client, config)

			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			if !tt.expectError {
				// Verify expected decisions were deleted
				for _, expectedDeleted := range tt.expectedDeleted {
					var decision v1alpha1.Decision
					err := client.Get(context.Background(),
						types.NamespacedName{Name: expectedDeleted}, &decision)
					if err == nil {
						t.Errorf("Expected decision %s to be deleted but it still exists", expectedDeleted)
					}
				}

				// Verify other decisions still exist
				for _, originalDecision := range tt.decisions {
					shouldBeDeleted := false
					for _, expectedDeleted := range tt.expectedDeleted {
						if originalDecision.Name == expectedDeleted {
							shouldBeDeleted = true
							break
						}
					}
					if !shouldBeDeleted {
						var decision v1alpha1.Decision
						err := client.Get(context.Background(),
							types.NamespacedName{Name: originalDecision.Name}, &decision)
						if err != nil {
							t.Errorf("Expected decision %s to still exist but got error: %v",
								originalDecision.Name, err)
						}
					}
				}
			}
		})
	}
}

func TestCleanupNovaDecisionsCancel(t *testing.T) {
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

	config := conf.Config{
		Operator: "test-operator",
		KeystoneSecretRef: corev1.SecretReference{
			Name:      "keystone-secret",
			Namespace: "default",
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()

	// This should exit quickly due to context cancellation
	if err := Cleanup(ctx, client, config); err != nil {
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Unexpected error during cleanup: %v", err)
		}
	}

	// If we reach here without hanging, the test passed
}

type mockServer struct {
	ID string `json:"id"`
}
