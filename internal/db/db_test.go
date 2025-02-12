// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package db

import (
	"database/sql"
	"fmt"
	"log"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	_ "github.com/lib/pq"
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

type MockTable struct {
	ID   int    `db:"id,primarykey"`
	Name string `db:"name"`
}

func (m MockTable) TableName() string {
	return "mock_table"
}

func TestNewDB(t *testing.T) {
	container := PostgresContainer{}
	container.Init(t)
	defer container.Close()

	config := conf.DBConfig{
		Host:     "localhost",
		Port:     container.GetPort(),
		User:     "postgres",
		Password: "secret",
		Database: "postgres",
	}

	db := NewPostgresDB(config)
	db.Close()
}

func TestDB_CreateTable(t *testing.T) {
	container := PostgresContainer{}
	container.Init(t)
	defer container.Close()

	config := conf.DBConfig{
		Host:     "localhost",
		Port:     container.GetPort(),
		User:     "postgres",
		Password: "secret",
		Database: "postgres",
	}

	db := NewPostgresDB(config)
	defer db.Close()

	table := db.AddTable(MockTable{})
	err := db.CreateTable(table)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !db.TableExists(MockTable{}) {
		t.Fatal("expected table to exist")
	}
}

func TestDB_AddTable(t *testing.T) {
	container := PostgresContainer{}
	container.Init(t)
	defer container.Close()

	config := conf.DBConfig{
		Host:     "localhost",
		Port:     container.GetPort(),
		User:     "postgres",
		Password: "secret",
		Database: "postgres",
	}

	db := NewPostgresDB(config)
	defer db.Close()

	table := db.AddTable(MockTable{})
	if table == nil {
		t.Fatal("expected table to be added")
	}
}

func TestDB_TableExists(t *testing.T) {
	container := PostgresContainer{}
	container.Init(t)
	defer container.Close()

	config := conf.DBConfig{
		Host:     "localhost",
		Port:     container.GetPort(),
		User:     "postgres",
		Password: "secret",
		Database: "postgres",
	}

	db := NewPostgresDB(config)
	defer db.Close()

	table := db.AddTable(MockTable{})
	err := db.CreateTable(table)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !db.TableExists(MockTable{}) {
		t.Fatal("expected table to exist")
	}
}

func TestDB_Close(t *testing.T) {
	container := PostgresContainer{}
	container.Init(t)
	defer container.Close()

	config := conf.DBConfig{
		Host:     "localhost",
		Port:     container.GetPort(),
		User:     "postgres",
		Password: "secret",
		Database: "postgres",
	}

	db := NewPostgresDB(config)
	db.Close()

	if err := db.DbMap.Db.Ping(); err == nil {
		t.Fatal("expected error, got nil")
	}
}
