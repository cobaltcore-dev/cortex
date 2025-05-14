// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package db

import (
	"embed"
	"log/slog"
	"slices"
	"sort"
)

// Migration files that should be executed before services are started.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

type Migrater interface {
	Migrate(bool)
}

type migrater struct {
	migrations map[string]string
	db         DB
}

// Migration model to keep track which migrations have been executed.
type Migration struct {
	FileName string `db:"file_name"`
}

// Table under which the migration model will be stored.
func (Migration) TableName() string {
	return "migrations"
}

// Indexes for the migration model.
func (Migration) Indexes() []Index {
	return []Index{
		{
			Name:        "idx_migrations_file_name",
			ColumnNames: []string{"file_name"},
		},
	}
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
func (m *migrater) Migrate(skipOnFresh bool) {
	migrationFileNames := make([]string, 0, len(m.migrations))
	for name := range m.migrations {
		migrationFileNames = append(migrationFileNames, name)
	}
	sort.Strings(migrationFileNames)

	// Check if we are starting with a completely fresh database.
	fresh := !m.db.TableExists(Migration{})

	// Create the table. Even if the table is already in the database, this
	// operation will ensure that go-rm knows where to store the migration model.
	if err := m.db.CreateTable(m.db.AddTable(Migration{})); err != nil {
		panic(err)
	}

	// If the migrations table does not exist, assume the database is fresh
	// which means that all migrations have been executed.
	if fresh && skipOnFresh {
		slog.Info("fresh database, tables will be created on-demand")
		// Mark all migrations as executed.
		var migrations []Migration
		for _, name := range migrationFileNames {
			migrations = append(migrations, Migration{FileName: name})
		}
		if err := ReplaceAll(m.db, migrations...); err != nil {
			panic(err)
		}
		slog.Info("migrations executed")
		return
	}

	// Get the migrations that were executed already.
	var executedFiles []string
	if _, err := m.db.Select(&executedFiles, "SELECT file_name FROM migrations"); err != nil {
		panic(err)
	}
	migrationsToExecute := []string{}
	for _, name := range migrationFileNames {
		if slices.Contains(executedFiles, name) {
			slog.Info("migration already executed, skipping", "name", name)
			continue
		}
		migrationsToExecute = append(migrationsToExecute, name)
	}

	tx, err := m.db.Begin()
	if err != nil {
		slog.Error("failed to begin transaction", "error", err)
		panic(tx.Rollback())
	}
	for _, fileName := range migrationsToExecute {
		migration := m.migrations[fileName]
		slog.Info("executing migration", "fileName", fileName)
		if _, err := tx.Exec(migration); err != nil {
			slog.Error("failed to execute migration", "fileName", fileName, "error", err)
			panic(tx.Rollback())
		}
		migrationObj := Migration{FileName: fileName}
		if err := tx.Insert(&migrationObj); err != nil {
			slog.Error("failed to insert migration", "fileName", fileName, "error", err)
			panic(tx.Rollback())
		}
	}
	if err = tx.Commit(); err != nil {
		panic(err)
	}
	slog.Info("migrations executed")
}
