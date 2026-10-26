// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package db

import (
	"database/sql"
	stdlog "log"
	"os"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/knowledge/db/testing/containers"
	"github.com/go-gorp/gorp"
	_ "github.com/mattn/go-sqlite3"
	"github.com/sapcc/go-api-declarations/bininfo"
	"go.xyrillian.de/gg/pgruntime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var log = logf.Log.WithName("db-testing")

type DBEnv struct {
	*gorp.DbMap
	Close func()
}

func SetupDBEnv(t *testing.T) DBEnv {
	var env DBEnv
	// To run tests faster, the default is running with sqlite.
	if os.Getenv("POSTGRES_CONTAINER") == "1" {
		log.Info("Using real postgres container")
		container := containers.PostgresContainer{}
		container.Init(t)
		target := pgruntime.ConnectionTarget{
			HostName:          "localhost",
			Port:              container.GetPort(),
			UserName:          "postgres",
			Password:          "secret",
			ConnectionOptions: "sslmode=disable",
			DatabaseName:      "postgres",
			ApplicationName:   bininfo.Component(),
		}
		dbURL, err := target.IntoURL()
		if err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open("postgres", dbURL.String())
		if err != nil {
			t.Fatal(err)
		}
		env.DbMap = &gorp.DbMap{Db: db, Dialect: gorp.PostgresDialect{}}
		env.Close = container.Close
	} else {
		log.Info("Using sqlite")
		tmpDir := t.TempDir()
		sqlDB, err := sql.Open("sqlite3", tmpDir+"/test.db")
		if err != nil {
			t.Fatal(err)
		}
		env.DbMap = &gorp.DbMap{Db: sqlDB, Dialect: gorp.SqliteDialect{}}
		env.Close = func() {}
	}
	env.TraceOn("[gorp]", stdlog.New(os.Stdout, "cortex:", stdlog.Lmicroseconds))
	return env
}
