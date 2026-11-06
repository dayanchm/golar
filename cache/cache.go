// Package cache stores short-lived values behind one interface: Memory keeps
// them in the process, File in a directory and SQLStore in a database table.
//
//	store := cache.NewMemory()
//	user, err := cache.Remember(ctx, store, "user:42", 10*time.Minute, func(ctx context.Context) (User, error) {
//		return orm.Find[User](ctx, db, 42)
//	})
//	hits, err := store.Increment(ctx, "hits", 1)
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"strconv"
	"time"
)

// Store keeps byte values by key. A zero ttl keeps a value until it is deleted;
// a negative ttl stores it already expired. Expired values are never returned.
// Implementations are safe for concurrent use.
type Store interface {
	// Get returns the value for key; ok is false when it is missing or expired.
	Get(ctx context.Context, key string) (value []byte, ok bool, err error)
	// Put stores value, replacing any value and expiry under key.
	Put(ctx context.Context, key string, value []byte, ttl time.Duration) error
	// Add stores value only if key has no live value and reports whether it
	// did. Only one concurrent caller succeeds.
	Add(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error)
	// Increment adds delta to the integer under key and returns the result.
	// Missing keys start at 0 with no expiry; existing keys keep theirs. Values
	// that are not integers return ErrNotInteger.
	Increment(ctx context.Context, key string, delta int64) (int64, error)
	// Delete removes key. Deleting a missing key is not an error.
	Delete(ctx context.Context, key string) error
	// Flush removes every value.
	Flush(ctx context.Context) error
}

// Pruner is implemented by stores that can delete expired values on demand.
type Pruner interface {
	// Prune deletes expired values and returns how many were removed.
	Prune(ctx context.Context) (int64, error)
}

var (
	// ErrNotInteger is returned by Increment when the value is not an integer
	// or the result does not fit in an int64.
	ErrNotInteger = errors.New("cache: value is not an integer")
	// ErrDecode wraps errors from decoding a stored value into the type asked
	// for, such as a value written by an older version of the application.
	ErrDecode = errors.New("cache: cannot decode value")
)

// Get decodes the JSON value under key into a T.
func Get[T any](ctx context.Context, store Store, key string) (T, bool, error) {
	var value T
	data, ok, err := store.Get(ctx, key)
	if err != nil || !ok {
		return value, false, err
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return value, false, fmt.Errorf("%w %q: %w", ErrDecode, key, err)
	}
	return value, true, nil
}

// Put stores value as JSON under key.
func Put[T any](ctx context.Context, store Store, key string, value T, ttl time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return store.Put(ctx, key, data, ttl)
}

// Remember returns the value under key, or calls compute and stores its result.
// Errors from compute are returned and not cached. A value that no longer
// decodes into T is computed again. Concurrent callers that miss each call
// compute.
func Remember[T any](ctx context.Context, store Store, key string, ttl time.Duration, compute func(context.Context) (T, error)) (T, error) {
	value, ok, err := Get[T](ctx, store, key)
	if err != nil && !errors.Is(err, ErrDecode) {
		return value, err
	}
	if ok {
		return value, nil
	}
	value, err = compute(ctx)
	if err != nil {
		return value, err
	}
	return value, Put(ctx, store, key, value, ttl)
}

// Decrement subtracts delta from the integer under key; see Store.Increment.
func Decrement(ctx context.Context, store Store, key string, delta int64) (int64, error) {
	if delta == math.MinInt64 {
		return 0, ErrNotInteger
	}
	return store.Increment(ctx, key, -delta)
}

// PruneEvery prunes store every interval until ctx is canceled, logging errors.
// It returns at once for stores that are not Pruners. Run it in a goroutine,
// for example from an app.OnStart hook.
func PruneEvery(ctx context.Context, store Store, interval time.Duration) {
	pruner, ok := store.(Pruner)
	if !ok {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := pruner.Prune(ctx); err != nil && ctx.Err() == nil {
				log.Printf("cache: prune: %v", err)
			}
		}
	}
}

// expiry returns when a value stored now expires; zero means never.
func expiry(ttl time.Duration) time.Time {
	if ttl == 0 {
		return time.Time{}
	}
	return time.Now().Add(ttl)
}

// addInt adds delta to an encoded integer.
func addInt(value []byte, delta int64) ([]byte, int64, error) {
	current, err := strconv.ParseInt(string(value), 10, 64)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %q", ErrNotInteger, value)
	}
	if delta > 0 && current > math.MaxInt64-delta || delta < 0 && current < math.MinInt64-delta {
		return nil, 0, fmt.Errorf("%w: %d + %d overflows", ErrNotInteger, current, delta)
	}
	next := current + delta
	return strconv.AppendInt(nil, next, 10), next, nil
}
