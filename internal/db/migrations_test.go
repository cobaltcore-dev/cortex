// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package db

import (
	"testing"

	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

type test struct {
	ID int `db:"id, primarykey"`
}

func (test) TableName() string { return "test" }

func TestMigrate(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	db := DB{DbMap: dbEnv.DbMap}
	defer db.Close()
	defer dbEnv.Close()

	migrations := map[string]string{
		"001_create_table.sql": "CREATE TABLE test (id INT);",
		"002_insert_data.sql":  "INSERT INTO test (id) VALUES (1);",
	}

	m := &migrater{db: db, migrations: migrations}
	m.Migrate(false)

	if !db.TableExists(test{}) {
		t.Fatal("expected table to exist")
	}
}

func TestMigrateWithFailure(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	db := DB{DbMap: dbEnv.DbMap}
	defer db.Close()
	defer dbEnv.Close()

	migrations := map[string]string{
		"001_create_table.sql": "CREATE TABLE test (id INT);",
		"002_fail.sql":         "FAIL",
	}

	m := &migrater{db: db, migrations: migrations}

	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic, but code did not panic")
		}
	}()

	m.Migrate(false)
}

func TestNewMigrater(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	db := DB{DbMap: dbEnv.DbMap}
	defer db.Close()
	defer dbEnv.Close()

	m := NewMigrater(db)
	if m == nil {
		t.Fatal("expected migrater to be created")
	}

	if len(m.(*migrater).migrations) == 0 {
		t.Fatal("expected migrations to be read")
	}
}

func TestMigrateEmptyMigrations(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	db := DB{DbMap: dbEnv.DbMap}
	defer db.Close()
	defer dbEnv.Close()

	// No migrations provided
	migrations := map[string]string{}

	m := &migrater{db: db, migrations: migrations}
	m.Migrate(false)

	// Ensure the migrations table is created even if no migrations exist
	if !db.TableExists(Migration{}) {
		t.Fatal("expected migrations table to exist")
	}
}

func TestMigratePartialExecution(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	db := DB{DbMap: dbEnv.DbMap}
	defer db.Close()
	defer dbEnv.Close()

	migrations := map[string]string{
		"001_create_table.sql": "CREATE TABLE test (id INT);",
		"002_insert_data.sql":  "INSERT INTO test (id) VALUES (1);",
		"003_fail.sql":         "INVALID SQL;",
	}

	m := &migrater{db: db, migrations: migrations}

	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic, but code did not panic")
		}
	}()

	// Run migrations, expecting a failure
	m.Migrate(false)

	// Ensure only the first migration was executed
	if !db.TableExists(test{}) {
		t.Fatal("expected table 'test' to exist")
	}

	// Ensure the second migration was not executed due to failure
	var count int
	err := db.SelectOne(&count, "SELECT COUNT(*) FROM test")
	if err != nil {
		t.Fatalf("unexpected error querying test table: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no rows in 'test' table, got %d", count)
	}
}

func TestMigrateFreshDatabase(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	db := DB{DbMap: dbEnv.DbMap}
	defer db.Close()
	defer dbEnv.Close()

	migrations := map[string]string{
		"001_create_table.sql": "CREATE TABLE test (id INT);",
		"002_insert_data.sql":  "INSERT INTO test (id) VALUES (1);",
	}

	m := &migrater{db: db, migrations: migrations}
	m.Migrate(false)

	// Ensure all migrations were executed
	if !db.TableExists(test{}) {
		t.Fatal("expected table 'test' to exist")
	}

	// Ensure data was inserted
	var count int
	err := db.SelectOne(&count, "SELECT COUNT(*) FROM test")
	if err != nil {
		t.Fatalf("unexpected error querying test table: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 row in 'test' table, got %d", count)
	}
}

func TestMigrateAlreadyExecuted(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	db := DB{DbMap: dbEnv.DbMap}
	defer db.Close()
	defer dbEnv.Close()

	migrations := map[string]string{
		"001_create_table.sql": "CREATE TABLE test (id INT);",
		"002_insert_data.sql":  "INSERT INTO test (id) VALUES (1);",
	}

	m := &migrater{db: db, migrations: migrations}
	m.Migrate(false)

	// Run migrations again
	m.Migrate(false)

	// Ensure no duplicate data was inserted
	var count int
	err := db.SelectOne(&count, "SELECT COUNT(*) FROM test")
	if err != nil {
		t.Fatalf("unexpected error querying test table: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 row in 'test' table, got %d", count)
	}
}
