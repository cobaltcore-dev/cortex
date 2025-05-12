// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package containers

import (
	"fmt"
	"log"
	"math/rand"
	"os"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/ory/dockertest"
	"github.com/ory/dockertest/docker"

	_ "embed"
)

//go:embed rabbitmq.conf
var rabbitMQConfig string

//go:embed rabbitmq-entrypoint.sh
var rabbitMQEntrypoint string

type RabbitMQContainer struct {
	pool     *dockertest.Pool
	resource *dockertest.Resource
}

func (c RabbitMQContainer) GetPort() string {
	return c.resource.GetPort("1883/tcp")
}

func (c *RabbitMQContainer) Init(t *testing.T) {
	// Create a temporary directory for the conf and entrypoint files.
	// We will mount these files into the container.
	tmpDir := t.TempDir()
	tmpConfFile, err := os.CreateTemp(tmpDir, "rabbitmq.conf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tmpConfFile.Write([]byte(rabbitMQConfig)); err != nil {
		t.Fatal(err)
	}
	if err := tmpConfFile.Close(); err != nil {
		t.Fatal(err)
	}
	tmpEntrypointFile, err := os.CreateTemp(tmpDir, "test-entrypoint.sh")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tmpEntrypointFile.Write([]byte(rabbitMQEntrypoint)); err != nil {
		t.Fatal(err)
	}
	// Make the entrypoint file executable.
	if err := os.Chmod(tmpEntrypointFile.Name(), 0755); err != nil {
		t.Fatal(err)
	}
	if err := tmpEntrypointFile.Close(); err != nil {
		t.Fatal(err)
	}

	log.Println("starting rabbitmq container")
	pool, err := dockertest.NewPool("")
	if err != nil {
		log.Fatalf("could not construct pool: %s", err)
	}
	c.pool = pool
	if err = pool.Client.Ping(); err != nil {
		log.Fatalf("could not connect to Docker: %s", err)
	}
	resource, err := pool.RunWithOptions(&dockertest.RunOptions{
		Repository: "rabbitmq",
		Tag:        "latest",
		Mounts: []string{
			fmt.Sprintf("%s:%s", tmpConfFile.Name(), "/etc/rabbitmq/rabbitmq.conf"),
			fmt.Sprintf("%s:%s", tmpEntrypointFile.Name(), "/usr/local/bin/test-entrypoint.sh"),
		},
		Cmd:          []string{"sh", "/usr/local/bin/test-entrypoint.sh"},
		ExposedPorts: []string{"1883/tcp"},
	}, func(config *docker.HostConfig) {
		// set AutoRemove to true so that stopped container goes away by itself
		config.AutoRemove = true
		config.RestartPolicy = docker.RestartPolicy{
			Name: "no",
		}
	})
	if err != nil {
		log.Fatalf("could not start resource: %s", err)
	}
	c.resource = resource
	if err := c.resource.Expire(60); err != nil {
		log.Fatalf("could not set expiration: %s", err)
	}
	// Wait for the mqtt connection to become available.
	opts := mqtt.NewClientOptions()
	opts.AddBroker("tcp://localhost:" + c.GetPort())
	opts.SetConnectTimeout(60 * time.Second)
	opts.SetConnectRetry(true)
	opts.SetConnectRetryInterval(5 * time.Second)
	//nolint:gosec // We don't care if the client id is cryptographically secure.
	opts.SetClientID(fmt.Sprintf("cortex-testlib-runup-%d", rand.Intn(1_000_000)))
	opts.SetProtocolVersion(5)
	client := mqtt.NewClient(opts)
	if conn := client.Connect(); conn.Wait() && conn.Error() != nil {
		panic(conn.Error())
	}
	log.Println("rabbitmq container is ready")
}

func (c *RabbitMQContainer) Close() {
	if err := c.pool.Purge(c.resource); err != nil {
		log.Fatalf("could not purge resource: %s", err)
	}
}
