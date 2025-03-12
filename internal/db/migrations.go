// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package db

import (
	"embed"
	"log/slog"
	"sort"
)

// Migration files that should be executed before services are started.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

type Migrater interface {
	Migrate()
}

type migrater struct {
	migrations map[string]string
	db         DB
}

// Create a new migrater with files embedded in the binary.
func NewMigrater(db DB) Migrater {
	// Read the embedded migration files.
	migrations := map[string]string{}
	files, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		panic(err)
	}
	for _, file := range files {
		if file.IsDir() {
			panic("migrations directory contains a directory")
		}
		content, err := migrationFiles.ReadFile("migrations/" + file.Name())
		if err != nil {
			panic(err)
		}
		migrations[file.Name()] = string(content)
	}
	return &migrater{db: db, migrations: migrations}
}

// Run all migrations ordered by their file name.
func (m *migrater) Migrate() {
	migrationFileNames := make([]string, 0, len(m.migrations))
	for name := range m.migrations {
		migrationFileNames = append(migrationFileNames, name)
	}
	sort.Strings(migrationFileNames)
	for _, name := range migrationFileNames {
		migration := m.migrations[name]
		slog.Info("executing migration", "name", name)
		if _, err := m.db.Exec(migration); err != nil {
			panic(err)
		}
	}
	slog.Info("migrations executed")
}
