// Package events decouples side effects from the code that causes them. An
// event is any Go value; listeners register for its type.
//
//	bus := events.New()
//	events.Listen(bus, func(ctx context.Context, e UserRegistered) error {
//		return audit.Record(ctx, "registered", e.UserID)
//	})
//	events.ListenQueued(bus, q, "events.welcome-email", func(ctx context.Context, e UserRegistered) error {
//		return sendWelcome(ctx, e.UserID)
//	})
//
//	err := events.Dispatch(ctx, bus, UserRegistered{UserID: user.ID})
package events

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"

	"github.com/dayanchm/golar/queue"
)

// Bus holds listeners by event type. It is safe for concurrent use.
type Bus struct {
	mu        sync.RWMutex
	listeners map[reflect.Type][]func(context.Context, any) error
}

// New returns an empty Bus.
func New() *Bus { return &Bus{listeners: map[reflect.Type][]func(context.Context, any) error{}} }

func typeOf[E any]() reflect.Type { return reflect.TypeFor[E]() }

// Listen registers fn for events of type E. Listeners run in the order they
// were registered.
func Listen[E any](b *Bus, fn func(context.Context, E) error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	t := typeOf[E]()
	b.listeners[t] = append(b.listeners[t], func(ctx context.Context, event any) error {
		return fn(ctx, event.(E))
	})
}

// ListenQueued runs fn on the queue instead of during Dispatch: it registers
// fn as a job named name, and Dispatch queues the event, encoded as JSON, for
// a worker. A failing run is retried and finally moves to the queue's failed
// job storage. Register it in the worker process too, as with any job.
func ListenQueued[E any](b *Bus, q *queue.Queue, name string, fn func(context.Context, E) error, options ...queue.HandlerOption) {
	queue.Handle(q, name, fn, options...)
	Listen(b, func(ctx context.Context, event E) error {
		return queue.Dispatch(ctx, q, name, event)
	})
}

// Dispatch runs every listener for the event's type, in order, and returns
// their errors joined: one failing listener does not stop the others. For
// queued listeners, the error is that of queuing the event. An event with
// no listeners is not an error.
func Dispatch[E any](ctx context.Context, b *Bus, event E) error {
	b.mu.RLock()
	listeners := b.listeners[typeOf[E]()]
	b.mu.RUnlock()
	var errs []error
	for i, listener := range listeners {
		if err := call(ctx, listener, event); err != nil {
			errs = append(errs, fmt.Errorf("events: %T listener %d: %w", event, i+1, err))
		}
	}
	return errors.Join(errs...)
}

// call runs a listener, turning a panic into an error so the remaining
// listeners still run.
func call(ctx context.Context, listener func(context.Context, any) error, event any) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return listener(ctx, event)
}

// HasListeners reports whether events of type E have any listener.
func HasListeners[E any](b *Bus) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.listeners[typeOf[E]()]) > 0
}
