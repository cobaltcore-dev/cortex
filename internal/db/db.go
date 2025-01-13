// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"
	"fmt"
	"time"

	"github.com/cobaltcore-dev/cortex/internal/conf"
	"github.com/cobaltcore-dev/cortex/internal/logging"
	"github.com/go-pg/pg/v10"
)

var DB *pg.DB

func Init() {
	c := conf.Get()
	DB = pg.Connect(&pg.Options{
		Addr:     fmt.Sprintf("%s:%s", c.DBHost, c.DBPort),
		User:     c.DBUser,
		Password: c.DBPass,
		Database: "postgres",
	})

	// Poll until the database is alive
	logging.Log.Info("waiting for database to be ready...")
	ctx := context.Background()
	for {
		if err := DB.Ping(ctx); err == nil {
			break
		}
		time.Sleep(time.Second * 1)
	}
	logging.Log.Info("database is ready")
}
