// Package mail builds and sends email through pluggable drivers: SMTP for
// delivery, Log for development and Fake for tests.
package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"sort"
	"strings"
	"time"
)

// Mailer sends messages. Implementations must be safe for concurrent use.
type Mailer interface {
	Send(ctx context.Context, m Message) error
}

// Message is an email. Addresses may be plain ("ada@example.com") or include
// a display name ("Ada <ada@example.com>").
type Message struct {
	From        string
	To, Cc, Bcc []string
	ReplyTo     string
	Subject     string
	Text, HTML  string
	// Headers adds extra headers, such as List-Unsubscribe. They cannot
	// replace the headers the message sets itself.
	Headers map[string]string
}

var reservedHeaders = map[string]bool{
	"From": true, "To": true, "Cc": true, "Bcc": true, "Reply-To": true, "Subject": true, "Date": true,
	"Message-Id": true, "Mime-Version": true, "Content-Type": true, "Content-Transfer-Encoding": true,
}

// Validate reports a message that cannot be sent: missing sender, recipients,
// subject or body, unparsable addresses or unsafe headers.
func (m Message) Validate() error {
	if m.From == "" {
		return errors.New("mail: From is required")
	}
	if _, err := mail.ParseAddress(m.From); err != nil {
		return fmt.Errorf("mail: invalid From %q: %w", m.From, err)
	}
	if len(m.To)+len(m.Cc)+len(m.Bcc) == 0 {
		return errors.New("mail: at least one recipient is required")
	}
	for _, list := range [][]string{m.To, m.Cc, m.Bcc} {
		for _, address := range list {
			if _, err := mail.ParseAddress(address); err != nil {
				return fmt.Errorf("mail: invalid recipient %q: %w", address, err)
			}
		}
	}
	if m.ReplyTo != "" {
		if _, err := mail.ParseAddress(m.ReplyTo); err != nil {
			return fmt.Errorf("mail: invalid Reply-To %q: %w", m.ReplyTo, err)
		}
	}
	if strings.TrimSpace(m.Subject) == "" {
		return errors.New("mail: Subject is required")
	}
	if strings.ContainsAny(m.Subject, "\r\n") {
		return errors.New("mail: Subject must be a single line")
	}
	if m.Text == "" && m.HTML == "" {
		return errors.New("mail: a Text or HTML body is required")
	}
	for name, value := range m.Headers {
		canonical := textproto.CanonicalMIMEHeaderKey(name)
		if name == "" || strings.ContainsAny(name, " :\r\n\t") || reservedHeaders[canonical] {
			return fmt.Errorf("mail: header %q is not allowed", name)
		}
		if strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("mail: header %q must be a single line", name)
		}
	}
	return nil
}

// Recipients returns the bare addresses of To, Cc and Bcc, for the SMTP envelope.
func (m Message) Recipients() []string {
	var out []string
	for _, list := range [][]string{m.To, m.Cc, m.Bcc} {
		for _, value := range list {
			if address, err := mail.ParseAddress(value); err == nil {
				out = append(out, address.Address)
			}
		}
	}
	return out
}

// Bytes encodes the message as MIME. Bcc recipients are not included.
func (m Message) Bytes() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	from, _ := mail.ParseAddress(m.From)
	var buf bytes.Buffer
	header := func(name, value string) { fmt.Fprintf(&buf, "%s: %s\r\n", name, value) }
	header("From", from.String())
	if len(m.To) > 0 {
		header("To", formatList(m.To))
	}
	if len(m.Cc) > 0 {
		header("Cc", formatList(m.Cc))
	}
	if m.ReplyTo != "" {
		replyTo, _ := mail.ParseAddress(m.ReplyTo)
		header("Reply-To", replyTo.String())
	}
	header("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	header("Date", time.Now().Format(time.RFC1123Z))
	header("Message-ID", messageID(from.Address))
	header("MIME-Version", "1.0")
	names := make([]string, 0, len(m.Headers))
	for name := range m.Headers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		header(textproto.CanonicalMIMEHeaderKey(name), mime.QEncoding.Encode("utf-8", m.Headers[name]))
	}

	if m.Text == "" || m.HTML == "" {
		contentType, body := "text/plain; charset=utf-8", m.Text
		if m.Text == "" {
			contentType, body = "text/html; charset=utf-8", m.HTML
		}
		header("Content-Type", contentType)
		header("Content-Transfer-Encoding", "quoted-printable")
		buf.WriteString("\r\n")
		if err := writeQP(&buf, body); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	parts := multipart.NewWriter(&buf)
	header("Content-Type", "multipart/alternative; boundary="+parts.Boundary())
	buf.WriteString("\r\n")
	for _, part := range []struct{ contentType, body string }{
		{"text/plain; charset=utf-8", m.Text},
		{"text/html; charset=utf-8", m.HTML},
	} {
		w, err := parts.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {part.contentType},
			"Content-Transfer-Encoding": {"quoted-printable"},
		})
		if err != nil {
			return nil, err
		}
		if err := writeQP(w, part.body); err != nil {
			return nil, err
		}
	}
	if err := parts.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func formatList(values []string) string {
	formatted := make([]string, len(values))
	for i, value := range values {
		address, _ := mail.ParseAddress(value)
		formatted[i] = address.String()
	}
	return strings.Join(formatted, ", ")
}

func writeQP(w interface{ Write([]byte) (int, error) }, body string) error {
	qp := quotedprintable.NewWriter(w)
	if _, err := qp.Write([]byte(strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n"))); err != nil {
		return err
	}
	return qp.Close()
}

func messageID(from string) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	domain := "localhost"
	if _, host, ok := strings.Cut(from, "@"); ok && host != "" {
		domain = host
	}
	return "<" + hex.EncodeToString(b) + "@" + domain + ">"
}

// WithFrom returns a Mailer that fills From with from when a message leaves it empty.
func WithFrom(m Mailer, from string) Mailer {
	return defaultFrom{Mailer: m, from: from}
}

type defaultFrom struct {
	Mailer
	from string
}

func (d defaultFrom) Send(ctx context.Context, m Message) error {
	if m.From == "" {
		m.From = d.from
	}
	return d.Mailer.Send(ctx, m)
}
