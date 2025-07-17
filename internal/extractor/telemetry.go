// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package extractor

import (
	"log/slog"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/extractor/plugins"
	"github.com/cobaltcore-dev/cortex/internal/mqtt"
)

type FeatureExtractorTelemetryPublisher struct {
	// The wrapped extractor that provides the telemetry data.
	FeatureExtractor plugins.FeatureExtractor
	// The MQTT topic to publish telemetry data.
	// If not set, the extractor will not publish telemetry data.
	MQTTTopic string
	// The mqtt client to publish telemetry data.
	MQTTClient mqtt.Client
}

// Get the name of the wrapped feature extractor.
func (p FeatureExtractorTelemetryPublisher) GetName() string {
	// Return the name of the wrapped feature extractor.
	return p.FeatureExtractor.GetName()
}

// Get the message topics that trigger a re-execution of the wrapped feature extractor.
func (p FeatureExtractorTelemetryPublisher) Triggers() []string {
	// Return the triggers of the wrapped feature extractor.
	return p.FeatureExtractor.Triggers()
}

// Initialize the wrapped feature extractor with the database and options.
func (p FeatureExtractorTelemetryPublisher) Init(db db.DB, conf conf.FeatureExtractorConfig) error {
	// Configure the wrapped feature extractor.
	return p.FeatureExtractor.Init(db, conf)
}

func (p FeatureExtractorTelemetryPublisher) NeedsUpdate() bool {
	// Check if the wrapped feature extractor needs an update.
	return p.FeatureExtractor.NeedsUpdate()
}

func (p FeatureExtractorTelemetryPublisher) MarkAsUpdated() {
	// Mark the wrapped feature extractor as updated.
	p.FeatureExtractor.MarkAsUpdated()
}

func (p FeatureExtractorTelemetryPublisher) NextPossibleExecution() time.Time {
	// Return the next update timestamp of the wrapped feature extractor.
	return p.FeatureExtractor.NextPossibleExecution()
}

func (p FeatureExtractorTelemetryPublisher) NotifySkip() {
	p.FeatureExtractor.NotifySkip()
}

// Publish telemetry data about the extractor to the mqtt broker.
func publishTelemetryIfNeeded(
	f plugins.FeatureExtractor,
	client mqtt.Client,
	topic string,
) FeatureExtractorTelemetryPublisher {

	return FeatureExtractorTelemetryPublisher{
		FeatureExtractor: f,
		MQTTClient:       client,
		MQTTTopic:        topic,
	}
}

// Run the wrapped feature extractor and publish telemetry data if needed.
func (p FeatureExtractorTelemetryPublisher) Extract() ([]plugins.Feature, error) {
	features, err := p.FeatureExtractor.Extract()
	if err != nil {
		return nil, err
	}
	if p.MQTTTopic == "" {
		// If no MQTT topic is set, just run the extractor without publishing telemetry.
		return features, nil
	}
	slog.Debug("features: publishing telemetry", "extractor", p.GetName(), "topic", p.MQTTTopic)
	p.MQTTClient.Publish(p.MQTTTopic, features)
	return features, nil
}
