package queue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"
)

// WorkOptions configures Work. Zero values use the defaults noted.
type WorkOptions struct {
	// Queues are worked in order of priority; the default is DefaultQueue.
	Queues []string
	// Concurrency is how many jobs run at once; the default is 1.
	Concurrency int
	// Poll is how long to wait when every queue is empty; the default is
	// one second.
	Poll time.Duration
	// Logger records failures; the default is slog.Default().
	Logger *slog.Logger
}

// Work runs jobs until ctx ends, then waits for the running jobs to finish;
// they are not cancelled by ctx, only by their own timeout. A job that
// fails is released for a later retry with backoff, or moved to failed job
// storage once it has used its tries. Run several workers, in one process
// or many, to share the load.
func (q *Queue) Work(ctx context.Context, options WorkOptions) error {
	if q.store == nil {
		return errors.New("queue: a sync queue has nothing to work; use New with a Store")
	}
	queues := options.Queues
	if len(queues) == 0 {
		queues = []string{DefaultQueue}
	}
	concurrency := max(options.Concurrency, 1)
	poll := options.Poll
	if poll <= 0 {
		poll = time.Second
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}

	slots := make(chan struct{}, concurrency)
	var running sync.WaitGroup
	defer running.Wait()
	for {
		select {
		case <-ctx.Done():
			return nil
		case slots <- struct{}{}:
		}
		job, ok, err := q.store.Reserve(ctx, queues, q.lease())
		if err != nil || !ok {
			<-slots
			if err != nil && ctx.Err() == nil {
				logger.Error("queue: reserve", "error", err)
			}
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(poll):
			}
			continue
		}
		running.Add(1)
		go func() {
			defer running.Done()
			defer func() { <-slots }()
			q.process(context.WithoutCancel(ctx), job, logger)
		}()
	}
}

// lease is how long a reservation lasts: the longest handler timeout plus a
// margin, so a job is never handed out again while it may still be running.
func (q *Queue) lease() time.Duration {
	q.mu.RLock()
	defer q.mu.RUnlock()
	longest := time.Minute
	for _, h := range q.handlers {
		longest = max(longest, h.timeout)
	}
	return longest + 30*time.Second
}

func (q *Queue) process(ctx context.Context, job Record, logger *slog.Logger) {
	h, ok := q.handler(job.Name)
	var err error
	if !ok {
		err = fmt.Errorf("%w for %q", ErrUnknownJob, job.Name)
	} else {
		err = run(ctx, h, job.Payload)
	}
	switch {
	case err == nil:
		err = q.store.Delete(ctx, job)
	case !ok || job.Attempts >= tries(job, h):
		logger.Error("queue: job failed", "job", job.Name, "id", job.ID, "attempts", job.Attempts, "error", err)
		err = q.store.Fail(ctx, job, err.Error())
	default:
		logger.Warn("queue: job will be retried", "job", job.Name, "id", job.ID, "attempts", job.Attempts, "error", err)
		err = q.store.Release(ctx, job, q.now().Add(q.backoff(job.Attempts)))
	}
	if err != nil {
		logger.Error("queue: record job result", "job", job.Name, "id", job.ID, "error", err)
	}
}

// WorkUntilEmpty runs available jobs on queues one at a time until none is
// left, for tests and scripts. Delayed jobs that are not yet due are left.
func (q *Queue) WorkUntilEmpty(ctx context.Context, queues ...string) error {
	if q.store == nil {
		return nil
	}
	if len(queues) == 0 {
		queues = []string{DefaultQueue}
	}
	queues = slices.Clone(queues)
	logger := slog.New(slog.DiscardHandler)
	for {
		job, ok, err := q.store.Reserve(ctx, queues, q.lease())
		if err != nil || !ok {
			return err
		}
		q.process(ctx, job, logger)
	}
}

func tries(job Record, h handler) int {
	if job.MaxTries > 0 {
		return job.MaxTries
	}
	return h.tries
}
