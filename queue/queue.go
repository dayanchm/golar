// Package queue runs work outside the request: jobs are dispatched with a
// JSON payload, stored, and run by workers with retries and delays.
//
//	q := queue.New(&queue.SQLStore{DB: db})
//	queue.Handle(q, "emails.welcome", func(ctx context.Context, job Welcome) error {
//		return mailer.Send(ctx, welcomeMessage(job.UserID))
//	}, queue.Tries(5))
//
//	err := queue.Dispatch(ctx, q, "emails.welcome", Welcome{UserID: 1}, queue.Delay(time.Minute))
//	err = q.Work(ctx, queue.WorkOptions{}) // in the worker process
//
// Handlers are registered by name in every process that dispatches or works,
// usually from one function both call.
package queue

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// DefaultQueue is the queue jobs go to unless OnQueue names another.
const DefaultQueue = "default"

// ErrUnknownJob is returned for a job name that has no handler.
var ErrUnknownJob = errors.New("queue: no handler registered")

// Record is a job as a Store keeps it.
type Record struct {
	ID          string
	Queue       string
	Name        string
	Payload     []byte
	Attempts    int // runs started, including the current one
	MaxTries    int // 0 uses the handler's Tries
	AvailableAt time.Time
	// Token identifies the current reservation; Delete, Release and Fail
	// only act while it is still held.
	Token string
}

// FailedJob is a job that used up its tries.
type FailedJob struct {
	ID       string
	Queue    string
	Name     string
	Payload  []byte
	Error    string
	FailedAt time.Time
}

// ErrLeaseLost means a job's reservation expired and another worker may
// have taken it, so the result of this run was not recorded.
var ErrLeaseLost = errors.New("queue: job reservation expired")

// Store keeps jobs. Reserve must hand a job to one caller at a time; a
// reservation that is not finished within its lease becomes available again,
// so jobs run at least once.
type Store interface {
	Push(ctx context.Context, job Record) error
	// Reserve takes the next available job on one of queues, or reports
	// false when there is none.
	Reserve(ctx context.Context, queues []string, lease time.Duration) (Record, bool, error)
	Delete(ctx context.Context, job Record) error
	// Release makes the job available again at the given time.
	Release(ctx context.Context, job Record, at time.Time) error
	// Fail moves the job to failed job storage.
	Fail(ctx context.Context, job Record, reason string) error
	Failed(ctx context.Context) ([]FailedJob, error)
	// Retry moves a failed job back onto its queue with fresh tries.
	Retry(ctx context.Context, id string) error
	Forget(ctx context.Context, id string) error
}

type handler struct {
	run     func(context.Context, []byte) error
	tries   int
	timeout time.Duration
}

// Queue dispatches jobs to a Store and runs them. It is safe for concurrent use.
type Queue struct {
	store   Store // nil runs jobs as they are dispatched
	backoff func(attempt int) time.Duration
	now     func() time.Time

	mu       sync.RWMutex
	handlers map[string]handler
}

// Option configures a Queue.
type Option func(*Queue)

// WithBackoff sets the delay before a failed job runs again; attempt is the
// number of runs so far. The default doubles from 5 seconds up to 10 minutes.
func WithBackoff(backoff func(attempt int) time.Duration) Option {
	return func(q *Queue) { q.backoff = backoff }
}

// New returns a Queue that stores jobs in store for workers to run.
func New(store Store, options ...Option) *Queue {
	q := &Queue{store: store, backoff: defaultBackoff, now: time.Now, handlers: map[string]handler{}}
	for _, option := range options {
		option(q)
	}
	return q
}

// NewSync returns a Queue that runs each job as it is dispatched, retrying
// without delay, and returns its error to Dispatch. Use it in tests and in
// projects without a worker.
func NewSync(options ...Option) *Queue { return New(nil, options...) }

func defaultBackoff(attempt int) time.Duration {
	delay := 5 * time.Second
	for range attempt - 1 {
		delay *= 2
		if delay >= 10*time.Minute {
			return 10 * time.Minute
		}
	}
	return delay
}

// HandlerOption configures a job handler.
type HandlerOption func(*handler)

// Tries sets how often a job runs before it fails for good; the default is 3.
func Tries(n int) HandlerOption {
	return func(h *handler) {
		if n > 0 {
			h.tries = n
		}
	}
}

// Timeout limits one run of the job; the default is one minute.
func Timeout(d time.Duration) HandlerOption {
	return func(h *handler) {
		if d > 0 {
			h.timeout = d
		}
	}
}

// Handle registers fn for jobs named name, replacing any earlier handler.
// Payloads are decoded from JSON into T.
func Handle[T any](q *Queue, name string, fn func(context.Context, T) error, options ...HandlerOption) {
	h := handler{tries: 3, timeout: time.Minute, run: func(ctx context.Context, payload []byte) error {
		var job T
		if err := json.Unmarshal(payload, &job); err != nil {
			return fmt.Errorf("queue: decode %s: %w", name, err)
		}
		return fn(ctx, job)
	}}
	for _, option := range options {
		option(&h)
	}
	q.mu.Lock()
	q.handlers[name] = h
	q.mu.Unlock()
}

func (q *Queue) handler(name string) (handler, bool) {
	q.mu.RLock()
	defer q.mu.RUnlock()
	h, ok := q.handlers[name]
	return h, ok
}

type dispatch struct {
	delay time.Duration
	queue string
}

// DispatchOption configures one dispatch.
type DispatchOption func(*dispatch)

// Delay makes the job available only after d.
func Delay(d time.Duration) DispatchOption { return func(o *dispatch) { o.delay = d } }

// OnQueue puts the job on the named queue instead of DefaultQueue.
func OnQueue(name string) DispatchOption { return func(o *dispatch) { o.queue = name } }

// Dispatch queues the job named name with payload, encoded as JSON. The name
// must have a handler. With NewSync the job runs before Dispatch returns.
func Dispatch[T any](ctx context.Context, q *Queue, name string, payload T, options ...DispatchOption) error {
	h, ok := q.handler(name)
	if !ok {
		return fmt.Errorf("%w for %q", ErrUnknownJob, name)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("queue: encode %s: %w", name, err)
	}
	o := dispatch{queue: DefaultQueue}
	for _, option := range options {
		option(&o)
	}
	if q.store == nil {
		return q.runSync(ctx, name, h, body)
	}
	return q.store.Push(ctx, Record{
		ID: newID(), Queue: o.queue, Name: name, Payload: body,
		MaxTries: h.tries, AvailableAt: q.now().Add(o.delay),
	})
}

func (q *Queue) runSync(ctx context.Context, name string, h handler, payload []byte) error {
	var err error
	for range h.tries {
		if err = run(ctx, h, payload); err == nil {
			return nil
		}
		if ctx.Err() != nil {
			break
		}
	}
	return fmt.Errorf("queue: %s failed: %w", name, err)
}

// run calls the handler with its timeout, turning a panic into an error.
func run(ctx context.Context, h handler, payload []byte) (err error) {
	ctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return h.run(ctx, payload)
}

// Failed lists jobs that used up their tries.
func (q *Queue) Failed(ctx context.Context) ([]FailedJob, error) {
	if q.store == nil {
		return nil, nil
	}
	return q.store.Failed(ctx)
}

// Retry puts a failed job back on its queue with fresh tries.
func (q *Queue) Retry(ctx context.Context, id string) error {
	if q.store == nil {
		return errors.New("queue: a sync queue keeps no failed jobs")
	}
	return q.store.Retry(ctx, id)
}

// Forget deletes a failed job.
func (q *Queue) Forget(ctx context.Context, id string) error {
	if q.store == nil {
		return nil
	}
	return q.store.Forget(ctx, id)
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
