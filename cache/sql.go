package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"

	"github.com/dayanchm/golar/database/orm"
	"github.com/dayanchm/golar/database/schema"
)

// Table is the default table name for SQLStore.
const Table = "cache"

// Columns defines the cache table. A migration can create it with
// s.Create(cache.Table, cache.Columns), which also works with migrate --pretend.
func Columns(t *schema.Blueprint) {
	t.String("id", 64).Primary()
	t.Binary("payload")
	t.BigInteger("expires_at")
	t.Index("expires_at")
}

// CreateTables creates the cache table if it is missing; pass a name to use a
// table other than Table. It checks the database, so it cannot run under
// migrate --pretend.
func CreateTables(s *schema.Schema, name ...string) error {
	table := Table
	if len(name) > 0 && name[0] != "" {
		table = name[0]
	}
	if ok, err := s.HasTable(table); err != nil || ok {
		return err
	}
	return s.Create(table, Columns)
}

// SQLStore keeps values in a database table, so they survive restarts and are
// shared between instances. Add and Increment are atomic across instances.
// Keys are stored as SHA-256 hashes, which keeps them a fixed length and
// case-sensitive on every database. Table defaults to Table. Expired rows stay
// until they are replaced or pruned. Values are limited by the column type:
// 64 KiB on MySQL.
type SQLStore struct {
	DB    orm.DB
	Table string
}

func (s *SQLStore) table() string {
	if s.Table != "" {
		return s.Table
	}
	return Table
}

func rowID(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// expiresAt encodes an expiry in Unix milliseconds, with 0 for never.
func expiresAt(expires time.Time) int64 {
	if expires.IsZero() {
		return 0
	}
	return expires.UnixMilli()
}

// read returns the row's payload and expiry. live is false for missing and
// expired rows; found separates them.
func (s *SQLStore) read(ctx context.Context, id string) (payload []byte, expires int64, found, live bool, err error) {
	rows, err := s.DB.Table(s.table()).Select("payload", "expires_at").Where("id", "=", id).Limit(1).QueryContext(ctx)
	if err != nil {
		return nil, 0, false, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, 0, false, false, rows.Err()
	}
	if err := rows.Scan(&payload, &expires); err != nil {
		return nil, 0, false, false, err
	}
	if err := rows.Close(); err != nil {
		return nil, 0, false, false, err
	}
	return payload, expires, true, expires == 0 || time.Now().UnixMilli() < expires, nil
}

func (s *SQLStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	payload, _, _, live, err := s.read(ctx, rowID(key))
	if err != nil || !live {
		return nil, false, err
	}
	return payload, true, nil
}

func (s *SQLStore) insert(ctx context.Context, id string, value []byte, expires int64) error {
	if value == nil {
		value = []byte{}
	}
	_, err := s.DB.Table(s.table()).InsertContext(ctx, map[string]any{"id": id, "payload": value, "expires_at": expires})
	return err
}

func (s *SQLStore) update(ctx context.Context, id string, value []byte, expires int64) (bool, error) {
	if value == nil {
		value = []byte{}
	}
	result, err := s.DB.Table(s.table()).Where("id", "=", id).UpdateContext(ctx, map[string]any{"payload": value, "expires_at": expires})
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

func (s *SQLStore) Put(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	id, expires := rowID(key), expiresAt(expiry(ttl))
	updated, err := s.update(ctx, id, value, expires)
	if err != nil || updated {
		return err
	}
	if err := s.insert(ctx, id, value, expires); err != nil {
		// Another caller inserted the row first, or MySQL reported an
		// unchanged row as unaffected: update it if it exists.
		if _, _, found, _, readErr := s.read(ctx, id); readErr != nil || !found {
			return err
		}
		_, err = s.update(ctx, id, value, expires)
		return err
	}
	return nil
}

// claim inserts a row for id, first removing an expired row seen as expires.
// It reports false when another caller's row is in the way.
func (s *SQLStore) claim(ctx context.Context, id string, value []byte, ttl time.Duration, found bool, seen int64) (bool, error) {
	if found {
		if _, err := s.DB.Table(s.table()).Where("id", "=", id).Where("expires_at", "=", seen).DeleteContext(ctx); err != nil {
			return false, err
		}
	}
	err := s.insert(ctx, id, value, expiresAt(expiry(ttl)))
	if err == nil {
		return true, nil
	}
	_, _, exists, _, readErr := s.read(ctx, id)
	if readErr != nil || !exists {
		return false, err
	}
	return false, nil
}

func (s *SQLStore) Add(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	id := rowID(key)
	for {
		_, seen, found, live, err := s.read(ctx, id)
		if err != nil || live {
			return false, err
		}
		added, err := s.claim(ctx, id, value, ttl, found, seen)
		if err != nil || added {
			return added, err
		}
		// Lost to a row that may already have expired, so look again.
		if err := ctx.Err(); err != nil {
			return false, err
		}
	}
}

// Increment compares and swaps the payload, retrying when another caller
// changed it first.
func (s *SQLStore) Increment(ctx context.Context, key string, delta int64) (int64, error) {
	id := rowID(key)
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		payload, seen, found, live, err := s.read(ctx, id)
		if err != nil {
			return 0, err
		}
		if !live {
			added, err := s.claim(ctx, id, strconv.AppendInt(nil, delta, 10), 0, found, seen)
			if err != nil {
				return 0, err
			}
			if added {
				return delta, nil
			}
			continue
		}
		value, next, err := addInt(payload, delta)
		if err != nil || delta == 0 {
			return next, err
		}
		result, err := s.DB.Table(s.table()).Where("id", "=", id).Where("payload", "=", payload).
			UpdateContext(ctx, map[string]any{"payload": value})
		if err != nil {
			return 0, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		if n > 0 {
			return next, nil
		}
	}
}

func (s *SQLStore) Delete(ctx context.Context, key string) error {
	_, err := s.DB.Table(s.table()).Where("id", "=", rowID(key)).DeleteContext(ctx)
	return err
}

func (s *SQLStore) Flush(ctx context.Context) error {
	_, err := s.DB.Table(s.table()).AllRows().DeleteContext(ctx)
	return err
}

// Prune deletes expired rows.
func (s *SQLStore) Prune(ctx context.Context) (int64, error) {
	result, err := s.DB.Table(s.table()).Where("expires_at", ">", 0).
		Where("expires_at", "<=", time.Now().UnixMilli()).DeleteContext(ctx)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
