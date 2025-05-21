// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package extractor

import (
	"crypto/tls"
	"log/slog"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/internal/monitoring"
	"github.com/cobaltcore-dev/cortex/internal/mqtt"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	extractorv1 "github.com/cobaltcore-dev/cortex/extractor/api/v1"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func Run(registry *monitoring.Registry, config conf.FeaturesConfig, db db.DB) {
	// Set up the kubernetes operator.
	schemeBuilder := &scheme.Builder{GroupVersion: schema.GroupVersion{
		Group:   "extractor.cortex.sap",
		Version: "v1",
	}}
	schemeBuilder.Register(&extractorv1.Feature{}, &extractorv1.FeatureList{})
	slog.Info("Registering scheme for feature CRD")
	scheme, err := schemeBuilder.Build()
	if err != nil {
		panic("failed to build scheme: " + err.Error())
	}
	// If the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	webhookServer := webhook.NewServer(webhook.Options{
		TLSOpts: []func(*tls.Config){
			func(c *tls.Config) {
				slog.Info("Setting up TLS for webhook server")
				c.NextProtos = []string{"http/1.1"}
			},
		},
	})
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Metrics:       metricsserver.Options{BindAddress: ":8081"}, // TODO: Conf + port + svcmonitor
		Scheme:        scheme,
		WebhookServer: webhookServer,
	})
	if err != nil {
		panic("failed to create controller manager: " + err.Error())
	}
	slog.Info("Created controller manager")

	// Setup the feature extractor.
	monitor := NewPipelineMonitor(registry)

	mqttClient := mqtt.NewClient(mqtt.NewMQTTMonitor(registry))
	if err := mqttClient.Connect(); err != nil {
		panic("failed to connect to mqtt broker: " + err.Error())
	}
	defer mqttClient.Disconnect()

	pipeline := NewPipeline(config, db, monitor, mqttClient)
	// Selects the extractors to run based on the config.
	pipeline.Init(SupportedExtractors)
	go pipeline.ExtractOnTrigger() // blocking

	// Bind the reconciliation loop.
	ctrl.NewControllerManagedBy(mgr).
		For(&extractorv1.Feature{}).
		Named("feature").
		Complete(&pipeline)

	slog.Info("starting manager")
	go func() {
		if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
			panic("failed to start controller manager: " + err.Error())
		}
	}()
}
