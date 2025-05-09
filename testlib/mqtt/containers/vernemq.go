// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package containers

import (
	"fmt"
	"log"
	"math/rand"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/ory/dockertest"
	"github.com/ory/dockertest/docker"
)

type VernemqContainer struct {
	pool     *dockertest.Pool
	resource *dockertest.Resource
}

func (c VernemqContainer) GetPort() string {
	return c.resource.GetPort("1883/tcp")
}

func (c *VernemqContainer) Init(t *testing.T) {
	log.Println("starting vernemq container")
	pool, err := dockertest.NewPool("")
	if err != nil {
		log.Fatalf("could not construct pool: %s", err)
	}
	c.pool = pool
	if err = pool.Client.Ping(); err != nil {
		log.Fatalf("could not connect to Docker: %s", err)
	}
	resource, err := pool.RunWithOptions(&dockertest.RunOptions{
		Repository: "ghcr.io/cobaltcore-dev/cortex-vernemq",
		Tag:        "latest",
		Env: []string{
			"DOCKER_VERNEMQ_ACCEPT_EULA=yes",
			"DOCKER_VERNEMQ_ALLOW_ANONYMOUS=on",
		},
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
	client := mqtt.NewClient(opts)
	if conn := client.Connect(); conn.Wait() && conn.Error() != nil {
		panic(conn.Error())
	}
	log.Println("vernemq container is ready")
}

func (c *VernemqContainer) Close() {
	if err := c.pool.Purge(c.resource); err != nil {
		log.Fatalf("could not purge resource: %s", err)
	}
}
