package auth_test

import (
	"context"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/dayanchm/golar/auth"
	"github.com/dayanchm/golar/mail"
)

var linkPattern = regexp.MustCompile(`https?://\S+`)

// link returns the only URL in a message's text body.
func link(t *testing.T, m mail.Message) *url.URL {
	t.Helper()
	found := linkPattern.FindAllString(m.Text, -1)
	if len(found) != 1 {
		t.Fatalf("text body links = %q", found)
	}
	u, err := url.Parse(found[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.HTML, strings.ReplaceAll(found[0], "&", "&amp;")) {
		t.Errorf("HTML body lacks the link: %s", m.HTML)
	}
	return u
}

func TestPasswordResetMail(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	fake := mail.NewFake()
	broker := &auth.PasswordBroker{
		Provider: e.provider,
		Store:    auth.NewMemoryResetStore(),
		Send: auth.PasswordResetMail{
			Mailer:  mail.WithFrom(fake, "Demo <hello@example.com>"),
			URL:     "https://app.example.com/reset-password?lang=tr",
			AppName: "Demo",
		}.Send,
	}
	if err := broker.SendResetLink(ctx, "ada@example.com"); err != nil {
		t.Fatal(err)
	}
	m := fake.AssertSent(t, "ada@example.com")
	if m.Subject != "Reset your Demo password" || m.From != "Demo <hello@example.com>" {
		t.Errorf("message = %+v", m)
	}
	u := link(t, m)
	query := u.Query()
	if u.Host != "app.example.com" || u.Path != "/reset-password" || query.Get("lang") != "tr" || query.Get("email") != "ada@example.com" {
		t.Errorf("link = %s", u)
	}
	if err := broker.Reset(ctx, query.Get("email"), query.Get("token"), "brand new password"); err != nil {
		t.Fatalf("reset with the mailed token: %v", err)
	}
}

func TestVerificationMail(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	fake := mail.NewFake()
	custom := mail.MustTemplate("Welcome to {{.AppName}}", "Verify: {{.Link}}", `<a href="{{.Link}}">Verify</a>`)
	v := &auth.EmailVerification{
		Secret:   []byte(strings.Repeat("s", 32)),
		Provider: e.provider,
		Send: auth.VerificationMail{
			Mailer:   mail.WithFrom(fake, "hello@example.com"),
			URL:      "https://api.example.com/auth/verify-email",
			Template: custom,
		}.Send,
	}
	if err := v.SendVerification(ctx, e.ada); err != nil {
		t.Fatal(err)
	}
	m := fake.AssertSent(t, "ada@example.com")
	if m.Subject != "Welcome to Golar" {
		t.Errorf("subject = %q, want the custom template with the default app name", m.Subject)
	}
	if _, err := v.Verify(ctx, link(t, m).Query().Get("token")); err != nil {
		t.Fatalf("verify with the mailed token: %v", err)
	}
}

type idOnly struct{}

func (idOnly) AuthID() string       { return "1" }
func (idOnly) AuthPassword() string { return "" }

func TestMailAdapterErrors(t *testing.T) {
	ctx := context.Background()
	fake := mail.NewFake()
	user := &User{Email: "ada@example.com"}
	for name, err := range map[string]error{
		"no email method": auth.PasswordResetMail{Mailer: fake, URL: "https://a.example"}.Send(ctx, idOnly{}, "t"),
		"no URL":          auth.PasswordResetMail{Mailer: fake}.Send(ctx, user, "t"),
		"relative URL":    auth.VerificationMail{Mailer: fake, URL: "/verify"}.Send(ctx, user, "t"),
		"no mailer":       auth.VerificationMail{URL: "https://a.example"}.Send(ctx, user, "t"),
		"no sender":       auth.VerificationMail{Mailer: fake, URL: "https://a.example"}.Send(ctx, user, "t"),
	} {
		if err == nil {
			t.Errorf("%s: Send succeeded", name)
		}
	}
	fake.AssertNothingSent(t)
}
