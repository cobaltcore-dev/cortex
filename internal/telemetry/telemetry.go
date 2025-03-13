// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package telemetry

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type Client interface {
	Connect() error
	Publish(any) error
	Disconnect()
}

type client struct {
	conf conf.TelemetryConfig
	// MQTT client to publish telemetry data.
	client *mqtt.Client
	// Lock to prevent concurrent writes to the MQTT client.
	lock *sync.Mutex
}

func NewClient(conf conf.TelemetryConfig) Client {
	return &client{conf: conf, lock: &sync.Mutex{}}
}

// Connect to the mqtt broker.
func (t *client) Connect() error {
	if t.client != nil {
		return nil
	}

	slog.Info("connecting to telemetry mqtt broker at", "url", t.conf.URL)
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
	slog.Info("connected to telemetry mqtt broker")

	return nil
}

// Publish telemetry data to the mqtt broker.
func (t *client) Publish(obj any) error {
	t.lock.Lock()
	defer t.lock.Unlock()

	// Connect if we aren't already.
	if err := t.Connect(); err != nil {
		return err
	}
	client := *t.client

	data, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	dataStr := string(data)
	pub := client.Publish("cortex/scheduler/telemetry", 2, true, dataStr)
	if pub.Wait() && pub.Error() != nil {
		slog.Error("failed to publish telemetry data", "err", pub.Error())
		return pub.Error()
	}
	slog.Info("published telemetry data")
	return nil
}

// Disconnect from the mqtt broker.
func (t *client) Disconnect() {
	if t.client == nil {
		return
	}
	client := *t.client
	client.Disconnect(1000)
	t.client = nil
	slog.Info("disconnected from telemetry mqtt broker")
}
