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
	m.Migrate()

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

	m.Migrate()
}
