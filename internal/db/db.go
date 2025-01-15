// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package db

//nolint:goimports
import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/logging"
	"github.com/go-pg/pg/v10"
)

// Global database connection.
var db *pg.DB

var connectLock = &sync.Mutex{}

// Initializes the database connection.
func connect() {
	if db != nil {
		return
	}
	c := conf.Get()
	db = pg.Connect(&pg.Options{
		Addr:     fmt.Sprintf("%s:%s", c.DBHost, c.DBPort),
		User:     c.DBUser,
		Password: c.DBPass,
		Database: "postgres",
	})

	// Poll until the database is alive
	logging.Log.Info("waiting for database to be ready...")
	ctx := context.Background()
	var i int
	for {
		err := db.Ping(ctx)
		if err == nil {
			break
		}
		i++
		if i > 10 {
			// Give up after 10 seconds
			panic(err)
		}
		logging.Log.Info("database is not ready yet, retrying", "attempt", i)
		time.Sleep(time.Second * 1)
	}
	logging.Log.Info("database is ready")
}

// Returns the global database connection.
// If the connection is not initialized, it will be initialized.
func Get() *pg.DB {
	if db == nil {
		// Don't init the db twice.
		connectLock.Lock()
		defer connectLock.Unlock()
		connect()
	}
	return db
}
