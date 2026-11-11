# Events

`github.com/dayanchm/golar/events` separates what happened from what to do
about it. A handler dispatches an event; listeners elsewhere send email,
record audits or update counters without the handler knowing about them.

```go
type UserRegistered struct {
    UserID int64  `json:"user_id"`
    Email  string `json:"email"`
}

bus := events.New()

events.Listen(bus, func(ctx context.Context, e UserRegistered) error {
    return audit.Record(ctx, "user.registered", e.UserID)
})

err := events.Dispatch(ctx, bus, UserRegistered{UserID: user.ID, Email: user.Email})
```

An event is any Go value. Listeners are typed and registered for that type;
`Dispatch` runs the listeners for the event's type in registration order.
Dispatching an event that has no listeners does nothing.

## Errors

Every listener runs, even when an earlier one fails or panics. `Dispatch`
returns their errors joined with `errors.Join`, each prefixed with the event
type and the listener's position, so `errors.Is` finds a specific error:

```go
if err := events.Dispatch(ctx, bus, OrderPaid{ID: id}); err != nil {
    return err // or log it, if the side effects are optional
}
```

## Queued listeners

Slow or unreliable work belongs on the [queue](queue.md):

```go
events.ListenQueued(bus, q, "events.welcome-email", func(ctx context.Context, e UserRegistered) error {
    return sendWelcome(ctx, e.Email)
}, queue.Tries(5))
```

`ListenQueued` registers the function as a queue job named by its second
argument. `Dispatch` then encodes the event as JSON and queues it; the error
it returns is that of queuing. A worker runs the listener with the queue's
retries and backoff, and a listener that keeps failing ends up in the queue's
failed job storage. With `queue.NewSync()` the listener runs during
`Dispatch`, and its final error is returned there.

Queued events must be serializable with `encoding/json`, and the worker
process must register the same listeners, as with any job. Register them in
one function that both the server and the worker call.

## Concurrency

A `Bus` is safe for concurrent use: listeners can be registered while events
are dispatched. Register listeners at startup, though, so every event sees
them. `events.HasListeners[E](bus)` reports whether a type has any.
