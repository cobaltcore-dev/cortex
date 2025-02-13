// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package containers

import (
	"database/sql"
	"fmt"
	"testing"

	_ "github.com/lib/pq"
)

func TestPostgresContainer_Init(t *testing.T) {
	container := PostgresContainer{}
	container.Init(t)
	defer container.Close()

	psqlInfo := fmt.Sprintf(
		"host=localhost port=%s user=postgres password=secret dbname=postgres sslmode=disable",
		container.GetPort(),
	)
	db, err := sql.Open("postgres", psqlInfo)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestPostgresContainer_Close(t *testing.T) {
	container := PostgresContainer{}
	container.Init(t)

	psqlInfo := fmt.Sprintf(
		"host=localhost port=%s user=postgres password=secret dbname=postgres sslmode=disable",
		container.GetPort(),
	)
	db, err := sql.Open("postgres", psqlInfo)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	defer db.Close()

	container.Close()

	if err := db.Ping(); err == nil {
		t.Fatal("expected error, got nil")
	}
}
