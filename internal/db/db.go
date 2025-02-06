// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"
	"log/slog"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/go-pg/pg/v10"
)

type DB interface {
	Init()
	Get() *pg.DB
	Close()
}

type db struct {
	DBBackend *pg.DB
	DBConfig  conf.DBConfig
}

func NewDB(conf conf.DBConfig) DB {
	return &db{DBConfig: conf}
}

// Initializes the database connection.
func (d *db) Init() {
	if d.DBBackend != nil {
		return
	}
	c := d.DBConfig
	opts := &pg.Options{
		Addr:     c.Host + ":" + c.Port,
		User:     c.User,
		Password: c.Password,
		Database: c.Name,
	}
	d.DBBackend = pg.Connect(opts)

	// Poll until the database is alive
	slog.Info("waiting for database to be ready...")
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
		slog.Info("database is not ready yet, retrying", "attempt", i)
		time.Sleep(time.Second * 1)
	}
	slog.Info("database is ready")
}

// Returns the global database connection.
// If the connection is not initialized, it will be initialized.
func (d *db) Get() *pg.DB {
	if d.DBBackend == nil {
		d.Init()
	}
	return d.DBBackend
}

// Closes the database connection.
func (d *db) Close() {
	if d.DBBackend == nil {
		return
	}
	if err := d.DBBackend.Close(); err != nil {
		slog.Error("failed to close database connection", "error", err)
	}
}
