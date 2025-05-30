// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package mqtt

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/sapcc/go-bits/jobloop"
)

type Client interface {
	Connect() error
	Publish(topic string, obj any)
	Disconnect()
	Subscribe(topic string, callback mqtt.MessageHandler) error
}

type client struct {
	conf conf.MQTTConfig
	// MQTT client to publish mqtt data.
	client *mqtt.Client
	// Lock to prevent concurrent writes to the MQTT client.
	lock *sync.Mutex
	// Monitor for mqtt related metrics
	monitor Monitor
	// All current subscribed topics of the client
	subscriptions map[string]mqtt.MessageHandler
}

func NewClient(monitor Monitor) Client {
	return NewClientWithConfig(conf.NewConfig().GetMQTTConfig(), monitor)
}

func NewClientWithConfig(conf conf.MQTTConfig, monitor Monitor) Client {
	return &client{
		conf:          conf,
		lock:          &sync.Mutex{},
		subscriptions: make(map[string]mqtt.MessageHandler),
		monitor:       monitor,
	}
}

// Called when the connection to the mqtt broker is lost.
func (t *client) onUnexpectedConnectionLoss(_ mqtt.Client, err error) {
	slog.Error("connection to mqtt broker lost", "err", err)
	t.Disconnect()
	t.client = nil

	for retry := range t.conf.Reconnect.MaxRetries {
		slog.Info("attempting to reconnect to mqtt broker", "attempt", retry+1, "url", t.conf.URL)

		if err := t.Connect(); err != nil {
			slog.Error("failed to reconnect to mqtt broker", "err", err)
			if retry < t.conf.Reconnect.MaxRetries-1 {
				interval := time.Duration(t.conf.Reconnect.RetryIntervalSeconds) * time.Second
				time.Sleep(jobloop.DefaultJitter(interval))
			}
			t.client = nil
			continue
		}
		slog.Info("reconnected to mqtt broker")
		if err := t.resubscribeAllTopics(); err != nil {
			slog.Error("failed to resubscribe to all topics", "err", err)
			panic(err)
		}
		return
	}

	slog.Error("failed to reconnect to mqtt broker after max retries", "maxRetries", t.conf.Reconnect.MaxRetries)
	panic(err)
}

// Connect to the mqtt broker.
func (t *client) Connect() error {
	if t.client != nil {
		return nil
	}

	if t.monitor.connectionAttempts != nil {
		t.monitor.connectionAttempts.Inc()
	}

	slog.Info("connecting to mqtt broker at", "url", t.conf.URL)
	opts := mqtt.NewClientOptions()
	opts.AddBroker(t.conf.URL)
	opts.SetConnectTimeout(10 * time.Second)
	opts.SetConnectRetry(false)
	opts.SetKeepAlive(60 * time.Second)
	opts.SetPingTimeout(10 * time.Second)

	// Changing this value to false might lead to unexpected behavior because
	// in the onUnexpectedConnectionLoss method the client manually resubscribes
	// to all the topics, when the client reconnects.
	opts.SetCleanSession(true)
	opts.SetConnectionLostHandler(t.onUnexpectedConnectionLoss)
	//nolint:gosec // We don't care if the client id is cryptographically secure.
	opts.SetClientID(fmt.Sprintf("cortex-%d", rand.Intn(1_000_000)))
	opts.SetOrderMatters(false)
	opts.SetProtocolVersion(5)
	opts.SetDefaultPublishHandler(func(client mqtt.Client, msg mqtt.Message) {
		slog.Warn("received unexpected message on topic", "topic", msg.Topic())
	})
	opts.SetUsername(t.conf.Username)
	opts.SetPassword(t.conf.Password)

	client := mqtt.NewClient(opts)
	if conn := client.Connect(); conn.Wait() && conn.Error() != nil {
		return conn.Error()
	}
	t.client = &client
	slog.Info("connected to mqtt broker")

	return nil
}

// Publish mqtt data to the mqtt broker.
// In case of errors, log them out and return.
func (t *client) Publish(topic string, obj any) {
	if err := t.publish(topic, obj); err != nil {
		slog.Error("failed to publish mqtt data", "err", err)
	}
	slog.Info("published mqtt data", "topic", topic)
}

// Publish mqtt data to the mqtt broker.
func (t *client) publish(topic string, obj any) error {
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
	pub := client.Publish(topic, 2, true, dataStr)
	if pub.Wait() && pub.Error() != nil {
		return err
	}
	return nil
}

// Resubscribe to all topics after a reconnect.
func (t *client) resubscribeAllTopics() error {
	for topic, handler := range t.subscriptions {
		if err := t.Subscribe(topic, handler); err != nil {
			slog.Error("failed to resubscribe to topic", "topic", topic, "err", err)
			return err
		}
		slog.Info("resubscribed to topic", "topic", topic)
	}
	return nil
}

// Subscribe to a topic on the mqtt broker.
func (t *client) Subscribe(topic string, callback mqtt.MessageHandler) error {
	t.lock.Lock()
	defer t.lock.Unlock()

	// Connect if we aren't already.
	if err := t.Connect(); err != nil {
		return err
	}
	client := *t.client

	token := client.Subscribe(topic, 2, callback)
	if token.Wait() && token.Error() != nil {
		slog.Error("failed to subscribe to topic", "topic", topic, "err", token.Error())
		return token.Error()
	}
	slog.Info("subscribed to topic", "topic", topic)

	t.subscriptions[topic] = callback
	return nil
}

// Disconnect from the mqtt broker.
func (t *client) Disconnect() {
	if t.client == nil {
		return
	}
	client := *t.client
	t.client = nil
	// Note: the disconnect will run in a goroutine.
	client.Disconnect(1000)
	// Wait for the disconnect to finish.
	for client.IsConnected() {
		time.Sleep(100 * time.Millisecond)
	}
	slog.Info("disconnected from mqtt broker")
}
