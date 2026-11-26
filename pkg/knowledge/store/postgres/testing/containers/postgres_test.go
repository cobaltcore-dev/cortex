// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package containers

import (
	"database/sql"
	"fmt"
	"os"
	"testing"

	_ "github.com/lib/pq"
)

func TestPostgresContainer_Init(t *testing.T) {
	if os.Getenv("POSTGRES_CONTAINER") != "1" {
		t.Skip("skipping test; set POSTGRES_CONTAINER=1 to run")
	}

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

	if err := db.PingContext(t.Context()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestPostgresContainer_Close(t *testing.T) {
	if os.Getenv("POSTGRES_CONTAINER") != "1" {
		t.Skip("skipping test; set POSTGRES_CONTAINER=1 to run")
	}

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

	if err := db.PingContext(t.Context()); err == nil {
		t.Fatal("expected error, got nil")
	}
}
