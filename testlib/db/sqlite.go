// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package db

import (
	"database/sql"
	"log"
	"os"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/go-gorp/gorp"
	_ "github.com/mattn/go-sqlite3"
)

type SqliteTestDB struct {
	*db.DB
}

func NewSqliteTestDB(t *testing.T) SqliteTestDB {
	tmpDir := t.TempDir()
	sqlDB, err := sql.Open("sqlite3", tmpDir+"/test.db")
	if err != nil {
		t.Fatal(err)
	}
	d := SqliteTestDB{DB: &db.DB{}}
	dbmap := &gorp.DbMap{Db: sqlDB, Dialect: gorp.SqliteDialect{}}
	dbmap.TraceOn("[gorp]", log.New(os.Stdout, "cortex:", log.Lmicroseconds))
	d.DbMap = dbmap
	return d
}

func (db *SqliteTestDB) GetDB() *db.DB { return db.DB }

// Check if a table exists in the database.
// Note: This overrides the method in db.DB, because sqlite needs
// a different query to check if a table exists.
func (d *SqliteTestDB) TableExists(table db.Table) bool {
	query := "SELECT name FROM sqlite_master WHERE type='table' AND name = :name"
	var name string
	err := d.SelectOne(&name, query, map[string]interface{}{"name": table.TableName()})
	return err == nil
}
