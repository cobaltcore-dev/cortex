// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/logging"
	"github.com/go-pg/pg/v10"
)

type DB interface {
	Init()
	Get() *pg.DB
	Close()
}

type db struct {
	DBBackend *pg.DB
	DBConfig  DBConfig
}

func NewDB() DB {
	return &db{
		DBConfig: NewDBConfig(),
	}
}

// Initializes the database connection.
func (d *db) Init() {
	if d.DBBackend != nil {
		return
	}
	c := d.DBConfig
	opts := &pg.Options{
		Addr:     c.GetDBHost() + ":" + c.GetDBPort(),
		User:     c.GetDBUser(),
		Password: c.GetDBPassword(),
		Database: c.GetDBName(),
	}
	d.DBBackend = pg.Connect(opts)

	// Poll until the database is alive
	logging.Log.Info("waiting for database to be ready...")
	ctx := context.Background()
	var i int
	for {
		err := d.DBBackend.Ping(ctx)
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
func (d *db) Get() *pg.DB {
	if d.DBBackend == nil {
		d.Init()
	}
	return d.DBBackend
}

func (d *db) Close() {
	if d.DBBackend != nil {
		err := d.DBBackend.Close()
		if err != nil {
			logging.Log.Error("failed to close database connection", "error", err)
		}
	}
}
