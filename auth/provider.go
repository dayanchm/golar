package auth

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"time"

	"github.com/dayanchm/golar/database/orm"
)

// ORMProvider loads users of model T through the ORM. *T or T must implement
// Authenticatable. It also implements PasswordUpdater and EmailVerifier's
// marker, so it works with password resets and email verification.
type ORMProvider[T any] struct {
	DB orm.DB
	// Column defaults: "email", "password" and "email_verified_at".
	IdentifierColumn, PasswordColumn, VerifiedColumn string
}

func (p *ORMProvider[T]) column(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func authenticatable[T any](model *T) (Authenticatable, error) {
	if user, ok := any(model).(Authenticatable); ok {
		return user, nil
	}
	if user, ok := any(*model).(Authenticatable); ok {
		return user, nil
	}
	return nil, fmt.Errorf("auth: %T does not implement Authenticatable", model)
}

func (p *ORMProvider[T]) found(model *T, err error) (Authenticatable, error) {
	if errors.Is(err, orm.ErrNotFound) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return authenticatable(model)
}

// key converts a string ID back to the model's primary key type.
func (p *ORMProvider[T]) key(id string) (any, error) {
	info, err := orm.Describe(new(T))
	if err != nil {
		return nil, err
	}
	switch info.KeyType.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.ParseInt(id, 10, 64)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.ParseUint(id, 10, 64)
	}
	return id, nil
}

func (p *ORMProvider[T]) RetrieveByID(ctx context.Context, id string) (Authenticatable, error) {
	key, err := p.key(id)
	if err != nil {
		return nil, ErrUserNotFound // a malformed ID cannot match any user
	}
	return p.found(orm.Find[T](ctx, p.DB, key))
}

func (p *ORMProvider[T]) RetrieveByIdentifier(ctx context.Context, identifier string) (Authenticatable, error) {
	return p.found(orm.Query[T](p.DB).Where(p.column(p.IdentifierColumn, "email"), "=", identifier).First(ctx))
}

func (p *ORMProvider[T]) update(ctx context.Context, user Authenticatable, values map[string]any) error {
	info, err := orm.Describe(new(T))
	if err != nil {
		return err
	}
	key, err := p.key(user.AuthID())
	if err != nil {
		return err
	}
	_, err = orm.Query[T](p.DB).Where(info.PrimaryKey, "=", key).Update(ctx, values)
	return err
}

func (p *ORMProvider[T]) UpdatePassword(ctx context.Context, user Authenticatable, hash string) error {
	return p.update(ctx, user, map[string]any{p.column(p.PasswordColumn, "password"): hash})
}

func (p *ORMProvider[T]) MarkEmailVerified(ctx context.Context, user Authenticatable) error {
	return p.update(ctx, user, map[string]any{p.column(p.VerifiedColumn, "email_verified_at"): time.Now().UTC()})
}
