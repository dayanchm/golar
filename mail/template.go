package mail

import (
	"bytes"
	"errors"
	htmltemplate "html/template"
	"strings"
	texttemplate "text/template"
)

// Template renders a message's subject and bodies from the same data. The
// subject and text body use text/template; the HTML body uses html/template,
// which escapes data.
type Template struct {
	Subject *texttemplate.Template
	Text    *texttemplate.Template
	HTML    *htmltemplate.Template
}

// NewTemplate parses a subject and optional text and HTML bodies. At least one
// body is required.
func NewTemplate(subject, text, html string) (*Template, error) {
	if text == "" && html == "" {
		return nil, errors.New("mail: a text or HTML template is required")
	}
	t := &Template{}
	var err error
	if t.Subject, err = texttemplate.New("subject").Option("missingkey=error").Parse(subject); err != nil {
		return nil, err
	}
	if text != "" {
		if t.Text, err = texttemplate.New("text").Option("missingkey=error").Parse(text); err != nil {
			return nil, err
		}
	}
	if html != "" {
		if t.HTML, err = htmltemplate.New("html").Option("missingkey=error").Parse(html); err != nil {
			return nil, err
		}
	}
	return t, nil
}

// MustTemplate is NewTemplate that panics on error, for package-level templates.
func MustTemplate(subject, text, html string) *Template {
	t, err := NewTemplate(subject, text, html)
	if err != nil {
		panic(err)
	}
	return t
}

// Render returns a message with Subject, Text and HTML filled from data. Set
// its recipients before sending.
func (t *Template) Render(data any) (Message, error) {
	var m Message
	if t.Subject == nil {
		return m, errors.New("mail: template has no subject")
	}
	var buf bytes.Buffer
	if err := t.Subject.Execute(&buf, data); err != nil {
		return m, err
	}
	// A subject is one line; template whitespace and data must not add headers.
	m.Subject = strings.Join(strings.Fields(buf.String()), " ")
	if t.Text != nil {
		buf.Reset()
		if err := t.Text.Execute(&buf, data); err != nil {
			return m, err
		}
		m.Text = buf.String()
	}
	if t.HTML != nil {
		buf.Reset()
		if err := t.HTML.Execute(&buf, data); err != nil {
			return m, err
		}
		m.HTML = buf.String()
	}
	return m, nil
}
