package events_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dayanchm/golar/database"
	"github.com/dayanchm/golar/database/schema"
	"github.com/dayanchm/golar/events"
	"github.com/dayanchm/golar/queue"
)

type UserRegistered struct {
	UserID int64  `json:"user_id"`
	Email  string `json:"email"`
}

type OrderPaid struct{ OrderID int64 }

func TestListenersRunInOrder(t *testing.T) {
	ctx := context.Background()
	bus := events.New()
	var order []string
	events.Listen(bus, func(_ context.Context, e UserRegistered) error {
		order = append(order, "audit:"+e.Email)
		return nil
	})
	events.Listen(bus, func(context.Context, UserRegistered) error {
		order = append(order, "metrics")
		return nil
	})
	events.Listen(bus, func(context.Context, OrderPaid) error {
		order = append(order, "wrong type")
		return nil
	})
	if err := events.Dispatch(ctx, bus, UserRegistered{UserID: 1, Email: "ada@example.com"}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(order, ",") != "audit:ada@example.com,metrics" {
		t.Errorf("order = %v", order)
	}
	if err := events.Dispatch(ctx, bus, struct{ Unheard bool }{}); err != nil {
		t.Errorf("event without listeners = %v", err)
	}
	if !events.HasListeners[OrderPaid](bus) || events.HasListeners[struct{}](bus) {
		t.Error("HasListeners is wrong")
	}
}

func TestFailingListenerErrorReachesCaller(t *testing.T) {
	bus := events.New()
	smtp := errors.New("smtp down")
	ran := false
	events.Listen(bus, func(context.Context, UserRegistered) error { return smtp })
	events.Listen(bus, func(context.Context, UserRegistered) error { panic("boom") })
	events.Listen(bus, func(context.Context, UserRegistered) error { ran = true; return nil })
	err := events.Dispatch(context.Background(), bus, UserRegistered{})
	if !errors.Is(err, smtp) || !strings.Contains(err.Error(), "panic: boom") || !strings.Contains(err.Error(), "listener 2") {
		t.Errorf("Dispatch = %v", err)
	}
	if !ran {
		t.Error("a failing listener stopped the next one")
	}
}

func TestDispatchIsSafeForConcurrentUse(t *testing.T) {
	bus := events.New()
	var count atomic.Int64
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			events.Listen(bus, func(context.Context, OrderPaid) error { count.Add(1); return nil })
		}()
		go func() {
			defer wg.Done()
			if err := events.Dispatch(context.Background(), bus, OrderPaid{OrderID: 1}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	count.Store(0)
	if err := events.Dispatch(context.Background(), bus, OrderPaid{}); err != nil || count.Load() != 20 {
		t.Errorf("after registration: %d listeners ran, %v", count.Load(), err)
	}
}

func TestQueuedListeners(t *testing.T) {
	ctx := context.Background()

	// With a sync queue the listener runs during Dispatch and its error,
	// after the queue's retries, reaches the caller.
	syncQueue := queue.NewSync()
	bus := events.New()
	var got []string
	events.ListenQueued(bus, syncQueue, "events.welcome", func(_ context.Context, e UserRegistered) error {
		got = append(got, e.Email)
		if e.Email == "bad@example.com" {
			return errors.New("bounced")
		}
		return nil
	}, queue.Tries(2))
	if err := events.Dispatch(ctx, bus, UserRegistered{Email: "ada@example.com"}); err != nil || len(got) != 1 {
		t.Fatalf("sync = %v, %v", got, err)
	}
	if err := events.Dispatch(ctx, bus, UserRegistered{Email: "bad@example.com"}); err == nil || !strings.Contains(err.Error(), "bounced") {
		t.Errorf("sync failure = %v", err)
	}

	// With a database queue, Dispatch only stores the event; the worker runs
	// the listener, and a failing one ends in failed job storage.
	db, err := database.Connect(database.Config{Driver: "sqlite", Database: filepath.Join(t.TempDir(), "events.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := queue.CreateTables(schema.New(db.DB, "sqlite")); err != nil {
		t.Fatal(err)
	}
	q := queue.New(&queue.SQLStore{DB: db}, queue.WithBackoff(func(int) time.Duration { return 0 }))
	stored := events.New()
	ran := 0
	events.ListenQueued(stored, q, "events.welcome", func(_ context.Context, e UserRegistered) error {
		ran++
		return errors.New("bounced")
	}, queue.Tries(2))
	if err := events.Dispatch(ctx, stored, UserRegistered{UserID: 7, Email: "x@example.com"}); err != nil {
		t.Fatal(err)
	}
	if ran != 0 {
		t.Fatal("a queued listener ran during Dispatch")
	}
	if err := q.WorkUntilEmpty(ctx); err != nil {
		t.Fatal(err)
	}
	failed, err := q.Failed(ctx)
	if err != nil || len(failed) != 1 || ran != 2 || failed[0].Error != "bounced" || string(failed[0].Payload) != `{"user_id":7,"email":"x@example.com"}` {
		t.Errorf("failed = %+v, %v after %d runs", failed, err, ran)
	}
}
