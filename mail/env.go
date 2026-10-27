package mail

import (
	"fmt"
	"net/mail"
	"os"
	"strconv"
	"strings"
)

// FromEnv builds a mailer from environment variables:
//
//	MAIL_DRIVER        log (default) or smtp
//	MAIL_HOST          SMTP host, required for smtp
//	MAIL_PORT          defaults to 587, or 465 with tls
//	MAIL_USERNAME      optional
//	MAIL_PASSWORD      optional
//	MAIL_ENCRYPTION    starttls (default), tls or none
//	MAIL_FROM_ADDRESS  default sender, required
//	MAIL_FROM_NAME     optional display name for the sender
func FromEnv() (Mailer, error) {
	fromAddress := strings.TrimSpace(os.Getenv("MAIL_FROM_ADDRESS"))
	if fromAddress == "" {
		return nil, fmt.Errorf("mail: MAIL_FROM_ADDRESS is required")
	}
	from := &mail.Address{Name: strings.TrimSpace(os.Getenv("MAIL_FROM_NAME")), Address: fromAddress}
	if _, err := mail.ParseAddress(from.String()); err != nil {
		return nil, fmt.Errorf("mail: invalid MAIL_FROM_ADDRESS %q: %w", fromAddress, err)
	}
	var driver Mailer
	switch name := strings.ToLower(strings.TrimSpace(os.Getenv("MAIL_DRIVER"))); name {
	case "", "log":
		driver = Log{}
	case "smtp":
		s := &SMTP{
			Host:       strings.TrimSpace(os.Getenv("MAIL_HOST")),
			Username:   os.Getenv("MAIL_USERNAME"),
			Password:   os.Getenv("MAIL_PASSWORD"),
			Encryption: strings.ToLower(strings.TrimSpace(os.Getenv("MAIL_ENCRYPTION"))),
		}
		if s.Host == "" {
			return nil, fmt.Errorf("mail: MAIL_HOST is required for the smtp driver")
		}
		if port := strings.TrimSpace(os.Getenv("MAIL_PORT")); port != "" {
			n, err := strconv.Atoi(port)
			if err != nil || n < 1 || n > 65535 {
				return nil, fmt.Errorf("mail: invalid MAIL_PORT %q", port)
			}
			s.Port = n
		}
		switch s.Encryption {
		case "", StartTLS, TLS, None:
		default:
			return nil, fmt.Errorf("mail: invalid MAIL_ENCRYPTION %q (use starttls, tls or none)", s.Encryption)
		}
		driver = s
	default:
		return nil, fmt.Errorf("mail: unknown MAIL_DRIVER %q (use log or smtp)", name)
	}
	return WithFrom(driver, from.String()), nil
}
