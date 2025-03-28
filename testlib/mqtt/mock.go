// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package mqtt

import (
	pahomqtt "github.com/eclipse/paho.mqtt.golang"
)

// Mock mqtt client that does nothing and can be used for testing.
type MockClient struct{}

func (m *MockClient) Publish(topic string, payload any) {
	// Do nothing
}

func (m *MockClient) Connect() error {
	return nil
}

func (m *MockClient) Disconnect() {
	// Do nothing
}

func (m *MockClient) Subscribe(topic string, callback pahomqtt.MessageHandler) error {
	return nil
}
