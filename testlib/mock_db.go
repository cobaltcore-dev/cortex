package testlib

import (
	"context"
	"fmt"
	"log"
	"testing"

	"github.com/go-pg/pg/v10"
	"github.com/ory/dockertest"
	"github.com/ory/dockertest/docker"
)

func WithMockDB(m *testing.M, killAfterSeconds uint) {
	pool, err := dockertest.NewPool("")
	if err != nil {
		log.Fatalf("could not construct pool: %s", err)
	}
	if err = pool.Client.Ping(); err != nil {
		log.Fatalf("could not connect to Docker: %s", err)
	}
	postgres, err := pool.RunWithOptions(&dockertest.RunOptions{
		Repository: "postgres",
		Tag:        "11",
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
	if err := postgres.Expire(killAfterSeconds); err != nil {
		log.Fatalf("could not set expiration: %s", err)
	}
	if err = pool.Retry(func() error {
		mockDB := pg.Connect(&pg.Options{
			Addr:     fmt.Sprintf("%s:%s", "localhost", postgres.GetPort("5432/tcp")),
			User:     "postgres",
			Password: "secret",
			Database: "postgres",
		})
		if err != nil {
			log.Fatalf("could not connect to Docker: %s", err)
		}
		return mockDB.Ping(context.Background())
	}); err != nil {
		log.Fatalf("could not connect to Docker: %s", err)
	}
	defer func() {
		if err := pool.Purge(postgres); err != nil {
			log.Fatalf("could not purge resource: %s", err)
		}
	}()
	m.Run()
}
