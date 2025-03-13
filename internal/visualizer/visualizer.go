// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package visualizer

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/telemetry"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/sapcc/go-bits/httpext"
)

//go:embed visualizer.html
var visualizerHTML string

type Visualizer interface {
	Init(context.Context)
}

type visualizer struct {
	config          conf.VisualizerConfig
	telemetryClient telemetry.Client
	monitor         Monitor
	data            *map[string]any
}

func NewVisualizer(config conf.VisualizerConfig, telemetryClient telemetry.Client, m Monitor) Visualizer {
	return &visualizer{
		config:          config,
		telemetryClient: telemetryClient,
		monitor:         m,
		data:            &map[string]any{},
	}
}

// Connect to a telemetry mqtt broker and visualize what cortex is doing.
// Open a http server and serve a web page that shows the telemetry data.
func (v *visualizer) Init(ctx context.Context) {
	v.telemetryClient.Subscribe("cortex/scheduler", func(client mqtt.Client, msg mqtt.Message) {
		if err := json.Unmarshal(msg.Payload(), v.data); err != nil {
			slog.Error("failed to unmarshal telemetry message", "err", err)
		}
		slog.Info("received telemetry data", "data", v.data)
	})

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if _, err := io.WriteString(w, visualizerHTML); err != nil {
			slog.Error("failed to write response", "error", err)
		}
		slog.Info("served visualizer page")
	})
	mux.HandleFunc("/data.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(*v.data); err != nil {
			slog.Error("failed to write response", "error", err)
		}
		slog.Info("served visualizer data")
	})
	slog.Info("visualizer listening on", "port", v.config.Port)
	addr := fmt.Sprintf(":%d", v.config.Port)
	if err := httpext.ListenAndServeContext(ctx, addr, mux); err != nil {
		panic(err)
	}
}
