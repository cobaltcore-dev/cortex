// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package db

import (
	"os"
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/db"
)

type TestDB interface {
	Close()
	GetDB() *db.DB
}

func NewTestDB(t *testing.T) TestDB {
	// To run tests faster, the default is running with sqlite.
	psql := os.Getenv("WITH_REAL_POSTGRES_CONTAINER")
	if psql == "1" {
		db := NewPostgresTestDB(t)
		return &db
	}
	db := NewSqliteTestDB(t)
	return &db
}
