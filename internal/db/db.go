// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package db

import (
	"database/sql"
	"log/slog"
	"strings"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/go-gorp/gorp"
	_ "github.com/lib/pq"
	"github.com/sapcc/go-bits/easypg"
)

// Wrapper around gorp.DbMap that adds some convenience functions.
type DB struct {
	*gorp.DbMap
}

type Table interface {
	TableName() string
}

// Create a new postgres database and wait until it is connected.
func NewPostgresDB(c conf.DBConfig) DB {
	stripYaml := func(s string) string { return strings.ReplaceAll(s, "\n", "") }
	dbURL, err := easypg.URLFrom(easypg.URLParts{
		HostName:          stripYaml(c.Host),
		Port:              stripYaml(c.Port),
		UserName:          stripYaml(c.User),
		Password:          stripYaml(c.Password),
		ConnectionOptions: "sslmode=disable",
		DatabaseName:      stripYaml(c.Database),
	})
	if err != nil {
		panic(err)
	}
	slog.Info("connecting to database", "dbURL", dbURL.String())
	db, err := sql.Open("postgres", dbURL.String())
	if err != nil {
		panic(err)
	}

	// If the wait time exceeds 10 seconds, we will panic.
	maxRetries := 10
	for i := range maxRetries {
		err := db.Ping()
		if err == nil {
			break
		}
		if i == maxRetries-1 {
			panic("giving up connecting to database")
		}
		slog.Error("failed to connect to database, retrying...", "error", err)
		time.Sleep(1 * time.Second)
	}

	db.SetMaxOpenConns(16)
	dbMap := &gorp.DbMap{Db: db, Dialect: gorp.PostgresDialect{}}
	slog.Info("database is ready")
	return DB{DbMap: dbMap}
}

// Adds missing functionality to gorp.DbMap which creates one table.
func (d *DB) CreateTable(table ...*gorp.TableMap) error {
	tx, err := d.Begin()
	if err != nil {
		slog.Error("failed to begin transaction", "error", err)
		return tx.Rollback()
	}
	for _, t := range table {
		slog.Info("creating table", "table", t.TableName)
		sql := t.SqlForCreate(true) // true means to add IF NOT EXISTS
		if _, err := tx.Exec(sql); err != nil {
			return tx.Rollback()
		}
	}
	return tx.Commit()
}

// Adds a Model table to the database.
func (d *DB) AddTable(t Table) *gorp.TableMap {
	slog.Info("adding table", "table", t.TableName(), "model", t)
	return d.AddTableWithName(t, t.TableName())
}

// Check if a table exists in the database.
func (d *DB) TableExists(t Table) bool {
	var query string
	switch d.DbMap.Dialect.(type) {
	case gorp.PostgresDialect:
		query = `SELECT EXISTS (
			SELECT 1
			FROM   information_schema.tables
			WHERE  table_name = :table_name
		);`
	case gorp.SqliteDialect:
		query = `SELECT EXISTS (
			SELECT 1
			FROM sqlite_master
			WHERE type='table' AND name = :table_name
		);`
	default:
		slog.Error("unsupported database dialect")
		return false
	}
	var exists bool
	err := d.SelectOne(&exists, query, map[string]any{"table_name": t.TableName()})
	if err != nil {
		slog.Error("failed to check if table exists", "error", err)
		return false
	}
	return exists
}

// Convenience function to close the database connection.
func (d *DB) Close() {
	if err := d.DbMap.Db.Close(); err != nil {
		slog.Error("failed to close database connection", "error", err)
	}
}

// Database or transaction that supports update and insert methods.
type upsertable interface {
	Update(list ...interface{}) (int64, error)
	Insert(list ...interface{}) error
}

// Upsert a model into the database (Insert if possible, otherwise Update).
func Upsert(u upsertable, model any) error {
	// This is a hacky way to check if the error is a duplicate key error.
	// In the future, we should look for a different solution.
	errmsgs := []string{
		"duplicate key value violates unique constraint", // postgres
		"UNIQUE constraint failed",                       // sqlite
	}

	if err := u.Insert(model); err != nil {
		slog.Error("failed to insert model", "error", err)
		for _, errmsg := range errmsgs {
			if strings.Contains(err.Error(), errmsg) {
				if _, err := u.Update(model); err != nil {
					return err
				}
				return nil
			}
		}
	}
	return nil
}
