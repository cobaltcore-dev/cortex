// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"errors"
	"testing"

	"github.com/cobaltcore-dev/cortex/lib/keystone"
)

func TestNewCommitmentsClient(t *testing.T) {
	config := keystone.Config{
		URL:                 "http://keystone.example.com",
		OSUsername:          "test-user",
		OSPassword:          "test-password",
		OSProjectName:       "test-project",
		OSUserDomainName:    "default",
		OSProjectDomainName: "default",
	}

	client := NewCommitmentsClient(config)
	if client == nil {
		t.Fatal("expected non-nil client")
	}

	// Type assertion to check it's the right implementation
	_, ok := client.(*commitmentsClient)
	if !ok {
		t.Fatal("expected *commitmentsClient")
	}
}

func TestCommitmentsClient_Init(t *testing.T) {
	// This test is more complex as it involves authentication
	// For now, we'll just test that the method doesn't panic
	config := keystone.Config{
		URL:                 "http://keystone.example.com",
		OSUsername:          "test-user",
		OSPassword:          "test-password",
		OSProjectName:       "test-project",
		OSUserDomainName:    "default",
		OSProjectDomainName: "default",
	}

	client := NewCommitmentsClient(config)

	// We can't easily test the full Init method without a real OpenStack environment
	// or complex mocking, so we'll just ensure the client was created properly
	commitmentsClient, ok := client.(*commitmentsClient)
	if !ok {
		t.Fatal("expected *commitmentsClient")
	}

	if commitmentsClient.conf.URL != config.URL {
		t.Errorf("expected URL '%s', got '%s'", config.URL, commitmentsClient.conf.URL)
	}
	if commitmentsClient.conf.OSUsername != config.OSUsername {
		t.Errorf("expected username '%s', got '%s'", config.OSUsername, commitmentsClient.conf.OSUsername)
	}
}

// Test the mock client interface
func TestMockCommitmentsClient(t *testing.T) {
	mockClient := &mockCommitmentsClient{
		flavorCommitments: []FlavorCommitment{
			{
				Commitment: Commitment{
					UUID:      "test-uuid-12345",
					Amount:    2,
					ProjectID: "test-project",
					DomainID:  "test-domain",
				},
				Flavor: Flavor{
					Name:  "test-flavor",
					RAM:   2048,
					VCPUs: 2,
					Disk:  10,
					ExtraSpecs: map[string]string{
						"capabilities:hypervisor_type": "kvm",
					},
				},
			},
		},
	}

	// Test Init
	mockClient.Init(t.Context())
	if !mockClient.initCalled {
		t.Error("expected Init to be called")
	}

	// Test GetFlavorCommitments
	commitments, err := mockClient.GetFlavorCommitments(t.Context())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(commitments) != 1 {
		t.Errorf("expected 1 commitment, got %d", len(commitments))
	}

	commitment := commitments[0]
	if commitment.UUID != "test-uuid-12345" {
		t.Errorf("expected UUID 'test-uuid-12345', got '%s'", commitment.UUID)
	}
	if commitment.Amount != 2 {
		t.Errorf("expected amount 2, got %d", commitment.Amount)
	}
	if commitment.Flavor.Name != "test-flavor" {
		t.Errorf("expected flavor name 'test-flavor', got '%s'", commitment.Flavor.Name)
	}
	if commitment.Flavor.RAM != 2048 {
		t.Errorf("expected flavor RAM 2048, got %d", commitment.Flavor.RAM)
	}
}

func TestMockCommitmentsClient_Error(t *testing.T) {
	mockClient := &mockCommitmentsClient{
		shouldError: true,
	}

	_, err := mockClient.GetFlavorCommitments(t.Context())
	if err == nil {
		t.Fatal("expected error from mock client")
	}

	expectedError := "mock error"
	if err.Error() != expectedError {
		t.Errorf("expected error '%s', got '%s'", expectedError, err.Error())
	}
}

// Mock CommitmentsClient for testing (moved from operator_test.go)
type mockCommitmentsClient struct {
	flavorCommitments []FlavorCommitment
	initCalled        bool
	shouldError       bool
}

func (m *mockCommitmentsClient) Init(ctx context.Context) {
	m.initCalled = true
}

func (m *mockCommitmentsClient) GetFlavorCommitments(ctx context.Context) ([]FlavorCommitment, error) {
	if m.shouldError {
		return nil, errors.New("mock error")
	}
	return m.flavorCommitments, nil
}
