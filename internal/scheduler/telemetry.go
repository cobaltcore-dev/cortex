// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package scheduler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/scheduler/api"
	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type TelemetryData struct {
	// The original request from Nova that was sent to the scheduler.
	Request api.Request `json:"request"`
	// The order in which the scheduler steps were applied.
	Order []string `json:"order"`
	// Activations before any scheduler steps are run.
	In map[string]float64 `json:"in"`
	// Activations by scheduler step.
	Steps map[string]map[string]float64 `json:"steps"`
	// The final activations that were used to make a decision.
	Out map[string]float64 `json:"out"`
}

type Telemetry interface {
	Publish(TelemetryData) error
}

type telemetry struct {
	conf conf.SchedulerTelemetryConfig
	// MQTT client to publish telemetry data.
	client *mqtt.Client
	// Lock to prevent concurrent writes to the MQTT client.
	lock *sync.Mutex
}

func NewTelemetry(conf conf.SchedulerTelemetryConfig) Telemetry {
	return &telemetry{conf: conf, lock: &sync.Mutex{}}
}

// Connect to the mqtt broker.
func (t *telemetry) connect() error {
	if t.client != nil {
		return nil
	}

	slog.Info("connecting to telmetry mqtt broker at", "url", t.conf.URL)
	opts := mqtt.NewClientOptions()
	opts.AddBroker(t.conf.URL)
	opts.SetConnectTimeout(10 * time.Second)
	opts.SetConnectRetry(true)
	opts.SetConnectRetryInterval(5 * time.Second)
	opts.SetAutoReconnect(true)
	opts.SetKeepAlive(60 * time.Second)
	opts.SetPingTimeout(10 * time.Second)
	opts.SetOnConnectHandler(func(client mqtt.Client) {
		slog.Info("connected to telemetry mqtt broker")
	})
	opts.SetConnectionLostHandler(func(client mqtt.Client, err error) {
		slog.Error("connection to telemetry mqtt broker lost", "err", err)
	})
	opts.SetClientID(fmt.Sprintf("cortex-scheduler-%d", rand.Intn(1_000_000)))
	opts.SetOrderMatters(false)
	opts.SetProtocolVersion(4)
	opts.SetDefaultPublishHandler(func(client mqtt.Client, msg mqtt.Message) {
		slog.Warn("received unexpected message on topic", "topic", msg.Topic())
	})
	opts.SetUsername(t.conf.Username)
	opts.SetPassword(t.conf.Password)

	client := mqtt.NewClient(opts)
	if conn := client.Connect(); conn.Wait() && conn.Error() != nil {
		panic(conn.Error())
	}
	t.client = &client

	return nil
}

// Publish telemetry data to the mqtt broker.
func (t *telemetry) Publish(telemetryData TelemetryData) error {
	t.lock.Lock()
	defer t.lock.Unlock()

	// Connect if we aren't already.
	if err := t.connect(); err != nil {
		return err
	}
	client := *t.client

	data, err := json.Marshal(telemetryData)
	if err != nil {
		return err
	}
	dataStr := string(data)
	pub := client.Publish("cortex/scheduler/telemetry", 2, true, dataStr)
	if pub.Wait() && pub.Error() != nil {
		slog.Error("failed to publish telemetry data", "err", pub.Error())
		return pub.Error()
	}
	return nil
}
