// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package mqtt

import (
	"os"
	"sync"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/testlib/mqtt/containers"
	mqtt "github.com/eclipse/paho.mqtt.golang"
)

func TestConnect(t *testing.T) {
	if os.Getenv("VERNEMQ_CONTAINER") != "1" {
		t.Skip("skipping test; set VERNEMQ_CONTAINER=1 to run")
	}

	container := containers.VernemqContainer{}
	container.Init(t)
	defer container.Close()
	conf := conf.MQTTConfig{URL: "tcp://localhost:" + container.GetPort()}
	c := client{conf: conf, lock: &sync.Mutex{}}
	defer c.Disconnect()

	err := c.Connect()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestPublish(t *testing.T) {
	if os.Getenv("VERNEMQ_CONTAINER") != "1" {
		t.Skip("skipping test; set VERNEMQ_CONTAINER=1 to run")
	}

	container := containers.VernemqContainer{}
	container.Init(t)
	defer container.Close()
	conf := conf.MQTTConfig{URL: "tcp://localhost:" + container.GetPort()}
	c := client{conf: conf, lock: &sync.Mutex{}}
	defer c.Disconnect()

	err := c.publish("test/topic", map[string]string{"key": "value"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestSubscribe(t *testing.T) {
	if os.Getenv("VERNEMQ_CONTAINER") != "1" {
		t.Skip("skipping test; set VERNEMQ_CONTAINER=1 to run")
	}

	container := containers.VernemqContainer{}
	container.Init(t)
	defer container.Close()
	conf := conf.MQTTConfig{URL: "tcp://localhost:" + container.GetPort()}
	c := client{conf: conf, lock: &sync.Mutex{}}
	defer c.Disconnect()

	err := c.Subscribe("test/topic", func(client mqtt.Client, msg mqtt.Message) {})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestDisconnect(t *testing.T) {
	if os.Getenv("VERNEMQ_CONTAINER") != "1" {
		t.Skip("skipping test; set VERNEMQ_CONTAINER=1 to run")
	}

	container := containers.VernemqContainer{}
	container.Init(t)
	defer container.Close()
	conf := conf.MQTTConfig{URL: "tcp://localhost:" + container.GetPort()}
	c := client{conf: conf, lock: &sync.Mutex{}}
	defer c.Disconnect() // Should do nothing (already disconnected)
	err := c.Connect()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	c.Disconnect()
}
