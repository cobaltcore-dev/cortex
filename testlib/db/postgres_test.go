// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package db

import "testing"

func TestNewPostgresTestDB(t *testing.T) {
	testDB := NewPostgresTestDB(t)
	defer testDB.Close()

	if testDB.DB == nil {
		t.Fatal("expected DB to be initialized")
	}

	if err := testDB.DbMap.Db.Ping(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}
