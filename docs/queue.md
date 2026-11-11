# Queue

`github.com/dayanchm/golar/queue` runs work outside the request: sending
email, calling slow APIs, processing uploads. A job is a name plus a JSON
payload. It is stored, and a worker runs it later with retries.

```go
type Welcome struct {
    UserID int64 `json:"user_id"`
}

q := queue.New(&queue.SQLStore{DB: db})

queue.Handle(q, "emails.welcome", func(ctx context.Context, job Welcome) error {
    return sendWelcome(ctx, job.UserID)
}, queue.Tries(5), queue.Timeout(30*time.Second))

err := queue.Dispatch(ctx, q, "emails.welcome", Welcome{UserID: 42})
```

`Handle` registers a typed handler by name. Payloads are encoded with
`encoding/json` when dispatched and decoded into the handler's type when run,
so keep them small and serializable: IDs rather than whole records. Register
handlers in every process that dispatches or works jobs, usually from one
function both call. Dispatching a name with no handler returns
`queue.ErrUnknownJob`.

## Drivers

| Driver | Created with | Use |
| --- | --- | --- |
| Sync | `queue.NewSync()` | runs the job inside `Dispatch` and returns its error; tests and projects without a worker |
| Database | `queue.New(&queue.SQLStore{DB: db})` | stores jobs in the `jobs` table; they survive restarts and any number of workers share them |

Create the tables in a migration:

```go
func (CreateJobsTables) Up(s *schema.Schema) error {
    if err := s.Create(queue.JobsTable, queue.JobColumns); err != nil {
        return err
    }
    return s.Create(queue.FailedTable, queue.FailedColumns)
}
```

`queue.CreateTables(s)` does the same when the tables are missing, but it
queries the database, so it cannot run under `migrate --pretend`. Payloads are
stored in a `TEXT` column, which holds 64 KiB on MySQL.

## Dispatching

```go
queue.Dispatch(ctx, q, "reports.build", Report{ID: 7}, queue.Delay(10*time.Minute))
queue.Dispatch(ctx, q, "images.resize", Image{Path: p}, queue.OnQueue("media"))
```

| Option | Default | Meaning |
| --- | --- | --- |
| `queue.Delay(d)` | none | the job becomes available after `d` |
| `queue.OnQueue(name)` | `"default"` | the queue the job goes to |
| `queue.Tries(n)` (handler) | 3 | runs before the job fails for good |
| `queue.Timeout(d)` (handler) | 1 minute | limit for one run; the job's context is cancelled after it |

A failed run is retried after a backoff that doubles from 5 seconds up to
10 minutes. Set another with `queue.New(store, queue.WithBackoff(func(attempt int) time.Duration { ... }))`.
A panic in a handler counts as a failure.

## Workers

```go
err := q.Work(ctx, queue.WorkOptions{
    Queues:      []string{"high", "default"}, // in priority order
    Concurrency: 4,                           // jobs at once; default 1
})
```

`Work` runs until `ctx` ends. It then stops taking jobs and waits for the
running ones; they are not cancelled, only limited by their timeout.

Each job is reserved with a compare-and-swap on a random token, so two workers
never run the same job at once. A reservation lasts the longest handler
timeout plus 30 seconds. If a worker dies mid-job, the reservation expires and
another worker runs the job again, so jobs run **at least once**: make
handlers safe to repeat, for example by checking whether the email was
already sent.

`q.WorkUntilEmpty(ctx)` runs every job that is due and returns, for tests and
scripts.

## Failed jobs

A job that fails on its last try, or whose name has no handler, moves to the
`failed_jobs` table with the error message.

```go
failed, err := q.Failed(ctx)       // []queue.FailedJob: ID, Name, Payload, Error, FailedAt
err = q.Retry(ctx, failed[0].ID)   // back on its queue with fresh tries
err = q.Forget(ctx, failed[0].ID)  // delete it
```

## Generated projects

Projects with a database get:

| File | Purpose |
| --- | --- |
| `app/jobs/jobs.go` | `Register` adds the handlers; it has an `example` job |
| `internal/queue/queue.go` | `queue.Jobs`, selected by `QUEUE_DRIVER`, and `Work` |
| `database/migrations/..._create_jobs_tables.go` | the `jobs` and `failed_jobs` tables |

`QUEUE_DRIVER=sync` (the default in `.env`) runs jobs as they are dispatched.
To run them in the background, run `golar migrate`, set
`QUEUE_DRIVER=database` and start a worker next to the server:

```sh
golar serve        # dispatches jobs
golar queue:work   # runs them; Ctrl+C lets running jobs finish
```

`golar queue:work` builds the server and runs it with `--work`, which runs the
application's startup hooks (opening the database), works the queue, then
runs the shutdown hooks. In production, run the binary twice: `server` and
`server --work`. Dispatch from a handler with:

```go
golarqueue.Dispatch(c.Context(), queue.Jobs, "example", jobs.Example{Message: "hi"})
```
