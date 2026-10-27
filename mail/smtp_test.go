package mail

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// smtpServer is a minimal SMTP server for tests.
type smtpServer struct {
	listener net.Listener
	tls      *tls.Config
	implicit bool // wrap connections in TLS before the greeting
	startTLS bool // offer STARTTLS
	silent   bool // never send a greeting
	mu       sync.Mutex
	auth     string
	from     string
	to       []string
	data     string
	sawTLS   bool
}

func newCert(t *testing.T) (*tls.Config, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}, pool
}

func startSMTP(t *testing.T, server *smtpServer) (host string, port int) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server.listener = listener
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go server.serve(conn)
		}
	}()
	addr := listener.Addr().(*net.TCPAddr)
	return "127.0.0.1", addr.Port
}

func (s *smtpServer) serve(conn net.Conn) {
	defer conn.Close()
	if s.silent {
		time.Sleep(2 * time.Second)
		return
	}
	secure := false
	if s.implicit {
		conn = tls.Server(conn, s.tls)
		secure = true
	}
	r := bufio.NewReader(conn)
	reply := func(line string) { conn.Write([]byte(line + "\r\n")) }
	reply("220 test ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		verb := strings.ToUpper(strings.SplitN(line, " ", 2)[0])
		switch verb {
		case "EHLO":
			reply("250-test")
			if s.startTLS && !secure {
				reply("250-STARTTLS")
			}
			reply("250 AUTH PLAIN")
		case "STARTTLS":
			reply("220 ready")
			conn = tls.Server(conn, s.tls)
			r = bufio.NewReader(conn)
			secure = true
		case "AUTH":
			decoded, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, "AUTH PLAIN "))
			s.mu.Lock()
			s.auth, s.sawTLS = string(decoded), secure
			s.mu.Unlock()
			reply("235 ok")
		case "MAIL":
			s.mu.Lock()
			s.from = line
			s.mu.Unlock()
			reply("250 ok")
		case "RCPT":
			s.mu.Lock()
			s.to = append(s.to, line)
			s.mu.Unlock()
			reply("250 ok")
		case "DATA":
			reply("354 go")
			var data strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				data.WriteString(l)
			}
			s.mu.Lock()
			s.data, s.sawTLS = data.String(), s.sawTLS || secure
			s.mu.Unlock()
			reply("250 queued")
		case "QUIT":
			reply("221 bye")
			return
		default:
			reply("502 unknown")
		}
	}
}

func TestSMTPStartTLS(t *testing.T) {
	serverTLS, pool := newCert(t)
	server := &smtpServer{tls: serverTLS, startTLS: true}
	host, port := startSMTP(t, server)
	s := &SMTP{Host: host, Port: port, Username: "user", Password: "secret", TLSConfig: &tls.Config{RootCAs: pool}}
	if err := s.Send(context.Background(), valid()); err != nil {
		t.Fatal(err)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.auth != "\x00user\x00secret" || !server.sawTLS {
		t.Errorf("auth = %q over TLS = %v", server.auth, server.sawTLS)
	}
	if server.from != "MAIL FROM:<hello@example.com> BODY=8BITMIME" && server.from != "MAIL FROM:<hello@example.com>" {
		t.Errorf("from = %q", server.from)
	}
	if strings.Join(server.to, ",") != "RCPT TO:<ayse@example.com>,RCPT TO:<cc@example.com>,RCPT TO:<hidden@example.com>" {
		t.Errorf("to = %v", server.to)
	}
	if !strings.Contains(server.data, "Subject: =?utf-8?q?Merhaba_d=C3=BCnya?=") || strings.Contains(server.data, "hidden@example.com") {
		t.Errorf("data = %q", server.data)
	}
}

func TestSMTPRequiresStartTLS(t *testing.T) {
	serverTLS, _ := newCert(t)
	host, port := startSMTP(t, &smtpServer{tls: serverTLS})
	err := (&SMTP{Host: host, Port: port}).Send(context.Background(), valid())
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("error = %v, want a STARTTLS error", err)
	}
}

func TestSMTPRejectsUntrustedCertificate(t *testing.T) {
	serverTLS, _ := newCert(t)
	host, port := startSMTP(t, &smtpServer{tls: serverTLS, startTLS: true})
	if err := (&SMTP{Host: host, Port: port}).Send(context.Background(), valid()); err == nil {
		t.Fatal("sent over a certificate that is not trusted")
	}
}

func TestSMTPImplicitTLSAndNone(t *testing.T) {
	serverTLS, pool := newCert(t)
	server := &smtpServer{tls: serverTLS, implicit: true}
	host, port := startSMTP(t, server)
	if err := (&SMTP{Host: host, Port: port, Encryption: TLS, TLSConfig: &tls.Config{RootCAs: pool}}).Send(context.Background(), valid()); err != nil {
		t.Fatal(err)
	}
	server.mu.Lock()
	if !server.sawTLS {
		t.Error("implicit TLS not used")
	}
	server.mu.Unlock()

	plain := &smtpServer{}
	host, port = startSMTP(t, plain)
	if err := (&SMTP{Host: host, Port: port, Encryption: None}).Send(context.Background(), valid()); err != nil {
		t.Fatal(err)
	}
	plain.mu.Lock()
	defer plain.mu.Unlock()
	if plain.data == "" || plain.auth != "" {
		t.Errorf("plain delivery: data %d bytes, auth %q", len(plain.data), plain.auth)
	}
}

func TestSMTPTimeoutsAndErrors(t *testing.T) {
	host, port := startSMTP(t, &smtpServer{silent: true})
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := (&SMTP{Host: host, Port: port}).Send(ctx, valid()); err == nil {
		t.Fatal("silent server succeeded")
	}
	if time.Since(start) > 3*time.Second {
		t.Error("context deadline was not honoured")
	}
	if err := (&SMTP{Host: host, Port: port, Timeout: 100 * time.Millisecond}).Send(context.Background(), valid()); err == nil {
		t.Fatal("Timeout was not honoured")
	}

	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	closedPort := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	for name, s := range map[string]*SMTP{
		"no host":    {},
		"bad mode":   {Host: "127.0.0.1", Encryption: "ssl"},
		"connection": {Host: "127.0.0.1", Port: closedPort},
	} {
		if err := s.Send(context.Background(), valid()); err == nil {
			t.Errorf("%s: Send succeeded", name)
		}
	}
	if err := (&SMTP{Host: "127.0.0.1", Port: closedPort}).Send(context.Background(), Message{}); err == nil || !strings.Contains(err.Error(), "From") {
		t.Errorf("invalid message error = %v", err)
	}
	if got := (&SMTP{Encryption: TLS}).port(); got != 465 {
		t.Errorf("TLS port = %d", got)
	}
	if got := (&SMTP{}).port(); got != 587 {
		t.Errorf("default port = %d", got)
	}
}
