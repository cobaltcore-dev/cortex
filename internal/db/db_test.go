// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package db

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/testlib"
)

func TestGet(t *testing.T) {
	mockDB := testlib.NewMockDB()
	mockDB.Init()
	defer mockDB.Close()

	db := &db{
		DBBackend: mockDB.Get(),
		DBConfig:  &mockDB,
	}
	db.Init()
	defer db.Close()
	if db.Get() == nil {
		t.Errorf("expected db to be initialized")
	}
}
