// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package db

import (
	"github.com/cobaltcore-dev/cortex/internal/env"
)

type DBConfig interface {
	GetDBHost() string
	GetDBPort() string
	GetDBUser() string
	GetDBPassword() string
	GetDBName() string
}

type dbConfig struct {
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string
}

func NewDBConfig() DBConfig {
	return &dbConfig{
		DBHost:     env.Getenv("POSTGRES_HOST", "localhost"),
		DBPort:     env.Getenv("POSTGRES_PORT", "5432"),
		DBUser:     env.Getenv("POSTGRES_USER", "postgres"),
		DBPassword: env.Getenv("POSTGRES_PASSWORD", "secret"),
		DBName:     env.Getenv("POSTGRES_DB", "postgres"),
	}
}

func (c *dbConfig) GetDBHost() string     { return c.DBHost }
func (c *dbConfig) GetDBPort() string     { return c.DBPort }
func (c *dbConfig) GetDBUser() string     { return c.DBUser }
func (c *dbConfig) GetDBPassword() string { return c.DBPassword }
func (c *dbConfig) GetDBName() string     { return c.DBName }
