// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

// Package valkey stores datasource objects in valkey (or redis).
//
// Each scope is stored as follows, where the hash tag {<scope>} keeps all
// keys of a scope in the same cluster slot:
//
//	cortex/{<scope>}/current    string: "<generation>/<number of objects>"
//	cortex/{<scope>}/gen/<gen>  hash:   <object key> -> <object json>
//
// Writers fill a new generation and then swap the current pointer
// atomically, so readers never see a partially written scope. Previous
// generations expire after a grace period that allows in-flight reads to
// complete.
package valkey

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cobaltcore-dev/cortex/pkg/knowledge/store"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/valkey-io/valkey-go"
)

const (
	// Number of hash fields written per command and requested per scan.
	batchSize = 1000
	// Number of commands sent per pipeline.
	pipelineSize = 64
	// How long a replaced generation stays readable.
	graceTTL = 5 * time.Minute
	// How long a generation that is being written may live without being
	// swapped in, so that crashed writers don't leak memory.
	pendingTTL = time.Hour
	// How often a read is retried if the scope was replaced during the read.
	maxReadAttempts = 3
)

// Atomically point the scope to the new generation, make the new generation
// persistent and let the previous one expire.
var swapScript = valkey.NewLuaScript(`
local old = redis.call('GET', KEYS[1])
redis.call('SET', KEYS[1], ARGV[1])
redis.call('PERSIST', KEYS[2])
local oldgen = old and string.match(old, '^([^/]+)/')
if oldgen and ARGV[2] .. oldgen ~= KEYS[2] then
  redis.call('EXPIRE', ARGV[2] .. oldgen, ARGV[3])
end
return 'OK'
`)

// Store implements store.Store on top of a valkey client.
type Store struct {
	client valkey.Client
	mu     sync.Mutex
	// Decoded objects by scope key, valid as long as the current pointer of
	// the scope is unchanged.
	cache map[string]cached
}

type cached struct {
	pointer string
	list    any
}

// New creates a store on top of the given valkey client.
func New(client valkey.Client) *Store {
	return &Store{client: client, cache: map[string]cached{}}
}

func prefix(scope store.Scope) string     { return "cortex/{" + scope.Key() + "}" }
func currentKey(scope store.Scope) string { return prefix(scope) + "/current" }
func genPrefix(scope store.Scope) string  { return prefix(scope) + "/gen/" }

func parsePointer(pointer string) (gen string, count int, err error) {
	gen, n, ok := strings.Cut(pointer, "/")
	if !ok {
		return "", 0, fmt.Errorf("malformed scope pointer %q", pointer)
	}
	count, err = strconv.Atoi(n)
	if err != nil {
		return "", 0, fmt.Errorf("malformed scope pointer %q: %w", pointer, err)
	}
	return gen, count, nil
}

// ListAll objects in the scope into out, a pointer to a slice of the model.
func (s *Store) ListAll(ctx context.Context, scope store.Scope, out any) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	// Fail fast on a wrong output type before reading any data.
	if err := store.CheckList(scope, out); err != nil {
		return err
	}
	timer := prometheus.NewTimer(Monitor.duration.WithLabelValues("list", scope.Key()))
	defer timer.ObserveDuration()
	for range maxReadAttempts {
		pointer, err := s.client.Do(ctx, s.client.B().Get().Key(currentKey(scope)).Build()).ToString()
		if valkey.IsValkeyNil(err) {
			return store.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("failed to read scope %s: %w", scope.Key(), err)
		}
		s.mu.Lock()
		c, ok := s.cache[scope.Key()]
		s.mu.Unlock()
		if ok && c.pointer == pointer {
			Monitor.cache.WithLabelValues(scope.Key(), "hit").Inc()
			return store.SetList(scope, out, c.list)
		}
		Monitor.cache.WithLabelValues(scope.Key(), "miss").Inc()
		gen, count, err := parsePointer(pointer)
		if err != nil {
			return err
		}
		data, err := s.scan(ctx, genPrefix(scope)+gen, count)
		if err != nil {
			return fmt.Errorf("failed to read scope %s: %w", scope.Key(), err)
		}
		if len(data) != count {
			// The generation expired while reading, retry with the new one.
			continue
		}
		list, err := store.Decode(scope, data)
		if err != nil {
			return err
		}
		s.mu.Lock()
		s.cache[scope.Key()] = cached{pointer: pointer, list: list}
		s.mu.Unlock()
		Monitor.objects.WithLabelValues("read", scope.Key()).Add(float64(count))
		return store.SetList(scope, out, list)
	}
	return fmt.Errorf("scope %s was replaced during %d consecutive reads", scope.Key(), maxReadAttempts)
}

// Read all values of the given hash.
func (s *Store) scan(ctx context.Context, key string, sizeHint int) ([][]byte, error) {
	if sizeHint == 0 {
		return nil, nil
	}
	// Scans may return fields more than once.
	byField := make(map[string]string, sizeHint)
	var cursor uint64
	for {
		cmd := s.client.B().Hscan().Key(key).Cursor(cursor).Count(batchSize).Build()
		entry, err := s.client.Do(ctx, cmd).AsScanEntry()
		if err != nil {
			return nil, err
		}
		for i := 0; i+1 < len(entry.Elements); i += 2 {
			byField[entry.Elements[i]] = entry.Elements[i+1]
		}
		if entry.Cursor == 0 {
			break
		}
		cursor = entry.Cursor
	}
	data := make([][]byte, 0, len(byField))
	for _, v := range byField {
		data = append(data, []byte(v))
	}
	return data, nil
}

// ReplaceAll objects in the scope with objs, a slice of the model.
func (s *Store) ReplaceAll(ctx context.Context, scope store.Scope, objs any) error {
	entries, err := store.Encode(scope, objs)
	if err != nil {
		return err
	}
	timer := prometheus.NewTimer(Monitor.duration.WithLabelValues("replace", scope.Key()))
	defer timer.ObserveDuration()
	genBytes := make([]byte, 8)
	if _, err := rand.Read(genBytes); err != nil {
		return err
	}
	gen := hex.EncodeToString(genBytes)
	genKey := genPrefix(scope) + gen
	if err := s.write(ctx, genKey, entries); err != nil {
		// Best effort cleanup, the pending ttl removes leftovers anyway.
		s.client.Do(context.WithoutCancel(ctx), s.client.B().Del().Key(genKey).Build())
		return fmt.Errorf("failed to write scope %s: %w", scope.Key(), err)
	}
	pointer := gen + "/" + strconv.Itoa(len(entries))
	keys := []string{currentKey(scope), genKey}
	args := []string{pointer, genPrefix(scope), strconv.Itoa(int(graceTTL.Seconds()))}
	if err := swapScript.Exec(ctx, s.client, keys, args).Error(); err != nil {
		s.client.Do(context.WithoutCancel(ctx), s.client.B().Del().Key(genKey).Build())
		return fmt.Errorf("failed to swap scope %s: %w", scope.Key(), err)
	}
	Monitor.objects.WithLabelValues("written", scope.Key()).Add(float64(len(entries)))
	return nil
}

// Write the entries into the given hash in pipelined batches.
func (s *Store) write(ctx context.Context, key string, entries []store.Entry) error {
	var cmds valkey.Commands
	flush := func() error {
		for _, res := range s.client.DoMulti(ctx, cmds...) {
			if err := res.Error(); err != nil {
				return err
			}
		}
		cmds = cmds[:0]
		return nil
	}
	for start := 0; start < len(entries); start += batchSize {
		hset := s.client.B().Hset().Key(key).FieldValue()
		for _, e := range entries[start:min(start+batchSize, len(entries))] {
			hset = hset.FieldValue(e.Key, string(e.Data))
		}
		cmds = append(cmds, hset.Build())
		if start == 0 {
			cmds = append(cmds, s.client.B().Expire().Key(key).Seconds(int64(pendingTTL.Seconds())).Build())
		}
		if len(cmds) >= pipelineSize {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if len(cmds) > 0 {
		return flush()
	}
	return nil
}

// Exists reports whether the scope was written.
func (s *Store) Exists(ctx context.Context, scope store.Scope) (bool, error) {
	if err := scope.Validate(); err != nil {
		return false, err
	}
	n, err := s.client.Do(ctx, s.client.B().Exists().Key(currentKey(scope)).Build()).AsInt64()
	if err != nil {
		return false, fmt.Errorf("failed to check scope %s: %w", scope.Key(), err)
	}
	return n > 0, nil
}
