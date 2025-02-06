// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package db

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	testlibDB "github.com/cobaltcore-dev/cortex/testlib/db"
)

func TestGet(t *testing.T) {
	mockDB := testlibDB.NewMockDB()
	mockDB.Init()
	defer mockDB.Close()

	db := &db{
		DBBackend: mockDB.Get(),
		DBConfig: conf.DBConfig{
			Host:     mockDB.GetDBHost(),
			Port:     mockDB.GetDBPort(),
			User:     mockDB.GetDBUser(),
			Password: mockDB.GetDBPassword(),
			Name:     mockDB.GetDBName(),
		},
	}
	db.Init()
	defer db.Close()
	if db.Get() == nil {
		t.Errorf("expected db to be initialized")
	}
}
