package auth

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/dayanchm/golar/mail"
)

// LinkMail is the data passed to password reset and verification templates.
type LinkMail struct {
	AppName string
	Email   string
	Link    string
}

// Default templates for PasswordResetMail and VerificationMail.
var (
	DefaultResetTemplate = mail.MustTemplate(
		"Reset your {{.AppName}} password",
		`Hello,

Someone asked to reset the password for {{.Email}} on {{.AppName}}.
Open this link to choose a new password:

{{.Link}}

If you did not ask for this, ignore this email; your password stays the same.
`,
		`<p>Hello,</p>
<p>Someone asked to reset the password for {{.Email}} on {{.AppName}}.</p>
<p><a href="{{.Link}}">Choose a new password</a></p>
<p>If you did not ask for this, ignore this email; your password stays the same.</p>
`)
	DefaultVerificationTemplate = mail.MustTemplate(
		"Verify your {{.AppName}} email address",
		`Hello,

Confirm that {{.Email}} is your email address on {{.AppName}}:

{{.Link}}

If you did not create an account, ignore this email.
`,
		`<p>Hello,</p>
<p>Confirm that {{.Email}} is your email address on {{.AppName}}.</p>
<p><a href="{{.Link}}">Verify email address</a></p>
<p>If you did not create an account, ignore this email.</p>
`)
)

// PasswordResetMail emails reset links. Use its Send as PasswordBroker.Send.
// Users must have an AuthEmail() string method, as MustVerifyEmail users do.
type PasswordResetMail struct {
	Mailer mail.Mailer
	// URL is the page that asks for a new password; token and email are
	// added as query parameters.
	URL     string
	AppName string
	// Template defaults to DefaultResetTemplate.
	Template *mail.Template
}

func (p PasswordResetMail) Send(ctx context.Context, user Authenticatable, token string) error {
	emailer, ok := user.(interface{ AuthEmail() string })
	if !ok {
		return fmt.Errorf("auth: %T has no AuthEmail method", user)
	}
	email := emailer.AuthEmail()
	link, err := withQuery(p.URL, url.Values{"token": {token}, "email": {email}})
	if err != nil {
		return err
	}
	return sendLink(ctx, p.Mailer, p.Template, DefaultResetTemplate, LinkMail{AppName: p.AppName, Email: email, Link: link})
}

// VerificationMail emails verification links. Use its Send as
// EmailVerification.Send.
type VerificationMail struct {
	Mailer mail.Mailer
	// URL is the endpoint that verifies the address; token is added as a
	// query parameter.
	URL     string
	AppName string
	// Template defaults to DefaultVerificationTemplate.
	Template *mail.Template
}

func (v VerificationMail) Send(ctx context.Context, user MustVerifyEmail, token string) error {
	link, err := withQuery(v.URL, url.Values{"token": {token}})
	if err != nil {
		return err
	}
	return sendLink(ctx, v.Mailer, v.Template, DefaultVerificationTemplate, LinkMail{AppName: v.AppName, Email: user.AuthEmail(), Link: link})
}

func withQuery(raw string, values url.Values) (string, error) {
	if raw == "" {
		return "", errors.New("auth: mail link URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("auth: mail link URL %q must be absolute", raw)
	}
	query := u.Query()
	for key, list := range values {
		query[key] = list
	}
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func sendLink(ctx context.Context, mailer mail.Mailer, t, fallback *mail.Template, data LinkMail) error {
	if mailer == nil {
		return errors.New("auth: Mailer is required")
	}
	if t == nil {
		t = fallback
	}
	if data.AppName == "" {
		data.AppName = "Golar"
	}
	m, err := t.Render(data)
	if err != nil {
		return err
	}
	m.To = []string{data.Email}
	return mailer.Send(ctx, m)
}
