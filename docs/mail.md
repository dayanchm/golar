# Mail

`github.com/dayanchm/golar/mail` builds and sends email. A `Mailer` sends a
`Message`; drivers deliver it over SMTP, print it to the log or record it for
tests.

```go
mailer, err := mail.FromEnv() // MAIL_DRIVER, MAIL_HOST, ...
err = mailer.Send(ctx, mail.Message{
    To:      []string{"Ayşe <ayse@example.com>"},
    Subject: "Welcome",
    Text:    "Thanks for signing up.",
    HTML:    "<p>Thanks for signing up.</p>",
})
```

## Messages

| Field | Notes |
| --- | --- |
| `From` | required; `mail.WithFrom` and `FromEnv` fill it when empty |
| `To`, `Cc`, `Bcc` | at least one recipient; Bcc never appears in headers |
| `ReplyTo` | optional |
| `Subject` | required, one line; non-ASCII is encoded |
| `Text`, `HTML` | at least one; both are sent as `multipart/alternative` |
| `Headers` | extra headers such as `List-Unsubscribe`; cannot replace the ones above |

Addresses may include display names. `Validate` rejects unparsable addresses
and any CR or LF in the subject or headers, so user input cannot add headers
or recipients. `Bytes` returns the encoded MIME message.

## Drivers

### SMTP

```go
mailer := &mail.SMTP{
    Host:       "smtp.example.com",
    Port:       587,               // default; 465 with TLS
    Username:   "apikey",
    Password:   os.Getenv("MAIL_PASSWORD"),
    Encryption: mail.StartTLS,     // default; mail.TLS or mail.None
}
```

With `StartTLS` the server must offer STARTTLS and its certificate must be
valid; the connection is never downgraded to plain text. `TLS` connects with
implicit TLS. `None` is for local catchers such as Mailpit; credentials are
then only sent to localhost. `Timeout` (30 seconds) and the context bound the
whole delivery. Each `Send` opens one connection.

### Log

`mail.Log{}` prints the sender, recipients, subject and text body to the
standard logger, so links can be followed in development. Do not use it in
production.

### Fake

```go
fake := mail.NewFake()
// ... run code that sends mail through fake ...
m := fake.AssertSent(t, "ayse@example.com") // latest message to that address
fake.AssertCount(t, 1)
fake.AssertNothingSent(t)
```

`Fake` validates and records messages without a network. `Messages`,
`SentTo` and `Reset` inspect and clear them; set `Err` to make sends fail.

## Configuration

`mail.FromEnv()` reads:

| Variable | Default | Notes |
| --- | --- | --- |
| `MAIL_DRIVER` | `log` | `log` or `smtp` |
| `MAIL_HOST` | | required for `smtp` |
| `MAIL_PORT` | 587, or 465 with `tls` | |
| `MAIL_USERNAME`, `MAIL_PASSWORD` | | optional |
| `MAIL_ENCRYPTION` | `starttls` | `starttls`, `tls` or `none` |
| `MAIL_FROM_ADDRESS` | | required default sender |
| `MAIL_FROM_NAME` | | optional sender name |

Invalid values return an error at startup instead of failing on the first send.

## Templates

```go
var welcome = mail.MustTemplate(
    "Welcome to {{.App}}",
    "Hello {{.Name}}, thanks for joining {{.App}}.",
    "<p>Hello {{.Name}}, thanks for joining {{.App}}.</p>",
)

m, err := welcome.Render(map[string]string{"App": "Demo", "Name": name})
m.To = []string{email}
err = mailer.Send(ctx, m)
```

The subject and text body use `text/template`; the HTML body uses
`html/template`, which escapes data. Templates fail on missing keys. Rendered
subjects are joined onto one line. Build a `mail.Template` from
`template.ParseFS` results to keep templates in files.

## Password reset and verification emails

`auth.PasswordResetMail` and `auth.VerificationMail` turn a mailer into the
`Send` functions of `auth.PasswordBroker` and `auth.EmailVerification`:

```go
broker := &auth.PasswordBroker{
    Provider: users,
    Store:    &auth.SQLResetStore{DB: db},
    Send: auth.PasswordResetMail{
        Mailer:  mailer,
        URL:     "https://app.example.com/reset-password", // ?token=…&email=… is added
        AppName: "Demo",
    }.Send,
}
verification := &auth.EmailVerification{
    Secret:   []byte(os.Getenv("APP_KEY")),
    Provider: users,
    Send: auth.VerificationMail{
        Mailer: mailer,
        URL:    "https://app.example.com/api/auth/verify-email", // ?token=… is added
    }.Send,
}
```

`URL` must be absolute. The default templates are `auth.DefaultResetTemplate`
and `auth.DefaultVerificationTemplate`; set `Template` to use your own, with
the fields of `auth.LinkMail` (`AppName`, `Email`, `Link`). Reset emails need
users with an `AuthEmail() string` method.

## Generated projects

Projects with authentication read `MAIL_*`, `APP_URL` and `APP_KEY` from
`.env`. `golar new` writes a random `APP_KEY` and `MAIL_DRIVER=log`, so reset
and verification links appear in the server log. Set `MAIL_DRIVER=smtp` and
the `MAIL_HOST` variables to deliver them. Tests pass a fake mailer to
`bootstrap.NewWithDB(db, mail.WithFrom(fake, "test@example.com"))` and read
the links from `fake.AssertSent`.
