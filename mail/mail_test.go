package mail

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"testing"
)

func valid() Message {
	return Message{
		From:    "Golar <hello@example.com>",
		To:      []string{"Ayşe Yılmaz <ayse@example.com>"},
		Cc:      []string{"cc@example.com"},
		Bcc:     []string{"hidden@example.com"},
		ReplyTo: "support@example.com",
		Subject: "Merhaba dünya",
		Text:    "Line one\nLine two with ünicode and a long line " + strings.Repeat("x", 100),
		HTML:    "<p>Merhaba</p>",
		Headers: map[string]string{"List-Unsubscribe": "<https://example.com/u>"},
	}
}

func TestBytes(t *testing.T) {
	raw, err := valid().Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("hidden@example.com")) {
		t.Error("Bcc leaked into the message")
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil || subject != "Merhaba dünya" {
		t.Errorf("Subject = %q, %v", subject, err)
	}
	to, err := msg.Header.AddressList("To")
	if err != nil || len(to) != 1 || to[0].Name != "Ayşe Yılmaz" || to[0].Address != "ayse@example.com" {
		t.Errorf("To = %v, %v", to, err)
	}
	for _, name := range []string{"From", "Cc", "Reply-To", "Date", "Message-Id", "List-Unsubscribe"} {
		if msg.Header.Get(name) == "" {
			t.Errorf("missing %s header", name)
		}
	}
	if !strings.HasSuffix(msg.Header.Get("Message-Id"), "@example.com>") {
		t.Errorf("Message-Id = %q", msg.Header.Get("Message-Id"))
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/alternative" {
		t.Fatalf("Content-Type = %q, %v", msg.Header.Get("Content-Type"), err)
	}
	reader := multipart.NewReader(msg.Body, params["boundary"])
	var bodies []string
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		// multipart.Reader decodes quoted-printable parts itself.
		body, _ := io.ReadAll(part)
		bodies = append(bodies, part.Header.Get("Content-Type")+"|"+string(body))
	}
	want := []string{
		"text/plain; charset=utf-8|" + strings.ReplaceAll(valid().Text, "\n", "\r\n"),
		"text/html; charset=utf-8|<p>Merhaba</p>",
	}
	if strings.Join(bodies, "\n--\n") != strings.Join(want, "\n--\n") {
		t.Errorf("parts = %q", bodies)
	}
}

func TestBytesSingleBody(t *testing.T) {
	for _, tt := range []struct{ text, html, contentType string }{
		{"plain text", "", "text/plain; charset=utf-8"},
		{"", "<b>bold</b>", "text/html; charset=utf-8"},
	} {
		m := valid()
		m.Text, m.HTML = tt.text, tt.html
		raw, err := m.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		msg, _ := mail.ReadMessage(bytes.NewReader(raw))
		if msg.Header.Get("Content-Type") != tt.contentType || msg.Header.Get("Content-Transfer-Encoding") != "quoted-printable" {
			t.Errorf("headers = %v", msg.Header)
		}
		body, _ := io.ReadAll(quotedprintable.NewReader(msg.Body))
		if string(body) != tt.text+tt.html {
			t.Errorf("body = %q", body)
		}
	}
}

func TestValidate(t *testing.T) {
	for name, change := range map[string]func(*Message){
		"no from":             func(m *Message) { m.From = "" },
		"bad from":            func(m *Message) { m.From = "not an address" },
		"no recipients":       func(m *Message) { m.To, m.Cc, m.Bcc = nil, nil, nil },
		"bad recipient":       func(m *Message) { m.To = []string{"a@b.c", "nope"} },
		"bad reply-to":        func(m *Message) { m.ReplyTo = "nope" },
		"no subject":          func(m *Message) { m.Subject = " " },
		"subject injection":   func(m *Message) { m.Subject = "Hi\r\nBcc: victim@example.com" },
		"no body":             func(m *Message) { m.Text, m.HTML = "", "" },
		"reserved header":     func(m *Message) { m.Headers = map[string]string{"bcc": "x@example.com"} },
		"bad header name":     func(m *Message) { m.Headers = map[string]string{"X Bad": "1"} },
		"header injection":    func(m *Message) { m.Headers = map[string]string{"X-Tag": "a\nBcc: x@example.com"} },
		"recipient injection": func(m *Message) { m.To = []string{"a@example.com\r\nBcc: x@example.com"} },
	} {
		m := valid()
		change(&m)
		if err := m.Validate(); err == nil {
			t.Errorf("%s: Validate succeeded", name)
		}
		if _, err := m.Bytes(); err == nil {
			t.Errorf("%s: Bytes succeeded", name)
		}
	}
	if err := valid().Validate(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(valid().Recipients(), ","); got != "ayse@example.com,cc@example.com,hidden@example.com" {
		t.Errorf("Recipients = %s", got)
	}
}

type recordingTB struct{ errors []string }

func (r *recordingTB) Helper() {}
func (r *recordingTB) Errorf(format string, args ...any) {
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}

func TestFake(t *testing.T) {
	fake := NewFake()
	ctx := context.Background()
	tb := &recordingTB{}
	fake.AssertNothingSent(tb)
	if len(tb.errors) != 0 {
		t.Fatal(tb.errors)
	}
	if err := fake.Send(ctx, Message{}); err == nil {
		t.Fatal("Fake accepted an invalid message")
	}
	first, second := valid(), valid()
	second.Subject = "Second"
	fake.Send(ctx, first)
	fake.Send(ctx, second)
	if got := fake.AssertSent(tb, "AYSE@example.com"); got.Subject != "Second" {
		t.Errorf("AssertSent = %q, want the latest message", got.Subject)
	}
	if len(fake.SentTo("Hidden <hidden@example.com>")) != 2 || len(fake.SentTo("nobody@example.com")) != 0 {
		t.Error("SentTo matched the wrong messages")
	}
	fake.AssertCount(tb, 2)
	if len(tb.errors) != 0 {
		t.Fatal(tb.errors)
	}
	fake.AssertSent(tb, "nobody@example.com")
	fake.AssertCount(tb, 3)
	fake.AssertNothingSent(tb)
	if len(tb.errors) != 3 {
		t.Errorf("failed assertions reported %v", tb.errors)
	}
	fake.Reset()
	if len(fake.Messages()) != 0 {
		t.Error("Reset kept messages")
	}
	fake.Err = errors.New("down")
	if err := fake.Send(ctx, valid()); !errors.Is(err, fake.Err) || len(fake.Messages()) != 0 {
		t.Errorf("Send with Err = %v", err)
	}
}

func TestLogAndWithFrom(t *testing.T) {
	var buf bytes.Buffer
	m := valid()
	m.From = ""
	mailer := WithFrom(Log{Logger: log.New(&buf, "", 0)}, "App <app@example.com>")
	if err := mailer.Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{`from="App <app@example.com>"`, "ayse@example.com", `subject="Merhaba dünya"`, "Line two"} {
		if !strings.Contains(out, want) {
			t.Errorf("log %q lacks %q", out, want)
		}
	}
	fake := NewFake()
	m.From = "Other <other@example.com>"
	WithFrom(fake, "app@example.com").Send(context.Background(), m)
	if fake.Messages()[0].From != m.From {
		t.Error("WithFrom replaced an explicit From")
	}
	if err := (Log{}).Send(context.Background(), Message{}); err == nil {
		t.Error("Log accepted an invalid message")
	}
}

func TestTemplate(t *testing.T) {
	tmpl := MustTemplate("Hi {{.Name}}\n", "Hello {{.Name}}", `<p>Hello {{.Name}}</p>`)
	m, err := tmpl.Render(map[string]string{"Name": "<Ada>\r\nBcc: x@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Subject != "Hi <Ada> Bcc: x@example.com" || m.Text != "Hello <Ada>\r\nBcc: x@example.com" || m.HTML != "<p>Hello &lt;Ada&gt;\r\nBcc: x@example.com</p>" {
		t.Errorf("rendered %+v", m)
	}
	if _, err := tmpl.Render(map[string]string{}); err == nil {
		t.Error("missing key rendered")
	}
	if _, err := NewTemplate("s", "", ""); err == nil {
		t.Error("template without body accepted")
	}
	if _, err := NewTemplate("{{", "x", ""); err == nil {
		t.Error("invalid subject accepted")
	}
	textOnly, _ := NewTemplate("s", "t", "")
	if m, _ := textOnly.Render(nil); m.HTML != "" || m.Text != "t" {
		t.Errorf("text-only = %+v", m)
	}
}

func TestFromEnv(t *testing.T) {
	set := func(values map[string]string) {
		for _, key := range []string{"MAIL_DRIVER", "MAIL_HOST", "MAIL_PORT", "MAIL_USERNAME", "MAIL_PASSWORD", "MAIL_ENCRYPTION", "MAIL_FROM_ADDRESS", "MAIL_FROM_NAME"} {
			t.Setenv(key, values[key])
		}
	}
	set(map[string]string{"MAIL_FROM_ADDRESS": "hello@example.com", "MAIL_FROM_NAME": "Demo App"})
	m, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if from, ok := m.(defaultFrom); !ok || from.from != `"Demo App" <hello@example.com>` {
		t.Errorf("mailer = %#v", m)
	} else if _, ok := from.Mailer.(Log); !ok {
		t.Errorf("default driver = %T", from.Mailer)
	}

	set(map[string]string{"MAIL_DRIVER": "SMTP", "MAIL_HOST": "smtp.example.com", "MAIL_PORT": "2525", "MAIL_USERNAME": "u", "MAIL_PASSWORD": "p", "MAIL_ENCRYPTION": "tls", "MAIL_FROM_ADDRESS": "hello@example.com"})
	m, err = FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	s, ok := m.(defaultFrom).Mailer.(*SMTP)
	if !ok || s.Host != "smtp.example.com" || s.Port != 2525 || s.Username != "u" || s.Password != "p" || s.Encryption != TLS {
		t.Errorf("smtp = %#v", m.(defaultFrom).Mailer)
	}

	for name, values := range map[string]map[string]string{
		"no from":        {},
		"bad from":       {"MAIL_FROM_ADDRESS": "nope"},
		"unknown driver": {"MAIL_DRIVER": "sendmail", "MAIL_FROM_ADDRESS": "a@example.com"},
		"no host":        {"MAIL_DRIVER": "smtp", "MAIL_FROM_ADDRESS": "a@example.com"},
		"bad port":       {"MAIL_DRIVER": "smtp", "MAIL_HOST": "h", "MAIL_PORT": "70000", "MAIL_FROM_ADDRESS": "a@example.com"},
		"bad encryption": {"MAIL_DRIVER": "smtp", "MAIL_HOST": "h", "MAIL_ENCRYPTION": "ssl3", "MAIL_FROM_ADDRESS": "a@example.com"},
	} {
		set(values)
		if _, err := FromEnv(); err == nil {
			t.Errorf("%s: FromEnv succeeded", name)
		}
	}
}
