// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package db

import (
	"database/sql"
	"log/slog"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/monitoring"
	"github.com/dlmiddlecote/sqlstats"
	"github.com/go-gorp/gorp"
	_ "github.com/lib/pq"
	"github.com/sapcc/go-bits/easypg"
	"github.com/sapcc/go-bits/jobloop"
)

// Wrapper around gorp.DbMap that adds some convenience functions.
type DB struct {
	*gorp.DbMap
	// Monitor for database related metrics like connection attempts.
	monitor Monitor
}

type Table interface {
	TableName() string
	Indexes() []Index
}

type Index struct {
	Name        string
	ColumnNames []string
}

// Create a new postgres database and wait until it is connected.
func NewPostgresDB(c conf.DBConfig, registry *monitoring.Registry, monitor Monitor) DB {
	strip := func(s string) string { return strings.ReplaceAll(s, "\n", "") }
	dbURL, err := easypg.URLFrom(easypg.URLParts{
		HostName:          strip(c.Host),
		Port:              strconv.Itoa(c.Port),
		UserName:          strip(c.User),
		Password:          strip(c.Password),
		ConnectionOptions: "sslmode=disable",
		DatabaseName:      strip(c.Database),
	})
	if err != nil {
		panic(err)
	}
	// Strip the password from the URL for logging.
	slog.Info("connecting to database", "url", strings.ReplaceAll(dbURL.String(), strip(c.Password), "****"))
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
	if registry != nil {
		// Expose metrics for the database connection pool.
		registry.MustRegister(sqlstats.NewStatsCollector("cortex", db))
	}
	return DB{DbMap: dbMap, monitor: monitor}
}

// Check periodically if the database is alive. If not, panic.
func (d *DB) CheckLivenessPeriodically() {
	var failures int
	for {
		maxRetries := 20
		if err := d.Db.Ping(); err != nil {
			if failures > maxRetries {
				slog.Error("database is unreachable, giving up", "error", err)
				panic(err)
			}
			failures++
			if d.monitor.connectionAttempts != nil {
				d.monitor.connectionAttempts.Inc()
			}

			slog.Error("failed to ping database", "error", err, "attempt", failures)
			time.Sleep(jobloop.DefaultJitter(1 * time.Second))
			continue
		}
		failures = 0
		slog.Debug("check ok: database is reachable")
		time.Sleep(jobloop.DefaultJitter(10 * time.Second))
	}
}

// Adds missing functionality to gorp.DbMap which creates one table.
func (d *DB) CreateTable(table ...*gorp.TableMap) error {
	tx, err := d.Begin()
	if err != nil {
		slog.Error("failed to begin transaction", "error", err)
		return tx.Rollback()
	}
	for _, t := range table {
		slog.Info("creating table if exists", "table", t.TableName)
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
	tablemap := d.AddTableWithName(t, t.TableName())
	for _, index := range t.Indexes() {
		slog.Info("adding index", "index", index.Name, "table", t.TableName(), "columns", index.ColumnNames)
		tablemap.AddIndex(index.Name, "Btree", index.ColumnNames)
	}
	return tablemap
}

// Check if a table exists in the database.
func (d *DB) TableExists(t Table) bool {
	var query string
	switch d.Dialect.(type) {
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
	if err := d.Db.Close(); err != nil {
		slog.Error("failed to close database connection", "error", err)
	}
}

// Replace all old objects of a table with new objects.
func ReplaceAll[T Table](db DB, objs ...T) error {
	var model T
	tableName := model.TableName()
	tx, err := db.Begin()
	if err != nil {
		slog.Error("failed to begin transaction", "error", err)
		return tx.Rollback()
	}
	if _, err = tx.Exec("DELETE FROM " + tableName); err != nil {
		slog.Error("failed to delete old objects", "tableName", tableName, "error", err)
		return tx.Rollback()
	}
	if err = BulkInsert(tx, db, objs...); err != nil {
		slog.Error("failed to insert new objects", "tableName", tableName, "error", err)
		return tx.Rollback()
	}
	if err = tx.Commit(); err != nil {
		slog.Error("failed to commit transaction", "error", err)
		return err
	}
	return nil
}

// Bulk insert objects into the database, using an executor which
// can be a transaction or a database connection itself.
//
// Note: This function does NOT support auto-incrementing primary keys.
func BulkInsert[T Table](executor gorp.SqlExecutor, db DB, allObjs ...T) error {
	if len(allObjs) == 0 {
		// Nothing to do.
		return nil
	}

	// Commit every n objects to avoid running out of memory and avoid
	// hitting the database parameter limit.
	const batchSize = 1000

	for i := 0; i < len(allObjs); i += batchSize {
		end := min(i+batchSize, len(allObjs))
		objs := allObjs[i:end]
		// Detect the table based on the first object.
		objType := reflect.ValueOf(objs).Index(0).Type()
		table, err := db.TableFor(objType, false)
		if err != nil {
			slog.Error("failed to get table for object", "error", err)
			return err
		}

		// Using a strings.Builder is much faster than string concatenation.
		var builder strings.Builder
		builder.WriteString("INSERT INTO ")
		builder.WriteString(db.Dialect.QuotedTableForQuery(table.SchemaName, table.TableName))
		builder.WriteString(" (")

		// Build the column names.
		for idx, col := range table.Columns {
			if col.Transient {
				continue
			}
			builder.WriteString(db.Dialect.QuoteField(col.ColumnName))
			if idx < len(table.Columns)-1 {
				builder.WriteString(", ")
			}
		}
		builder.WriteString(") VALUES ")

		var params []any
		// Build the values.
		paramIdx := 0
		for i, obj := range objs {
			if i > 0 {
				builder.WriteString(", ")
			}
			builder.WriteString("(")
			for j, col := range table.Columns {
				if col.Transient {
					continue
				}
				val := reflect.ValueOf(obj).FieldByIndex([]int{j}).Interface()
				params = append(params, val)
				builder.WriteString(db.Dialect.BindVar(paramIdx))
				if j < len(table.Columns)-1 {
					builder.WriteString(", ")
				}
				paramIdx++
			}
			builder.WriteString(")")
		}

		builder.WriteString(db.Dialect.QuerySuffix())
		query := builder.String()

		slog.Debug("bulk inserting objects", "n", len(objs))
		if _, err = executor.Exec(query, params...); err != nil {
			slog.Error("failed to execute bulk insert", "error", err)
			return err
		}
	}
	return nil
}
