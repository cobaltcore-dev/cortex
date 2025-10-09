// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package extractor

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/extractor/internal/plugins"
	"github.com/cobaltcore-dev/cortex/testlib/mqtt"
)

func TestFeatureExtractorTelemetryPublisher_Extract_PublishesTelemetry(t *testing.T) {
	mockExtractor := &mockFeatureExtractor{
		name: "mock_extractor",
		// Usually the features are a struct, but it doesn't matter for this test
		extractFunc: func() ([]plugins.Feature, error) {
			return []plugins.Feature{"1", "2"}, nil
		},
	}
	mqttClient := &mqtt.MockClient{}
	publisher := FeatureExtractorTelemetryPublisher{
		FeatureExtractor: mockExtractor,
		MQTTTopic:        "telemetry/topic",
		MQTTClient:       mqttClient,
	}

	_, err := publisher.Extract()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}
