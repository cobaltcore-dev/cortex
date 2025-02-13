// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package db

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/testlib/containers"
)

type MockTable struct {
	ID   int    `db:"id,primarykey"`
	Name string `db:"name"`
}

func (m MockTable) TableName() string {
	return "mock_table"
}

func TestNewDB(t *testing.T) {
	container := containers.PostgresContainer{}
	container.Init(t)
	defer container.Close()

	config := conf.DBConfig{
		Host:     "localhost",
		Port:     container.GetPort(),
		User:     "postgres",
		Password: "secret",
		Database: "postgres",
	}

	db := NewPostgresDB(config)
	db.Close()
}

func TestDB_CreateTable(t *testing.T) {
	container := containers.PostgresContainer{}
	container.Init(t)
	defer container.Close()

	config := conf.DBConfig{
		Host:     "localhost",
		Port:     container.GetPort(),
		User:     "postgres",
		Password: "secret",
		Database: "postgres",
	}

	db := NewPostgresDB(config)
	defer db.Close()

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
	container := containers.PostgresContainer{}
	container.Init(t)
	defer container.Close()

	config := conf.DBConfig{
		Host:     "localhost",
		Port:     container.GetPort(),
		User:     "postgres",
		Password: "secret",
		Database: "postgres",
	}

	db := NewPostgresDB(config)
	defer db.Close()

	table := db.AddTable(MockTable{})
	if table == nil {
		t.Fatal("expected table to be added")
	}
}

func TestDB_TableExists(t *testing.T) {
	container := containers.PostgresContainer{}
	container.Init(t)
	defer container.Close()

	config := conf.DBConfig{
		Host:     "localhost",
		Port:     container.GetPort(),
		User:     "postgres",
		Password: "secret",
		Database: "postgres",
	}

	db := NewPostgresDB(config)
	defer db.Close()

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
	container := containers.PostgresContainer{}
	container.Init(t)
	defer container.Close()

	config := conf.DBConfig{
		Host:     "localhost",
		Port:     container.GetPort(),
		User:     "postgres",
		Password: "secret",
		Database: "postgres",
	}

	db := NewPostgresDB(config)
	db.Close()

	if err := db.DbMap.Db.Ping(); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestUpsert(t *testing.T) {
	container := containers.PostgresContainer{}
	container.Init(t)
	defer container.Close()

	config := conf.DBConfig{
		Host:     "localhost",
		Port:     container.GetPort(),
		User:     "postgres",
		Password: "secret",
		Database: "postgres",
	}

	db := NewPostgresDB(config)
	defer db.Close()

	table := db.AddTable(MockTable{})
	err := db.CreateTable(table)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Insert a new record
	mockRecord := MockTable{ID: 1, Name: "test"}
	err = Upsert(db, &mockRecord)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify the record was inserted
	var insertedRecord MockTable
	err = db.SelectOne(&insertedRecord, "SELECT * FROM mock_table WHERE id = $1", mockRecord.ID)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if insertedRecord.Name != "test" {
		t.Errorf("expected name to be 'test', got %s", insertedRecord.Name)
	}

	// Update the existing record
	mockRecord.Name = "updated"
	err = Upsert(db, &mockRecord)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify the record was updated
	var updatedRecord MockTable
	err = db.SelectOne(&updatedRecord, "SELECT * FROM mock_table WHERE id = $1", mockRecord.ID)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if updatedRecord.Name != "updated" {
		t.Errorf("expected name to be 'updated', got %s", updatedRecord.Name)
	}
}
