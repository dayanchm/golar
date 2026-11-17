# Notifications

`github.com/dayanchm/golar/notify` sends one notification to a user over
several channels: an email, an entry in an in-app list, or channels of your
own.

```go
type InvoicePaid struct {
    InvoiceID int64 `json:"invoice_id"`
}

// Via picks the channels, per recipient if needed.
func (InvoicePaid) Via(notify.Recipient) []string {
    return []string{notify.Mail, notify.Database}
}

func (n InvoicePaid) ToMail(r notify.Recipient) mail.Message {
    return mail.Message{
        Subject: "Invoice paid",
        Text:    fmt.Sprintf("Hello %s, invoice %d is paid.", r.Name, n.InvoiceID),
    }
}

func (n InvoicePaid) ToDatabase(notify.Recipient) any {
    return map[string]any{"invoice_id": n.InvoiceID}
}

notifier := notify.New(
    &notify.MailChannel{Mailer: mailer, From: "billing@example.com"},
    &notify.DatabaseChannel{DB: db},
)

to := notify.Recipient{ID: strconv.FormatInt(user.ID, 10), Email: user.Email, Name: user.Name}
err := notifier.Send(ctx, to, InvoicePaid{InvoiceID: 7})
```

`Send` tries every channel the notification names and returns their errors
joined, so a failing mail server does not stop the database entry. A channel
that is not registered is an error. `SendAll` sends to several recipients.

## Mail

`MailChannel` sends notifications that implement `ToMail`. The message goes
to the recipient's `Name <Email>` unless it sets `To`, and from
`MailChannel.From` unless it sets `From`. Any `mail.Mailer` works, so tests
use the fake:

```go
fake := mail.NewFake()
notifier := notify.New(&notify.MailChannel{Mailer: fake, From: "app@example.com"})
// ...
fake.AssertSent(t, "ada@example.com")
```

## Database

`DatabaseChannel` stores notifications that implement `ToDatabase`, whose
result is encoded as JSON. Create the table in a migration with
`s.Create(notify.Table, notify.Columns)`.

```go
store := &notify.DatabaseChannel{DB: db}

list, err := store.List(ctx, userID, false, 20)   // newest first; true for unread only
count, err := store.UnreadCount(ctx, userID)
err = store.MarkRead(ctx, userID, list[0].ID)
err = store.MarkAllRead(ctx, userID)
err = store.Delete(ctx, userID, list[0].ID)
```

Each `notify.Stored` has `ID`, `Type`, `Data` (raw JSON), `ReadAt` (zero while
unread) and `CreatedAt`. `Type` is the Go type name, such as
`billing.InvoicePaid`; implement `NotificationType() string` to choose a
stable name instead. `MarkRead` and `Delete` take the recipient's ID as well,
so pass the signed-in user's ID and one user cannot change another's
notifications.

## Queued delivery

```go
sendInvoicePaid := notify.Queued[InvoicePaid](notifier, q, "notify.invoice-paid", queue.Tries(5))

err := sendInvoicePaid(ctx, to, InvoicePaid{InvoiceID: 7}) // returns once queued
```

`Queued` registers a [queue](queue.md) job for the notification type and
returns a function that queues one. A worker sends it with the queue's
retries; one that keeps failing ends in failed job storage. The notification
must be serializable with `encoding/json`, and the worker must call `Queued`
with the same name too. Note that retries resend on every channel, so a
notification whose mail failed after the database entry was written is
stored again; send to the channels separately if that matters.

## Custom channels

Implement `notify.Channel`:

```go
type SlackChannel struct{ Webhook string }

func (*SlackChannel) Name() string { return "slack" }

func (c *SlackChannel) Send(ctx context.Context, to notify.Recipient, n notify.Notification) error {
    message, ok := n.(interface{ ToSlack(notify.Recipient) string })
    if !ok {
        return fmt.Errorf("%T cannot be sent to Slack", n)
    }
    return postJSON(ctx, c.Webhook, map[string]string{"text": message.ToSlack(to)})
}

notifier.Register(&SlackChannel{Webhook: os.Getenv("SLACK_WEBHOOK")})
```
