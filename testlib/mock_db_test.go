// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package testlib

import (
	"context"
	"fmt"
	"testing"

	"github.com/go-pg/pg/v10"
)

func TestMain(m *testing.M) {
	WithMockDB(m, 5)
}

func TestMockDB(t *testing.T) {
	db := pg.Connect(&pg.Options{
		Addr:     fmt.Sprintf("%s:%s", "localhost", "5432"),
		User:     "postgres",
		Password: "secret",
		Database: "postgres",
	})
	if db == nil {
		t.Errorf("expected db to be initialized")
		t.FailNow()
	}
	if err := db.Ping(context.Background()); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}
