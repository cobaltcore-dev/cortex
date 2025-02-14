// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package db

import (
	"log"
	"os"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/testlib/containers"
)

type PostgresTestDB struct {
	*db.DB
	container containers.PostgresContainer
}

func NewPostgresTestDB(t *testing.T) PostgresTestDB {
	container := containers.PostgresContainer{}
	container.Init(t)

	db := db.NewPostgresDB(conf.DBConfig{
		Host:     "localhost",
		Port:     container.GetPort(),
		User:     "postgres",
		Password: "secret",
		Database: "postgres",
	})
	testDB := PostgresTestDB{DB: &db, container: container}
	testDB.DbMap.TraceOn("[gorp]", log.New(os.Stdout, "cortex:", log.Lmicroseconds))
	return testDB
}

func (db *PostgresTestDB) GetDB() *db.DB { return db.DB }

func (db *PostgresTestDB) Close() {
	db.DB.Close()
	db.container.Close()
}
