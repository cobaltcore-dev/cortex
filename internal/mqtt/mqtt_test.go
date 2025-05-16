// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package mqtt

import (
	"os"
	"sync"
	"testing"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/testlib/mqtt/containers"
	mqtt "github.com/eclipse/paho.mqtt.golang"
)

func TestConnect(t *testing.T) {
	if os.Getenv("RABBITMQ_CONTAINER") != "1" {
		t.Skip("skipping test; set RABBITMQ_CONTAINER=1 to run")
	}

	container := containers.RabbitMQContainer{}
	container.Init(t)
	defer container.Close()
	conf := conf.MQTTConfig{URL: "tcp://localhost:" + container.GetPort()}
	c := client{conf: conf, lock: &sync.Mutex{}}

	err := c.Connect()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	c.Disconnect()
}

func TestPublish(t *testing.T) {
	if os.Getenv("RABBITMQ_CONTAINER") != "1" {
		t.Skip("skipping test; set RABBITMQ_CONTAINER=1 to run")
	}
	// FIXME: It seems like GitHub Actions kills the container on the publish call.
	if os.Getenv("GITHUB_ACTIONS") == "1" {
		t.Skip("skipping test; GITHUB_ACTIONS=1")
	}

	container := containers.RabbitMQContainer{}
	container.Init(t)
	defer container.Close()
	conf := conf.MQTTConfig{URL: "tcp://localhost:" + container.GetPort()}
	c := client{conf: conf, lock: &sync.Mutex{}}
	err := c.publish("test/topic", map[string]string{"key": "value"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	t.Log("published message to test/topic")
	c.Disconnect()
}

func TestSubscribe(t *testing.T) {
	if os.Getenv("RABBITMQ_CONTAINER") != "1" {
		t.Skip("skipping test; set RABBITMQ_CONTAINER=1 to run")
	}

	container := containers.RabbitMQContainer{}
	container.Init(t)
	defer container.Close()
	conf := conf.MQTTConfig{URL: "tcp://localhost:" + container.GetPort()}
	subscriptions := make(map[string]mqtt.MessageHandler)
	c := client{conf: conf, lock: &sync.Mutex{}, subscriptions: subscriptions}

	err := c.Subscribe("test/topic", func(client mqtt.Client, msg mqtt.Message) {})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(c.subscriptions) != 1 {
		t.Fatalf("expected 1 subscription, got %d", len(c.subscriptions))
	}
	c.Disconnect()
}

func TestUnexpectedConnectionLoss(t *testing.T) {
	if os.Getenv("RABBITMQ_CONTAINER") != "1" {
		t.Skip("skipping test; set RABBITMQ_CONTAINER=1 to run")
	}

	container := containers.RabbitMQContainer{}
	container.Init(t)

	conf := conf.MQTTConfig{
		URL: "tcp://localhost:" + container.GetPort(),
		Reconnect: conf.MQTTReconnectConfig{
			InitialDelay:  10,
			MaxRetries:    10,
			RetryInterval: 2,
		},
	}
	subscriptions := make(map[string]mqtt.MessageHandler)
	c := client{conf: conf, lock: &sync.Mutex{}, subscriptions: subscriptions}
	// no need to defer the container close here, as it will be closed in the test below

	reconnected := make(chan string)
	err := c.Subscribe("test/topic", func(client mqtt.Client, msg mqtt.Message) {
		reconnected <- "Welcome back!"
	})

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Simulate connection loss
	container.Close()
	container = containers.RabbitMQContainer{}
	container.Init(t)
	defer container.Close()

	// update the mqtt url since we created a new container and the port may has changed
	conf.URL = "tcp://localhost:" + container.GetPort()
	c.conf = conf
	otherClient := client{conf: conf, lock: &sync.Mutex{}}

	if err := otherClient.Connect(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if err := otherClient.publish("test/topic", "Welcome back!"); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Wait for the client to reconnect and receive the message
	select {
	case <-reconnected:
		t.Log("client successfully reconnected and received message")
	case <-time.After(10 * time.Second):
		t.Fatal("client did not reconnect and receive message in time")
	}

	c.Disconnect()
	otherClient.Disconnect()
}

func TestDisconnect(t *testing.T) {
	if os.Getenv("RABBITMQ_CONTAINER") != "1" {
		t.Skip("skipping test; set RABBITMQ_CONTAINER=1 to run")
	}

	container := containers.RabbitMQContainer{}
	container.Init(t)
	defer container.Close()
	conf := conf.MQTTConfig{URL: "tcp://localhost:" + container.GetPort()}
	c := client{conf: conf, lock: &sync.Mutex{}}
	err := c.Connect()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	c.Disconnect()
	c.Disconnect() // Should do nothing (already disconnected)
}
