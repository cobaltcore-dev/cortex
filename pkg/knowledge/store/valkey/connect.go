// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package valkey

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/pkg/knowledge/store"
	"github.com/valkey-io/valkey-go"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var log = logf.Log.WithName("valkey")

func init() {
	store.Register(v1alpha1.StorageTypeValkey, func(ctx context.Context, c client.Client, spec v1alpha1.StorageSpec) (store.Store, error) {
		if spec.Valkey == nil {
			return nil, errors.New("valkey storage spec is missing")
		}
		return FromSecretRef(ctx, c, spec.Valkey.SecretRef)
	})
}

// Connection details read from a secret.
type options struct {
	addr     string
	username string
	password string
	db       int
}

var (
	// Stores by connection details, so that connections and caches are
	// shared by all users of the same valkey server.
	stores   = map[options]*Store{}
	storesMu sync.Mutex
)

// FromSecretRef returns the store of the valkey server described by the
// given secret, connecting to it if necessary.
func FromSecretRef(ctx context.Context, c client.Client, ref corev1.SecretReference) (*Store, error) {
	secret := &corev1.Secret{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, secret); err != nil {
		return nil, err
	}
	get := func(key string) string { return strings.TrimSpace(string(secret.Data[key])) }
	opts := options{username: get("username"), password: get("password")}
	host, port := get("host"), get("port")
	if host == "" || port == "" {
		return nil, errors.New("missing host or port in valkey secret data")
	}
	opts.addr = net.JoinHostPort(host, port)
	if db := get("db"); db != "" {
		n, err := strconv.Atoi(db)
		if err != nil {
			return nil, fmt.Errorf("invalid db in valkey secret data: %w", err)
		}
		opts.db = n
	}

	storesMu.Lock()
	defer storesMu.Unlock()
	if s, ok := stores[opts]; ok {
		return s, nil
	}
	log.Info("connecting to valkey", "addr", opts.addr, "db", opts.db)
	Monitor.connectionAttempts.WithLabelValues(opts.addr).Inc()
	clientOpts := valkey.ClientOption{
		InitAddress: []string{opts.addr},
		Username:    opts.username,
		Password:    opts.password,
		SelectDB:    opts.db,
		// Decoded scopes are cached by the store itself.
		DisableCache: true,
	}
	// Retry connecting for up to 10 seconds, e.g. while valkey starts.
	const maxRetries = 10
	var vk valkey.Client
	var err error
	for i := range maxRetries {
		if vk, err = valkey.NewClient(clientOpts); err == nil {
			break
		}
		if i == maxRetries-1 {
			return nil, fmt.Errorf("giving up connecting to valkey: %w", err)
		}
		log.Error(err, "failed to connect to valkey, retrying...")
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	log.Info("valkey is ready", "addr", opts.addr)
	s := New(vk)
	stores[opts] = s
	return s, nil
}
