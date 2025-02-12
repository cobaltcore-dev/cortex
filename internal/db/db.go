// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package db

import (
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/go-gorp/gorp"
	_ "github.com/lib/pq"
)

// Wrapper around gorp.DbMap that adds some convenience functions.
type DB struct {
	*gorp.DbMap
	DBConfig conf.DBConfig
}

type Table interface {
	TableName() string
}

// Parse the database configuration into a connection string.
func parseConnOpts(c conf.DBConfig) string {
	opts := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		c.Host, c.Port, c.User, c.Password, c.Database,
	)
	// Strip any newlines that may have been added by the yaml parser.
	return strings.ReplaceAll(opts, "\n", "")
}

// Create a new postgres database and wait until it is connected.
func NewPostgresDB(c conf.DBConfig) DB {
	psqlInfo := parseConnOpts(c)
	slog.Info("connecting to database", "psqlInfo", psqlInfo)
	db, err := sql.Open("postgres", psqlInfo)
	if err != nil {
		panic(err)
	}

	var sqlDB *sql.DB
	// If the wait time exceeds 10 seconds, we will panic.
	maxRetries := 10
	for i := range maxRetries {
		if err := db.Ping(); err == nil {
			sqlDB = db
			break
		}
		if i == maxRetries-1 {
			panic("failed to connect to database")
		}
		slog.Error("failed to connect to database, retrying...", "error", err)
		time.Sleep(1 * time.Second)
	}

	sqlDB.SetMaxOpenConns(16)
	dbMap := &gorp.DbMap{Db: sqlDB, Dialect: gorp.PostgresDialect{}}
	slog.Info("database is ready")
	return DB{DBConfig: c, DbMap: dbMap}
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
	query := `SELECT EXISTS (
		SELECT 1
		FROM   information_schema.tables
		WHERE  table_name = :table_name
	);`
	var exists bool
	err := d.SelectOne(&exists, query, map[string]any{"table_name": t.TableName()})
	if err != nil {
		slog.Error("failed to check if table exists", "error", err)
		return false
	}
	return exists
}

// Convenience function to the database connection.
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
	if err := u.Insert(model); err != nil {
		if strings.Contains(err.Error(), "duplicate key value violates unique constraint") {
			if _, err := u.Update(model); err != nil {
				return err
			}
		}
	}
	return nil
}
