package notify

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dayanchm/golar/database"
	"github.com/dayanchm/golar/database/schema"
	"github.com/dayanchm/golar/mail"
	"github.com/dayanchm/golar/queue"
)

type InvoicePaid struct {
	InvoiceID int64    `json:"invoice_id"`
	Channels  []string `json:"channels"`
}

func (n InvoicePaid) Via(Recipient) []string { return n.Channels }

func (n InvoicePaid) ToMail(r Recipient) mail.Message {
	return mail.Message{Subject: "Invoice paid", Text: "Hello " + r.Name + ", your invoice is paid."}
}

func (n InvoicePaid) ToDatabase(Recipient) any { return map[string]any{"invoice_id": n.InvoiceID} }

// MailOnly does not implement Storable.
type MailOnly struct{}

func (MailOnly) Via(Recipient) []string { return []string{Database} }

func connect(t *testing.T) *database.DB {
	t.Helper()
	driver := os.Getenv("GOLAR_TEST_DRIVER")
	if driver == "" {
		driver = "sqlite"
	}
	config := database.Config{Driver: driver, Database: filepath.Join(t.TempDir(), "notify.db")}
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
		for _, table := range []string{Table, queue.JobsTable, queue.FailedTable} {
			if err := s.DropIfExists(table); err != nil {
				t.Fatal(err)
			}
		}
	}
	drop()
	t.Cleanup(drop)
	for range 2 {
		if err := CreateTables(s); err != nil {
			t.Fatal(err)
		}
	}
	if err := queue.CreateTables(s); err != nil {
		t.Fatal(err)
	}
	return db
}

var ada = Recipient{ID: "42", Email: "ada@example.com", Name: "Ada"}

func TestOneNotificationOnSeveralChannels(t *testing.T) {
	ctx := context.Background()
	fake := mail.NewFake()
	clock := time.Now()
	store := &DatabaseChannel{DB: connect(t), now: func() time.Time { return clock }}
	n := New(&MailChannel{Mailer: fake, From: "billing@example.com"}, store)

	if err := n.Send(ctx, ada, InvoicePaid{InvoiceID: 7, Channels: []string{Mail, Database}}); err != nil {
		t.Fatal(err)
	}
	message := fake.AssertSent(t, "ada@example.com")
	if message.From != "billing@example.com" || message.To[0] != `"Ada" <ada@example.com>` || !strings.Contains(message.Text, "Hello Ada") {
		t.Errorf("mail = %+v", message)
	}
	list, err := store.List(ctx, "42", false, 0)
	if err != nil || len(list) != 1 {
		t.Fatalf("List = %+v, %v", list, err)
	}
	if s := list[0]; s.Type != "notify.InvoicePaid" || string(s.Data) != `{"invoice_id":7}` || s.Read() || s.RecipientID != "42" {
		t.Errorf("stored = %+v", s)
	}

	// Read and unread state.
	clock = clock.Add(time.Second)
	must(t, n.Send(ctx, ada, InvoicePaid{InvoiceID: 8, Channels: []string{Database}}))
	must(t, n.Send(ctx, Recipient{ID: "other"}, InvoicePaid{InvoiceID: 9, Channels: []string{Database}}))
	if count, err := store.UnreadCount(ctx, "42"); err != nil || count != 2 {
		t.Fatalf("UnreadCount = %d, %v", count, err)
	}
	list, _ = store.List(ctx, "42", false, 0)
	var data struct {
		InvoiceID int64 `json:"invoice_id"`
	}
	json.Unmarshal(list[0].Data, &data)
	if data.InvoiceID != 8 {
		t.Errorf("newest first: got invoice %d", data.InvoiceID)
	}
	must(t, store.MarkRead(ctx, "other", list[0].ID)) // another user's ID changes nothing
	if count, _ := store.UnreadCount(ctx, "42"); count != 2 {
		t.Error("a user marked another user's notification read")
	}
	must(t, store.MarkRead(ctx, "42", list[0].ID))
	unread, _ := store.List(ctx, "42", true, 0)
	if len(unread) != 1 || unread[0].ID != list[1].ID {
		t.Errorf("unread = %+v", unread)
	}
	if all, _ := store.List(ctx, "42", false, 1); len(all) != 1 || !all[0].Read() {
		t.Errorf("limit and read state = %+v", all)
	}
	must(t, store.MarkAllRead(ctx, "42"))
	if count, _ := store.UnreadCount(ctx, "42"); count != 0 {
		t.Errorf("after MarkAllRead: %d unread", count)
	}
	must(t, store.Delete(ctx, "42", list[1].ID))
	if all, _ := store.List(ctx, "42", false, 0); len(all) != 1 {
		t.Errorf("after Delete: %d left", len(all))
	}
	if count, _ := store.UnreadCount(ctx, "other"); count != 1 {
		t.Errorf("other recipient: %d unread", count)
	}
}

func TestChannelErrors(t *testing.T) {
	ctx := context.Background()
	fake := mail.NewFake()
	fake.Err = errors.New("smtp down")
	store := &DatabaseChannel{DB: connect(t)}
	n := New(&MailChannel{Mailer: fake, From: "billing@example.com"}, store)

	// A failing channel does not stop the others; errors are joined.
	err := n.Send(ctx, ada, InvoicePaid{InvoiceID: 1, Channels: []string{Mail, "sms", Database}})
	if !errors.Is(err, fake.Err) || !strings.Contains(err.Error(), `no "sms" channel`) {
		t.Errorf("Send = %v", err)
	}
	if list, _ := store.List(ctx, "42", false, 0); len(list) != 1 {
		t.Error("the database channel did not run after the mail channel failed")
	}
	if err := n.Send(ctx, ada, MailOnly{}); err == nil || !strings.Contains(err.Error(), "Storable") {
		t.Errorf("non-storable = %v", err)
	}
	fake.Err = nil
	if err := n.Send(ctx, Recipient{ID: "1"}, InvoicePaid{Channels: []string{Mail}}); err == nil || !strings.Contains(err.Error(), "no email address") {
		t.Errorf("no address = %v", err)
	}
	if err := n.SendAll(ctx, []Recipient{ada, {ID: "2", Email: "bob@example.com"}}, InvoicePaid{Channels: []string{Mail}}); err != nil {
		t.Fatal(err)
	}
	fake.AssertCount(t, 2)
}

func TestQueuedDelivery(t *testing.T) {
	ctx := context.Background()
	db := connect(t)
	fake := mail.NewFake()
	n := New(&MailChannel{Mailer: fake, From: "billing@example.com"}, &DatabaseChannel{DB: db})
	q := queue.New(&queue.SQLStore{DB: db}, queue.WithBackoff(func(int) time.Duration { return 0 }))
	send := Queued[InvoicePaid](n, q, "notify.invoice-paid", queue.Tries(2))

	must(t, send(ctx, ada, InvoicePaid{InvoiceID: 7, Channels: []string{Mail, Database}}))
	fake.AssertNothingSent(t)
	must(t, q.WorkUntilEmpty(ctx))
	fake.AssertSent(t, "ada@example.com")

	fake.Err = errors.New("smtp down")
	must(t, send(ctx, ada, InvoicePaid{InvoiceID: 8, Channels: []string{Mail}}))
	must(t, q.WorkUntilEmpty(ctx))
	failed, err := q.Failed(ctx)
	if err != nil || len(failed) != 1 || !strings.Contains(failed[0].Error, "smtp down") || !strings.Contains(string(failed[0].Payload), `"invoice_id":8`) {
		t.Errorf("failed = %+v, %v", failed, err)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
