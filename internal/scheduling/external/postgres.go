// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

// Package external provides access to external data sources for scheduling.
package external

import (
	"context"
	"fmt"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/db"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PostgresReader provides read access to a postgres database.
// It reads the database connection info from a Datasource CRD.
type PostgresReader struct {
	// Kubernetes client to read the Datasource CRD and secrets.
	Client client.Client
	// Reference to the database secret containing connection info.
	DatabaseSecretRef corev1.SecretReference
	// Cached database connection (lazily initialized).
	db *db.DB
}

// NewPostgresReader creates a new PostgresReader from a Datasource CRD name.
// It looks up the Datasource CRD to get the database secret reference.
func NewPostgresReader(ctx context.Context, c client.Client, datasourceName string) (*PostgresReader, error) {
	// Look up the Datasource CRD to get the database secret reference
	datasource := &v1alpha1.Datasource{}
	if err := c.Get(ctx, client.ObjectKey{Name: datasourceName}, datasource); err != nil {
		return nil, fmt.Errorf("failed to get datasource %s: %w", datasourceName, err)
	}

	return &PostgresReader{
		Client:            c,
		DatabaseSecretRef: datasource.Spec.DatabaseSecretRef,
	}, nil
}

// NewPostgresReaderFromSecretRef creates a new PostgresReader with a direct secret reference.
func NewPostgresReaderFromSecretRef(c client.Client, secretRef corev1.SecretReference) *PostgresReader {
	return &PostgresReader{
		Client:            c,
		DatabaseSecretRef: secretRef,
	}
}

// DB returns the database connection, initializing it if necessary.
func (r *PostgresReader) DB(ctx context.Context) (*db.DB, error) {
	if r.db != nil {
		return r.db, nil
	}

	// Connect to the database using the secret reference
	database, err := db.Connector{Client: r.Client}.FromSecretRef(ctx, r.DatabaseSecretRef)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	r.db = database
	return r.db, nil
}

// Select executes a SELECT query and returns the results.
func (r *PostgresReader) Select(ctx context.Context, dest interface{}, query string, args ...interface{}) error {
	database, err := r.DB(ctx)
	if err != nil {
		return err
	}

	_, err = database.Select(dest, query, args...)
	return err
}
