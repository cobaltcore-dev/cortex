// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"
	"testing"

	"github.com/go-pg/pg/v10"
)

func TestMockDB(t *testing.T) {
	mockDB := NewMockDB()
	mockDB.Init()
	defer mockDB.Close()

	db := pg.Connect(&pg.Options{
		Addr:     mockDB.GetDBHost() + ":" + mockDB.GetDBPort(),
		User:     mockDB.GetDBUser(),
		Password: mockDB.GetDBPassword(),
		Database: mockDB.GetDBName(),
	})
	if db == nil {
		t.Errorf("expected db to be initialized")
		t.FailNow()
	}
	if err := db.Ping(context.Background()); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}
