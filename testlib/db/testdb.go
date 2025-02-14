// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package db

import (
	"database/sql"
	"log"
	"log/slog"
	"os"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/cobaltcore-dev/cortex/testlib/containers"
	"github.com/go-gorp/gorp"
	_ "github.com/mattn/go-sqlite3"
)

type DBEnv struct {
	*db.DB
	Close func()
}

func SetupDBEnv(t *testing.T) DBEnv {
	var env DBEnv
	// To run tests faster, the default is running with sqlite.
	if os.Getenv("POSTGRES_CONTAINER") == "1" {
		slog.Info("Using real postgres container")
		container := containers.PostgresContainer{}
		container.Init(t)
		db := db.NewPostgresDB(conf.DBConfig{
			Host:     "localhost",
			Port:     container.GetPort(),
			User:     "postgres",
			Password: "secret",
			Database: "postgres",
		})
		env.DB = &db
		env.Close = func() {
			env.DB.Close()
			container.Close()
		}
	} else {
		slog.Info("Using sqlite")
		tmpDir := t.TempDir()
		sqlDB, err := sql.Open("sqlite3", tmpDir+"/test.db")
		if err != nil {
			t.Fatal(err)
		}
		env.DB = &db.DB{}
		env.DB.DbMap = &gorp.DbMap{Db: sqlDB, Dialect: gorp.SqliteDialect{}}
		env.Close = func() {
			env.DB.Close()
		}
	}
	env.DB.DbMap.TraceOn("[gorp]", log.New(os.Stdout, "cortex:", log.Lmicroseconds))
	return env
}
