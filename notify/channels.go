package notify

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	netmail "net/mail"
	"reflect"
	"time"

	"github.com/dayanchm/golar/database/orm"
	"github.com/dayanchm/golar/database/schema"
	"github.com/dayanchm/golar/mail"
	"github.com/dayanchm/golar/queue"
)

// Mailable is a notification that can be sent by email.
type Mailable interface {
	ToMail(Recipient) mail.Message
}

// MailChannel sends Mailable notifications with a mail.Mailer, such as
// mail.NewFake() in tests.
type MailChannel struct {
	Mailer mail.Mailer
	// From is used when the message sets none.
	From string
}

func (*MailChannel) Name() string { return Mail }

// Send addresses the message to the recipient when it has no To.
func (c *MailChannel) Send(ctx context.Context, to Recipient, n Notification) error {
	mailable, ok := n.(Mailable)
	if !ok {
		return fmt.Errorf("%T does not implement notify.Mailable", n)
	}
	message := mailable.ToMail(to)
	if len(message.To) == 0 {
		if to.Email == "" {
			return fmt.Errorf("recipient %q has no email address", to.ID)
		}
		address := to.Email
		if to.Name != "" {
			address = (&netmail.Address{Name: to.Name, Address: to.Email}).String()
		}
		message.To = []string{address}
	}
	if message.From == "" {
		message.From = c.From
	}
	return c.Mailer.Send(ctx, message)
}

// Storable is a notification kept in the database for an in-app list.
// ToDatabase returns the data to store, encoded as JSON.
type Storable interface {
	ToDatabase(Recipient) any
}

// Typer names a notification's type in storage. Without it, the Go type
// name is used, such as "billing.InvoicePaid".
type Typer interface {
	NotificationType() string
}

// Table is the default table for DatabaseChannel.
const Table = "notifications"

// Columns defines the notifications table. Times are Unix milliseconds;
// read_at is 0 while unread. A migration can create it with
// s.Create(notify.Table, notify.Columns).
func Columns(t *schema.Blueprint) {
	t.String("id", 32).Primary()
	t.String("recipient_id", 191)
	t.String("type", 191)
	t.Text("data")
	t.BigInteger("read_at")
	t.BigInteger("created_at")
	t.Index("recipient_id", "read_at")
}

// CreateTables creates the notifications table if it is missing. It checks
// the database, so it cannot run under migrate --pretend.
func CreateTables(s *schema.Schema) error {
	ok, err := s.HasTable(Table)
	if err != nil || ok {
		return err
	}
	return s.Create(Table, Columns)
}

// Stored is a notification kept by DatabaseChannel.
type Stored struct {
	ID          string
	RecipientID string
	Type        string
	Data        json.RawMessage
	ReadAt      time.Time // zero while unread
	CreatedAt   time.Time
}

// Read reports whether the notification has been marked read.
func (s Stored) Read() bool { return !s.ReadAt.IsZero() }

// DatabaseChannel stores Storable notifications with read and unread state.
type DatabaseChannel struct {
	DB    orm.DB
	Table string // defaults to Table
	now   func() time.Time
}

func (*DatabaseChannel) Name() string { return Database }

func (c *DatabaseChannel) table() string {
	if c.Table != "" {
		return c.Table
	}
	return Table
}

func (c *DatabaseChannel) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *DatabaseChannel) Send(ctx context.Context, to Recipient, n Notification) error {
	storable, ok := n.(Storable)
	if !ok {
		return fmt.Errorf("%T does not implement notify.Storable", n)
	}
	if to.ID == "" {
		return errors.New("recipient has no ID")
	}
	data, err := json.Marshal(storable.ToDatabase(to))
	if err != nil {
		return err
	}
	_, err = c.DB.Table(c.table()).InsertContext(ctx, map[string]any{
		"id": newID(), "recipient_id": to.ID, "type": typeName(n), "data": string(data),
		"read_at": int64(0), "created_at": c.clock().UnixMilli(),
	})
	return err
}

func typeName(n Notification) string {
	if typer, ok := n.(Typer); ok {
		return typer.NotificationType()
	}
	t := reflect.TypeOf(n)
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.String()
}

// List returns the recipient's notifications, newest first; with unreadOnly,
// only those not yet read. limit 0 returns all.
func (c *DatabaseChannel) List(ctx context.Context, recipientID string, unreadOnly bool, limit int) ([]Stored, error) {
	b := c.DB.Table(c.table()).Select("id", "recipient_id", "type", "data", "read_at", "created_at").
		Where("recipient_id", "=", recipientID)
	if unreadOnly {
		b = b.Where("read_at", "=", 0)
	}
	b = b.OrderBy("created_at", "desc").OrderBy("id", "desc")
	if limit > 0 {
		b = b.Limit(limit)
	}
	rows, err := b.QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Stored
	for rows.Next() {
		var s Stored
		var data string
		var read, created int64
		if err := rows.Scan(&s.ID, &s.RecipientID, &s.Type, &data, &read, &created); err != nil {
			return nil, err
		}
		s.Data, s.CreatedAt = json.RawMessage(data), time.UnixMilli(created)
		if read > 0 {
			s.ReadAt = time.UnixMilli(read)
		}
		list = append(list, s)
	}
	return list, errors.Join(rows.Err(), rows.Close())
}

// UnreadCount returns how many notifications the recipient has not read.
func (c *DatabaseChannel) UnreadCount(ctx context.Context, recipientID string) (int64, error) {
	return c.DB.Table(c.table()).Where("recipient_id", "=", recipientID).Where("read_at", "=", 0).Count(ctx)
}

// MarkRead marks one of the recipient's notifications read. The recipient
// is checked, so a user cannot mark another user's notification.
func (c *DatabaseChannel) MarkRead(ctx context.Context, recipientID, id string) error {
	_, err := c.DB.Table(c.table()).Where("id", "=", id).Where("recipient_id", "=", recipientID).Where("read_at", "=", 0).
		UpdateContext(ctx, map[string]any{"read_at": c.clock().UnixMilli()})
	return err
}

// MarkAllRead marks every unread notification of the recipient read.
func (c *DatabaseChannel) MarkAllRead(ctx context.Context, recipientID string) error {
	_, err := c.DB.Table(c.table()).Where("recipient_id", "=", recipientID).Where("read_at", "=", 0).
		UpdateContext(ctx, map[string]any{"read_at": c.clock().UnixMilli()})
	return err
}

// Delete removes one of the recipient's notifications.
func (c *DatabaseChannel) Delete(ctx context.Context, recipientID, id string) error {
	_, err := c.DB.Table(c.table()).Where("id", "=", id).Where("recipient_id", "=", recipientID).DeleteContext(ctx)
	return err
}

// queued is the payload of a queued notification.
type queued[T any] struct {
	To           Recipient `json:"to"`
	Notification T         `json:"notification"`
}

// Queued registers queued delivery of notifications of type T as the queue
// job name, and returns a function that queues one. A worker sends it on its
// channels with the queue's retries; one that keeps failing ends in failed
// job storage. T must be serializable with encoding/json. Call Queued in the
// worker process too, as with any job.
func Queued[T Notification](n *Notifier, q *queue.Queue, name string, options ...queue.HandlerOption) func(context.Context, Recipient, T) error {
	queue.Handle(q, name, func(ctx context.Context, job queued[T]) error {
		return n.Send(ctx, job.To, job.Notification)
	}, options...)
	return func(ctx context.Context, to Recipient, notification T) error {
		return queue.Dispatch(ctx, q, name, queued[T]{To: to, Notification: notification})
	}
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
