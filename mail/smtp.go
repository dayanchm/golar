package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"time"
)

// Encryption values for SMTP.
const (
	StartTLS = "starttls" // upgrade a plain connection; required by default
	TLS      = "tls"      // implicit TLS, usually port 465
	None     = "none"     // no encryption, for local catchers such as Mailpit
)

// SMTP delivers mail through an SMTP server.
type SMTP struct {
	Host string
	// Port defaults to 587, or 465 with TLS.
	Port               int
	Username, Password string
	// Encryption is StartTLS (the default), TLS or None. With StartTLS the
	// server must offer STARTTLS; the connection is never downgraded.
	Encryption string
	// LocalName is sent in EHLO; it defaults to "localhost".
	LocalName string
	// Timeout bounds the whole delivery; it defaults to 30 seconds.
	Timeout time.Duration
	// TLSConfig overrides the TLS settings; ServerName defaults to Host.
	TLSConfig *tls.Config
}

func (s *SMTP) encryption() string {
	if s.Encryption == "" {
		return StartTLS
	}
	return s.Encryption
}

func (s *SMTP) port() int {
	if s.Port != 0 {
		return s.Port
	}
	if s.encryption() == TLS {
		return 465
	}
	return 587
}

func (s *SMTP) tlsConfig() *tls.Config {
	config := &tls.Config{MinVersion: tls.VersionTLS12}
	if s.TLSConfig != nil {
		config = s.TLSConfig.Clone()
	}
	if config.ServerName == "" {
		config.ServerName = s.Host
	}
	return config
}

// Send delivers m. Credentials are only sent over TLS, or to localhost.
func (s *SMTP) Send(ctx context.Context, m Message) error {
	if s.Host == "" {
		return errors.New("mail: SMTP Host is required")
	}
	encryption := s.encryption()
	if encryption != StartTLS && encryption != TLS && encryption != None {
		return fmt.Errorf("mail: unknown SMTP encryption %q", encryption)
	}
	body, err := m.Bytes()
	if err != nil {
		return err
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	address := net.JoinHostPort(s.Host, strconv.Itoa(s.port()))
	var conn net.Conn
	if encryption == TLS {
		conn, err = (&tls.Dialer{Config: s.tlsConfig()}).DialContext(ctx, "tcp", address)
	} else {
		conn, err = (&net.Dialer{}).DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return fmt.Errorf("mail: connect to %s: %w", address, err)
	}
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

	err = s.deliver(conn, m, body)
	if ctxErr := ctx.Err(); ctxErr != nil && err != nil {
		return fmt.Errorf("mail: %w", ctxErr)
	}
	return err
}

func (s *SMTP) deliver(conn net.Conn, m Message, body []byte) error {
	client, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("mail: %w", err)
	}
	defer client.Close()
	localName := s.LocalName
	if localName == "" {
		localName = "localhost"
	}
	if err := client.Hello(localName); err != nil {
		return fmt.Errorf("mail: EHLO: %w", err)
	}
	if s.encryption() == StartTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("mail: server does not support STARTTLS; set Encryption to None only for local servers")
		}
		if err := client.StartTLS(s.tlsConfig()); err != nil {
			return fmt.Errorf("mail: STARTTLS: %w", err)
		}
	}
	if s.Username != "" {
		if ok, _ := client.Extension("AUTH"); !ok {
			return errors.New("mail: server does not support AUTH")
		}
		if err := client.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); err != nil {
			return fmt.Errorf("mail: AUTH: %w", err)
		}
	}
	from, _ := mail.ParseAddress(m.From)
	if err := client.Mail(from.Address); err != nil {
		return fmt.Errorf("mail: MAIL FROM: %w", err)
	}
	for _, recipient := range m.Recipients() {
		if err := client.Rcpt(recipient); err != nil {
			return fmt.Errorf("mail: RCPT TO %s: %w", recipient, err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("mail: DATA: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("mail: DATA: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mail: DATA: %w", err)
	}
	return client.Quit()
}
