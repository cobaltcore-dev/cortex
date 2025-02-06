// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"
	"fmt"
	"log"

	"github.com/go-pg/pg/v10"
	"github.com/ory/dockertest"
	"github.com/ory/dockertest/docker"
)

type MockDB struct {
	pool     *dockertest.Pool
	resource *dockertest.Resource
	backend  *pg.DB
}

func NewMockDB() MockDB {
	return MockDB{}
}

func (db *MockDB) GetDBHost() string     { return "localhost" }
func (db *MockDB) GetDBPort() string     { return db.resource.GetPort("5432/tcp") }
func (db *MockDB) GetDBUser() string     { return "postgres" }
func (db *MockDB) GetDBPassword() string { return "secret" }
func (db *MockDB) GetDBName() string     { return "postgres" }

func (db *MockDB) Init() {
	pool, err := dockertest.NewPool("")
	if err != nil {
		log.Fatalf("could not construct pool: %s", err)
	}
	db.pool = pool
	if err = pool.Client.Ping(); err != nil {
		log.Fatalf("could not connect to Docker: %s", err)
	}
	resource, err := pool.RunWithOptions(&dockertest.RunOptions{
		Repository: "postgres",
		Tag:        "17",
		Env: []string{
			"POSTGRES_USER=" + db.GetDBUser(),
			"POSTGRES_PASSWORD=" + db.GetDBPassword(),
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
	db.resource = resource
	if err := db.resource.Expire(60); err != nil {
		log.Fatalf("could not set expiration: %s", err)
	}
	if err = pool.Retry(func() error {
		db.backend = pg.Connect(&pg.Options{
			Addr:     fmt.Sprintf("%s:%s", db.GetDBHost(), resource.GetPort("5432/tcp")),
			User:     db.GetDBUser(),
			Password: db.GetDBPassword(),
			Database: db.GetDBName(),
		})
		if err != nil {
			log.Fatalf("could not connect to Docker: %s", err)
		}
		return db.backend.Ping(context.Background())
	}); err != nil {
		log.Fatalf("could not connect to Docker: %s", err)
	}
}

func (db *MockDB) Get() *pg.DB {
	if db.backend == nil {
		db.Init()
	}
	return db.backend
}

func (db *MockDB) Close() {
	if err := db.pool.Purge(db.resource); err != nil {
		log.Fatalf("could not purge resource: %s", err)
	}
}
