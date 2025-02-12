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

func TestSqliteMockDB_Init(t *testing.T) {
	mockDB := NewSqliteMockDB()
	mockDB.Init(t)

	if mockDB.DbMap == nil {
		t.Fatal("expected DbMap to be initialized")
	}

	if err := mockDB.DbMap.Db.Ping(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestSqliteMockDB_CreateTable(t *testing.T) {
	mockDB := NewSqliteMockDB()
	mockDB.Init(t)

	table := mockDB.AddTable(MockTable{})
	err := mockDB.CreateTable(table)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !mockDB.TableExists(MockTable{}) {
		t.Fatal("expected table to exist")
	}
}

func TestSqliteMockDB_TableExists(t *testing.T) {
	mockDB := NewSqliteMockDB()
	mockDB.Init(t)

	table := mockDB.AddTable(MockTable{})
	err := mockDB.CreateTable(table)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !mockDB.TableExists(MockTable{}) {
		t.Fatal("expected table to exist")
	}
}

func TestSqliteMockDB_Close(t *testing.T) {
	mockDB := NewSqliteMockDB()
	mockDB.Init(t)
	mockDB.Close()

	if err := mockDB.DbMap.Db.Ping(); err == nil {
		t.Fatal("expected error, got nil")
	}
}
