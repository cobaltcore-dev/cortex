// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package db

import (
	"testing"
)

type MockTable struct {
	ID   int    `db:"id,primarykey"`
	Name string `db:"name"`
}

func (m MockTable) TableName() string {
	return "mock_table"
}

func TestSqliteTestDB_Init(t *testing.T) {
	testDB := NewSqliteTestDB(t)

	if testDB.DbMap == nil {
		t.Fatal("expected DbMap to be initialized")
	}

	if err := testDB.DbMap.Db.Ping(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestSqliteTestDB_CreateTable(t *testing.T) {
	testDB := NewSqliteTestDB(t)

	table := testDB.AddTable(MockTable{})
	err := testDB.CreateTable(table)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !testDB.TableExists(MockTable{}) {
		t.Fatal("expected table to exist")
	}
}

func TestSqliteTestDB_TableExists(t *testing.T) {
	testDB := NewSqliteTestDB(t)

	table := testDB.AddTable(MockTable{})
	err := testDB.CreateTable(table)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !testDB.TableExists(MockTable{}) {
		t.Fatal("expected table to exist")
	}
}

func TestSqliteTestDB_Close(t *testing.T) {
	testDB := NewSqliteTestDB(t)

	testDB.Close()

	if err := testDB.DbMap.Db.Ping(); err == nil {
		t.Fatal("expected error, got nil")
	}
}
