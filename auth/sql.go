package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/dayanchm/golar/database/orm"
	"github.com/dayanchm/golar/database/schema"
)

// Default table names for the SQL stores.
const (
	TokensTable = "auth_tokens"
	ResetsTable = "password_reset_tokens"
)

// CreateTables creates the token and password reset tables if they are
// missing. Call it from a migration.
func CreateTables(s *schema.Schema) error {
	if ok, err := s.HasTable(TokensTable); err != nil {
		return err
	} else if !ok {
		err := s.Create(TokensTable, func(t *schema.Blueprint) {
			t.String("id", 64).Primary()
			t.String("user_id", 191)
			t.String("name", 255)
			t.Text("abilities")
			t.Timestamp("expires_at").Nullable()
			t.Timestamp("created_at")
			t.Index("user_id")
		})
		if err != nil {
			return err
		}
	}
	if ok, err := s.HasTable(ResetsTable); err != nil || ok {
		return err
	}
	return s.Create(ResetsTable, func(t *schema.Blueprint) {
		t.String("user_id", 191).Primary()
		t.String("token", 64)
		t.Timestamp("created_at")
	})
}

// SQLTokenStore stores API tokens with the query builder. Table defaults to
// TokensTable. MySQL DSNs need parseTime=true.
type SQLTokenStore struct {
	DB    orm.DB
	Table string
}

func (s *SQLTokenStore) table() string { return or(s.Table, TokensTable) }

func (s *SQLTokenStore) Create(ctx context.Context, t Token) error {
	abilities, err := json.Marshal(t.Abilities)
	if err != nil {
		return err
	}
	var expires any
	if t.ExpiresAt != nil {
		expires = t.ExpiresAt.UTC()
	}
	_, err = s.DB.Table(s.table()).InsertContext(ctx, map[string]any{
		"id": t.ID, "user_id": t.UserID, "name": t.Name, "abilities": string(abilities),
		"expires_at": expires, "created_at": t.CreatedAt.UTC(),
	})
	return err
}

func (s *SQLTokenStore) Find(ctx context.Context, id string) (Token, error) {
	rows, err := s.DB.Table(s.table()).Select("id", "user_id", "name", "abilities", "expires_at", "created_at").
		Where("id", "=", id).Limit(1).QueryContext(ctx)
	if err != nil {
		return Token{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return Token{}, err
		}
		return Token{}, ErrInvalidToken
	}
	var t Token
	var abilities string
	var expires sql.NullTime
	if err := rows.Scan(&t.ID, &t.UserID, &t.Name, &abilities, &expires, &t.CreatedAt); err != nil {
		return Token{}, err
	}
	if expires.Valid {
		t.ExpiresAt = &expires.Time
	}
	if err := json.Unmarshal([]byte(abilities), &t.Abilities); err != nil {
		return Token{}, err
	}
	return t, rows.Close()
}

func (s *SQLTokenStore) Delete(ctx context.Context, id string) error {
	_, err := s.DB.Table(s.table()).Where("id", "=", id).DeleteContext(ctx)
	return err
}

func (s *SQLTokenStore) DeleteUser(ctx context.Context, userID string) error {
	_, err := s.DB.Table(s.table()).Where("user_id", "=", userID).DeleteContext(ctx)
	return err
}

// SQLResetStore stores password reset token hashes. Table defaults to ResetsTable.
type SQLResetStore struct {
	DB    orm.DB
	Table string
}

func (s *SQLResetStore) table() string { return or(s.Table, ResetsTable) }

func (s *SQLResetStore) Put(ctx context.Context, userID, hash string, created time.Time) error {
	if err := s.Delete(ctx, userID); err != nil {
		return err
	}
	_, err := s.DB.Table(s.table()).InsertContext(ctx, map[string]any{"user_id": userID, "token": hash, "created_at": created.UTC()})
	return err
}

func (s *SQLResetStore) Get(ctx context.Context, userID string) (string, time.Time, error) {
	rows, err := s.DB.Table(s.table()).Select("token", "created_at").Where("user_id", "=", userID).Limit(1).QueryContext(ctx)
	if err != nil {
		return "", time.Time{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return "", time.Time{}, err
		}
		return "", time.Time{}, ErrInvalidToken
	}
	var hash string
	var created time.Time
	if err := rows.Scan(&hash, &created); err != nil {
		return "", time.Time{}, err
	}
	return hash, created, rows.Close()
}

func (s *SQLResetStore) Delete(ctx context.Context, userID string) error {
	_, err := s.DB.Table(s.table()).Where("user_id", "=", userID).DeleteContext(ctx)
	return err
}
