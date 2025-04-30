// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package db

import (
	"os"
	"strconv"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
	"github.com/cobaltcore-dev/cortex/testlib/db/containers"
)

type MockTable struct {
	ID   int    `db:"id,primarykey"`
	Name string `db:"name"`
}

func (m MockTable) TableName() string {
	return "mock_table"
}

func TestNewDB(t *testing.T) {
	if os.Getenv("POSTGRES_CONTAINER") != "1" {
		t.Skip("skipping test; set POSTGRES_CONTAINER=1 to run")
	}
	container := containers.PostgresContainer{}
	container.Init(t)
	defer container.Close()

	port, err := strconv.Atoi(container.GetPort())
	if err != nil {
		t.Fatalf("failed to convert port: %v", err)
	}
	config := conf.DBConfig{
		Host:     "localhost",
		Port:     port,
		User:     "postgres",
		Password: "secret",
		Database: "postgres",
	}

	db := NewPostgresDB(config, nil)
	db.Close()
}

func TestDB_CreateTable(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	db := DB{DbMap: dbEnv.DbMap}
	defer db.Close()
	defer dbEnv.Close()

	table := db.AddTable(MockTable{})
	err := db.CreateTable(table)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !db.TableExists(MockTable{}) {
		t.Fatal("expected table to exist")
	}
}

func TestDB_AddTable(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	db := DB{DbMap: dbEnv.DbMap}
	defer db.Close()
	defer dbEnv.Close()

	table := db.AddTable(MockTable{})
	if table == nil {
		t.Fatal("expected table to be added")
	}
}

func TestDB_TableExists(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	db := DB{DbMap: dbEnv.DbMap}
	defer db.Close()
	defer dbEnv.Close()

	table := db.AddTable(MockTable{})
	err := db.CreateTable(table)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !db.TableExists(MockTable{}) {
		t.Fatal("expected table to exist")
	}
}

func TestDB_Close(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	db := DB{DbMap: dbEnv.DbMap}
	db.Close()
	defer dbEnv.Close()

	if err := db.Db.Ping(); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestReplaceAll(t *testing.T) {
	dbEnv := testlibDB.SetupDBEnv(t)
	db := DB{DbMap: dbEnv.DbMap}
	defer db.Close()
	defer dbEnv.Close()

	table := db.AddTable(MockTable{})
	err := db.CreateTable(table)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert initial records
	initialRecords := []MockTable{
		{ID: 1, Name: "record1"},
		{ID: 2, Name: "record2"},
	}
	for _, record := range initialRecords {
		err = db.Insert(&record)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	}

	// Replace with new records
	newRecords := []MockTable{
		{ID: 1, Name: "new_record1"},
		{ID: 4, Name: "new_record2"},
	}
	err = ReplaceAll(db, newRecords...)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify old records are deleted
	var count int
	err = db.SelectOne(&count, "SELECT COUNT(*) FROM mock_table WHERE id IN (1, 2)")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 old records, got %d", count)
	}

	// Verify new records are inserted
	err = db.SelectOne(&count, "SELECT COUNT(*) FROM mock_table WHERE id IN (3, 4)")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 new records, got %d", count)
	}
}
