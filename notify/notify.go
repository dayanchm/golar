// Package notify sends one notification to a user over several channels,
// such as email and an in-app list stored in the database.
//
//	type InvoicePaid struct{ InvoiceID int64 }
//
//	func (InvoicePaid) Via(notify.Recipient) []string { return []string{notify.Mail, notify.Database} }
//	func (n InvoicePaid) ToMail(r notify.Recipient) mail.Message {
//		return mail.Message{Subject: "Invoice paid", Text: fmt.Sprintf("Invoice %d is paid.", n.InvoiceID)}
//	}
//	func (n InvoicePaid) ToDatabase(notify.Recipient) any { return n }
//
//	n := notify.New(
//		&notify.MailChannel{Mailer: mailer, From: "billing@example.com"},
//		&notify.DatabaseChannel{DB: db},
//	)
//	err := n.Send(ctx, notify.Recipient{ID: "42", Email: "ada@example.com"}, InvoicePaid{InvoiceID: 7})
package notify

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Built-in channel names.
const (
	Mail     = "mail"
	Database = "database"
)

// Recipient is who a notification is for. ID identifies them in stored
// notifications; Email and Name address mail.
type Recipient struct {
	ID    string `json:"id"`
	Email string `json:"email,omitempty"`
	Name  string `json:"name,omitempty"`
}

// Notification names the channels it goes to. It also implements the
// interface each of those channels needs, such as Mailable or Storable.
type Notification interface {
	Via(Recipient) []string
}

// Channel delivers notifications of one kind.
type Channel interface {
	Name() string
	Send(ctx context.Context, to Recipient, n Notification) error
}

// Notifier routes notifications to channels. It is safe for concurrent use.
type Notifier struct {
	mu       sync.RWMutex
	channels map[string]Channel
}

// New returns a Notifier with the given channels.
func New(channels ...Channel) *Notifier {
	n := &Notifier{channels: map[string]Channel{}}
	for _, channel := range channels {
		n.Register(channel)
	}
	return n
}

// Register adds a channel, replacing one with the same name.
func (n *Notifier) Register(channel Channel) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.channels[channel.Name()] = channel
}

// Send delivers the notification on every channel it names. All channels are
// tried; their errors are joined, and an unknown channel is an error.
func (n *Notifier) Send(ctx context.Context, to Recipient, notification Notification) error {
	var errs []error
	for _, name := range notification.Via(to) {
		n.mu.RLock()
		channel, ok := n.channels[name]
		n.mu.RUnlock()
		if !ok {
			errs = append(errs, fmt.Errorf("notify: no %q channel registered", name))
			continue
		}
		if err := channel.Send(ctx, to, notification); err != nil {
			errs = append(errs, fmt.Errorf("notify: %s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

// SendAll sends the notification to each recipient and joins the errors.
func (n *Notifier) SendAll(ctx context.Context, to []Recipient, notification Notification) error {
	var errs []error
	for _, recipient := range to {
		if err := n.Send(ctx, recipient, notification); err != nil {
			errs = append(errs, fmt.Errorf("to %s: %w", recipient.ID, err))
		}
	}
	return errors.Join(errs...)
}
