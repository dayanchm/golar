package queue

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dayanchm/golar/database"
	"github.com/dayanchm/golar/database/schema"
)

// connect opens SQLite, or the database named by GOLAR_TEST_DRIVER and
// GOLAR_TEST_DSN, with fresh queue tables.
func connect(t *testing.T) *database.DB {
	t.Helper()
	driver := os.Getenv("GOLAR_TEST_DRIVER")
	if driver == "" {
		driver = "sqlite"
	}
	config := database.Config{Driver: driver, Database: filepath.Join(t.TempDir(), "queue.db")}
	if driver != "sqlite" {
		config.Database = ""
		config.DSN = os.Getenv("GOLAR_TEST_DSN")
		if config.DSN == "" {
			t.Fatal("GOLAR_TEST_DSN is required")
		}
	}
	db, err := database.Connect(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s := schema.New(db.DB, driver)
	drop := func() {
		for _, table := range []string{JobsTable, FailedTable} {
			if err := s.DropIfExists(table); err != nil {
				t.Fatal(err)
			}
		}
	}
	drop()
	t.Cleanup(drop)
	for range 2 { // CreateTables is idempotent
		if err := CreateTables(s); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

type email struct {
	To string `json:"to"`
}

func noBackoff(int) time.Duration { return 0 }

func TestSync(t *testing.T) {
	ctx := context.Background()
	q := NewSync()
	var sent []string
	Handle(q, "email", func(_ context.Context, job email) error {
		sent = append(sent, job.To)
		return nil
	})
	if err := Dispatch(ctx, q, "email", email{To: "ada@example.com"}); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || sent[0] != "ada@example.com" {
		t.Errorf("sent = %v", sent)
	}

	runs := 0
	Handle(q, "flaky", func(context.Context, email) error {
		runs++
		if runs < 3 {
			return errors.New("try again")
		}
		return nil
	}, Tries(3))
	if err := Dispatch(ctx, q, "flaky", email{}); err != nil || runs != 3 {
		t.Errorf("flaky = %v after %d runs", err, runs)
	}
	Handle(q, "broken", func(context.Context, email) error { return errors.New("smtp down") }, Tries(2))
	if err := Dispatch(ctx, q, "broken", email{}); err == nil || !strings.Contains(err.Error(), "smtp down") {
		t.Errorf("broken = %v", err)
	}
	if err := Dispatch(ctx, q, "missing", email{}); !errors.Is(err, ErrUnknownJob) {
		t.Errorf("missing = %v", err)
	}
	Handle(q, "panics", func(context.Context, email) error { panic("boom") }, Tries(1))
	if err := Dispatch(ctx, q, "panics", email{}); err == nil || !strings.Contains(err.Error(), "panic: boom") {
		t.Errorf("panic = %v", err)
	}
	if err := q.Work(ctx, WorkOptions{}); err == nil {
		t.Error("Work on a sync queue succeeded")
	}
}

func TestDatabaseQueue(t *testing.T) {
	ctx := context.Background()
	db := connect(t)
	clock := time.Now()
	now := func() time.Time { return clock }
	store := &SQLStore{DB: db, now: now}
	q := New(store, WithBackoff(noBackoff))
	q.now = now
	var mu sync.Mutex
	var sent []string
	register := func(q *Queue) {
		Handle(q, "email", func(_ context.Context, job email) error {
			mu.Lock()
			defer mu.Unlock()
			sent = append(sent, job.To)
			return nil
		})
	}
	register(q)

	must(t, Dispatch(ctx, q, "email", email{To: "now@example.com"}))
	must(t, Dispatch(ctx, q, "email", email{To: "later@example.com"}, Delay(time.Minute)))
	must(t, Dispatch(ctx, q, "email", email{To: "other@example.com"}, OnQueue("mail")))

	// Jobs survive a restart: a new queue on the same tables runs them.
	restarted := New(&SQLStore{DB: db, now: now}, WithBackoff(noBackoff))
	register(restarted)
	must(t, restarted.WorkUntilEmpty(ctx))
	if fmt.Sprint(sent) != "[now@example.com]" {
		t.Fatalf("after first run sent = %v", sent)
	}
	clock = clock.Add(2 * time.Minute)
	must(t, restarted.WorkUntilEmpty(ctx, "mail", DefaultQueue))
	if fmt.Sprint(sent) != "[now@example.com other@example.com later@example.com]" {
		t.Fatalf("sent = %v", sent)
	}

	// A job that keeps failing ends up in failed job storage.
	runs := 0
	Handle(q, "broken", func(context.Context, email) error { runs++; return errors.New("smtp down") }, Tries(3))
	must(t, Dispatch(ctx, q, "broken", email{To: "x@example.com"}))
	must(t, q.WorkUntilEmpty(ctx))
	failed, err := q.Failed(ctx)
	if err != nil || len(failed) != 1 || runs != 3 {
		t.Fatalf("failed = %+v, %v after %d runs", failed, err, runs)
	}
	if f := failed[0]; f.Name != "broken" || f.Error != "smtp down" || string(f.Payload) != `{"to":"x@example.com"}` || f.Queue != DefaultQueue {
		t.Errorf("failed job = %+v", f)
	}

	// Retry puts it back with fresh tries; Forget deletes it.
	runs = 0
	must(t, q.Retry(ctx, failed[0].ID))
	if failed, _ := q.Failed(ctx); len(failed) != 0 {
		t.Errorf("retried job still failed: %+v", failed)
	}
	must(t, q.WorkUntilEmpty(ctx))
	if runs != 3 {
		t.Errorf("retried job ran %d times, want 3", runs)
	}
	failed, _ = q.Failed(ctx)
	must(t, q.Forget(ctx, failed[0].ID))
	if failed, _ := q.Failed(ctx); len(failed) != 0 {
		t.Errorf("forgotten job still listed: %+v", failed)
	}

	// A job with no handler fails at once instead of being retried.
	must(t, store.Push(ctx, Record{ID: newID(), Queue: DefaultQueue, Name: "gone", Payload: []byte("{}"), MaxTries: 5, AvailableAt: clock}))
	must(t, q.WorkUntilEmpty(ctx))
	if failed, _ := q.Failed(ctx); len(failed) != 1 || !strings.Contains(failed[0].Error, "no handler") {
		t.Errorf("unknown job = %+v", failed)
	}
}

func TestBackoffDelaysRetries(t *testing.T) {
	ctx := context.Background()
	clock := time.Now()
	now := func() time.Time { return clock }
	q := New(&SQLStore{DB: connect(t), now: now})
	q.now = now
	runs := 0
	Handle(q, "flaky", func(context.Context, email) error {
		runs++
		if runs == 1 {
			return errors.New("first run fails")
		}
		return nil
	})
	must(t, Dispatch(ctx, q, "flaky", email{}))
	must(t, q.WorkUntilEmpty(ctx))
	if runs != 1 {
		t.Fatalf("runs = %d before the backoff passed", runs)
	}
	clock = clock.Add(defaultBackoff(1))
	must(t, q.WorkUntilEmpty(ctx))
	if runs != 2 {
		t.Errorf("runs = %d after the backoff", runs)
	}
	if got := []time.Duration{defaultBackoff(1), defaultBackoff(2), defaultBackoff(3), defaultBackoff(20)}; fmt.Sprint(got) != "[5s 10s 20s 10m0s]" {
		t.Errorf("backoff = %v", got)
	}
}

func TestExpiredReservation(t *testing.T) {
	ctx := context.Background()
	clock := time.Now()
	store := &SQLStore{DB: connect(t), now: func() time.Time { return clock }}
	must(t, store.Push(ctx, Record{ID: "job-1", Queue: DefaultQueue, Name: "x", Payload: []byte("{}"), MaxTries: 3, AvailableAt: clock}))
	first, ok, err := store.Reserve(ctx, []string{DefaultQueue}, time.Minute)
	if err != nil || !ok || first.Attempts != 1 {
		t.Fatalf("Reserve = %+v, %v, %v", first, ok, err)
	}
	if _, ok, _ := store.Reserve(ctx, []string{DefaultQueue}, time.Minute); ok {
		t.Fatal("a held job was reserved twice")
	}
	clock = clock.Add(2 * time.Minute) // the first worker is presumed dead
	second, ok, err := store.Reserve(ctx, []string{DefaultQueue}, time.Minute)
	if err != nil || !ok || second.Attempts != 2 || second.Token == first.Token {
		t.Fatalf("Reserve after expiry = %+v, %v, %v", second, ok, err)
	}
	if err := store.Delete(ctx, first); !errors.Is(err, ErrLeaseLost) {
		t.Errorf("Delete with an expired token = %v", err)
	}
	if err := store.Fail(ctx, first, "late"); !errors.Is(err, ErrLeaseLost) {
		t.Errorf("Fail with an expired token = %v", err)
	}
	must(t, store.Delete(ctx, second))
}

// TestWorkersNeverShareAJob runs several workers on one table and checks
// that every job runs exactly once and never twice at the same time.
func TestWorkersNeverShareAJob(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db := connect(t)
	const jobs = 40
	var mu sync.Mutex
	runs := map[int]int{}
	running := map[int]bool{}
	var overlap, done atomic.Int32
	finished := make(chan struct{})
	newWorker := func() *Queue {
		q := New(&SQLStore{DB: db}, WithBackoff(noBackoff))
		Handle(q, "count", func(_ context.Context, n int) error {
			mu.Lock()
			if running[n] {
				overlap.Add(1)
			}
			running[n] = true
			runs[n]++
			mu.Unlock()
			time.Sleep(2 * time.Millisecond)
			mu.Lock()
			running[n] = false
			mu.Unlock()
			if done.Add(1) == jobs {
				close(finished)
			}
			return nil
		})
		return q
	}
	dispatcher := newWorker()
	for n := range jobs {
		must(t, Dispatch(ctx, dispatcher, "count", n))
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := newWorker().Work(ctx, WorkOptions{Concurrency: 3, Poll: 5 * time.Millisecond}); err != nil {
				t.Error(err)
			}
		}()
	}
	select {
	case <-finished:
	case <-time.After(30 * time.Second):
		t.Fatalf("only %d of %d jobs ran", done.Load(), jobs)
	}
	cancel()
	wg.Wait()
	if overlap.Load() != 0 {
		t.Errorf("%d jobs ran on two workers at once", overlap.Load())
	}
	for n := range jobs {
		if runs[n] != 1 {
			t.Errorf("job %d ran %d times", n, runs[n])
		}
	}
}

func TestWorkFinishesRunningJobsOnShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	q := New(&SQLStore{DB: connect(t)})
	started := make(chan struct{})
	var finished atomic.Bool
	Handle(q, "slow", func(ctx context.Context, _ email) error {
		close(started)
		select {
		case <-time.After(100 * time.Millisecond):
			finished.Store(true)
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	must(t, Dispatch(context.Background(), q, "slow", email{}))
	done := make(chan error, 1)
	go func() { done <- q.Work(ctx, WorkOptions{Poll: 5 * time.Millisecond}) }()
	<-started
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !finished.Load() {
		t.Error("shutdown cancelled the running job")
	}
	if failed, _ := q.Failed(context.Background()); len(failed) != 0 {
		t.Errorf("failed = %+v", failed)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
