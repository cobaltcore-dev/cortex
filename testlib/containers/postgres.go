// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package containers

import (
	"database/sql"
	"fmt"
	"log"
	"testing"

	"github.com/ory/dockertest"
	"github.com/ory/dockertest/docker"
)

type PostgresContainer struct {
	pool     *dockertest.Pool
	resource *dockertest.Resource
}

func (c PostgresContainer) GetPort() string {
	return c.resource.GetPort("5432/tcp")
}

func (c *PostgresContainer) Init(t *testing.T) {
	pool, err := dockertest.NewPool("")
	if err != nil {
		log.Fatalf("could not construct pool: %s", err)
	}
	c.pool = pool
	if err = pool.Client.Ping(); err != nil {
		log.Fatalf("could not connect to Docker: %s", err)
	}
	resource, err := pool.RunWithOptions(&dockertest.RunOptions{
		Repository: "postgres",
		Tag:        "17",
		Env: []string{
			"POSTGRES_USER=postgres",
			"POSTGRES_PASSWORD=secret",
			"listen_addresses = '*'",
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
	if err := c.resource.Expire(10); err != nil {
		log.Fatalf("could not set expiration: %s", err)
	}
	psqlInfo := fmt.Sprintf(
		"host=localhost port=%s user=postgres password=secret dbname=postgres sslmode=disable",
		resource.GetPort("5432/tcp"),
	)
	sqlDB, err := sql.Open("postgres", psqlInfo)
	if err != nil {
		log.Fatalf("could not connect to sql db: %s", err)
	}
	if err = pool.Retry(sqlDB.Ping); err != nil {
		log.Fatalf("postgres db is not ready in time: %s", err)
	}
}

func (c *PostgresContainer) Close() {
	if err := c.pool.Purge(c.resource); err != nil {
		log.Fatalf("could not purge resource: %s", err)
	}
}
