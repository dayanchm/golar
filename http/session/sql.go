package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"time"

	"github.com/dayanchm/golar/database/orm"
	"github.com/dayanchm/golar/database/schema"
)

// Table is the default table name for SQLStore.
const Table = "sessions"

// CreateTables creates the sessions table if it is missing. Call it from a
// migration; pass a name to use a table other than Table.
func CreateTables(s *schema.Schema, name ...string) error {
	table := Table
	if len(name) > 0 && name[0] != "" {
		table = name[0]
	}
	if ok, err := s.HasTable(table); err != nil || ok {
		return err
	}
	return s.Create(table, func(t *schema.Blueprint) {
		t.String("id", 64).Primary()
		t.Text("payload")
		t.Timestamp("expires_at")
		t.Index("expires_at")
	})
}

// SQLStore keeps sessions in a database table, so they survive restarts and
// are shared between instances. Rows are keyed by the SHA-256 of the session
// ID; a leaked table does not reveal usable cookies. Table defaults to Table.
// MySQL DSNs need parseTime=true.
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

func hashID(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])
}

// now is truncated to seconds, the precision of MySQL TIMESTAMP columns.
func now() time.Time { return time.Now().UTC().Truncate(time.Second) }

func (s *SQLStore) Load(ctx context.Context, id string) (map[string]string, bool, error) {
	rows, err := s.DB.Table(s.table()).Select("payload", "expires_at").
		Where("id", "=", hashID(id)).Limit(1).QueryContext(ctx)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, false, rows.Err()
	}
	var payload string
	var expires time.Time
	if err := rows.Scan(&payload, &expires); err != nil {
		return nil, false, err
	}
	if err := rows.Close(); err != nil {
		return nil, false, err
	}
	if !now().Before(expires) {
		return nil, false, nil
	}
	data := map[string]string{}
	if err := json.Unmarshal([]byte(payload), &data); err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func (s *SQLStore) Save(ctx context.Context, id string, data map[string]string, expires time.Time) error {
	if data == nil {
		data = map[string]string{}
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	key := hashID(id)
	values := map[string]any{"payload": string(payload), "expires_at": expires.UTC().Truncate(time.Second)}
	updated, err := s.update(ctx, key, values)
	if err != nil || updated {
		return err
	}
	values["id"] = key
	if _, err := s.DB.Table(s.table()).InsertContext(ctx, values); err != nil {
		// Another request inserted the row first, or MySQL reported an
		// unchanged row as unaffected: update it if it exists.
		delete(values, "id")
		count, countErr := s.DB.Table(s.table()).Where("id", "=", key).Count(ctx)
		if countErr != nil || count == 0 {
			return err
		}
		_, err = s.update(ctx, key, values)
		return err
	}
	return nil
}

func (s *SQLStore) update(ctx context.Context, key string, values map[string]any) (bool, error) {
	result, err := s.DB.Table(s.table()).Where("id", "=", key).UpdateContext(ctx, values)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

func (s *SQLStore) Delete(ctx context.Context, id string) error {
	_, err := s.DB.Table(s.table()).Where("id", "=", hashID(id)).DeleteContext(ctx)
	return err
}

// Prune deletes expired sessions and returns how many were removed.
func (s *SQLStore) Prune(ctx context.Context) (int64, error) {
	result, err := s.DB.Table(s.table()).Where("expires_at", "<=", now()).DeleteContext(ctx)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// PruneEvery calls Prune every interval until ctx is canceled, logging
// errors. Run it in a goroutine, for example from an app.OnStart hook.
func (s *SQLStore) PruneEvery(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.Prune(ctx); err != nil && ctx.Err() == nil {
				log.Printf("session: prune: %v", err)
			}
		}
	}
}
