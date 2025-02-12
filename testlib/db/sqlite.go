// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package db

import (
	"database/sql"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/db"
	"github.com/go-gorp/gorp"
	_ "github.com/mattn/go-sqlite3"
)

type SqliteMockDB struct {
	*db.DB
}

func NewSqliteMockDB() SqliteMockDB {
	return SqliteMockDB{DB: &db.DB{}}
}

func (db *SqliteMockDB) Init(t *testing.T) {
	tmpDir := t.TempDir()
	sqlDB, err := sql.Open("sqlite3", tmpDir+"/test.db")
	if err != nil {
		t.Fatal(err)
	}
	db.DbMap = &gorp.DbMap{Db: sqlDB, Dialect: gorp.SqliteDialect{}}
}

// Check if a table exists in the database.
// Note: This overrides the method in db.DB, because sqlite needs
// a different query to check if a table exists.
func (db *SqliteMockDB) TableExists(table db.Table) bool {
	query := "SELECT name FROM sqlite_master WHERE type='table' AND name = :name"
	var name string
	err := db.SelectOne(&name, query, map[string]interface{}{"name": table.TableName()})
	return err == nil
}
