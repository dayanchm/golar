package mail

import (
	"context"
	"log"
	"net/mail"
	"strings"
	"sync"
)

// Log writes messages to a logger instead of sending them, for development.
// The text body is logged in full, so links such as password resets can be
// followed. Never use it in production.
type Log struct {
	// Logger defaults to the standard logger.
	Logger *log.Logger
}

func (l Log) Send(_ context.Context, m Message) error {
	if err := m.Validate(); err != nil {
		return err
	}
	body := m.Text
	if body == "" {
		body = m.HTML
	}
	logger := l.Logger
	if logger == nil {
		logger = log.Default()
	}
	logger.Printf("mail: from=%q to=%q subject=%q\n%s", m.From, strings.Join(m.Recipients(), ", "), m.Subject, body)
	return nil
}

// TB is the part of testing.TB that Fake's assertions use.
type TB interface {
	Helper()
	Errorf(format string, args ...any)
}

// Fake records messages in memory for tests. Set Err to make Send fail.
type Fake struct {
	mu       sync.Mutex
	messages []Message
	Err      error
}

func NewFake() *Fake { return &Fake{} }

// Send validates and records m, or returns Err.
func (f *Fake) Send(_ context.Context, m Message) error {
	if err := m.Validate(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return f.Err
	}
	f.messages = append(f.messages, m)
	return nil
}

// Messages returns the recorded messages in send order.
func (f *Fake) Messages() []Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Message(nil), f.messages...)
}

// SentTo returns the messages with address in To, Cc or Bcc, ignoring case
// and display names.
func (f *Fake) SentTo(address string) []Message {
	want := strings.ToLower(address)
	if parsed, err := mail.ParseAddress(address); err == nil {
		want = strings.ToLower(parsed.Address)
	}
	var out []Message
	for _, m := range f.Messages() {
		for _, recipient := range m.Recipients() {
			if strings.ToLower(recipient) == want {
				out = append(out, m)
				break
			}
		}
	}
	return out
}

// Reset forgets recorded messages.
func (f *Fake) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = nil
}

// AssertSent fails t unless a message was sent to address, and returns the
// latest one.
func (f *Fake) AssertSent(t TB, address string) Message {
	t.Helper()
	sent := f.SentTo(address)
	if len(sent) == 0 {
		t.Errorf("mail: no message sent to %s (sent %d)", address, len(f.Messages()))
		return Message{}
	}
	return sent[len(sent)-1]
}

// AssertCount fails t unless exactly n messages were sent.
func (f *Fake) AssertCount(t TB, n int) {
	t.Helper()
	if got := len(f.Messages()); got != n {
		t.Errorf("mail: sent %d messages, want %d", got, n)
	}
}

// AssertNothingSent fails t if any message was sent.
func (f *Fake) AssertNothingSent(t TB) {
	t.Helper()
	if got := len(f.Messages()); got != 0 {
		t.Errorf("mail: sent %d messages, want none", got)
	}
}
