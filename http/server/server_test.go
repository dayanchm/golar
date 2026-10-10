package server

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestConfig(t *testing.T) {
	s := New(http.NotFoundHandler(), Config{})
	if s.http.Addr != ":8080" || s.http.ReadTimeout != 15*time.Second || s.http.ReadHeaderTimeout != 5*time.Second || s.http.WriteTimeout != 30*time.Second || s.http.IdleTimeout != 60*time.Second || s.shutdownTimeout != 10*time.Second {
		t.Fatal("unexpected defaults")
	}
	config := Config{Addr: "127.0.0.1:9000", ReadTimeout: time.Second, ReadHeaderTimeout: 2 * time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: 4 * time.Second, ShutdownTimeout: 5 * time.Second}
	s = New(http.NotFoundHandler(), config)
	if s.http.Addr != config.Addr || s.http.ReadTimeout != config.ReadTimeout || s.http.ReadHeaderTimeout != config.ReadHeaderTimeout || s.http.WriteTimeout != config.WriteTimeout || s.http.IdleTimeout != config.IdleTimeout || s.shutdownTimeout != config.ShutdownTimeout {
		t.Fatal("config was not applied")
	}
}

func listen(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return listener
}

func await(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("server did not finish")
		return nil
	}
}

func TestServeHTTP(t *testing.T) {
	listener := listen(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(201); _, _ = io.WriteString(w, "Golar") }), Config{})
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, listener) }()
	t.Cleanup(func() { _ = s.http.Close() })
	client := &http.Client{Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()
	resp, err := client.Get("http://" + listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || resp.StatusCode != 201 || string(body) != "Golar" {
		t.Fatalf("response = %d %q, err=%v", resp.StatusCode, body, err)
	}
	cancel()
	if err := await(t, done); err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err == nil {
		_ = conn.Close()
		t.Fatal("listener still accepting connections")
	}
}

func TestGracefulShutdown(t *testing.T) {
	listener := listen(t)
	started, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		_, _ = io.WriteString(w, "finished")
	}), Config{ShutdownTimeout: 3 * time.Second})
	t.Cleanup(func() { _ = s.http.Close() })
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, listener) }()
	requestDone := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 4 * time.Second}
		defer client.CloseIdleConnections()
		resp, err := client.Get("http://" + listener.Addr().String())
		if err == nil {
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			err = readErr
			if string(body) != "finished" {
				err = errors.New("active request did not finish")
			}
		}
		requestDone <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not start")
	}
	cancel()
	select {
	case err := <-done:
		t.Fatalf("shutdown returned before active request finished: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	release <- struct{}{}
	if err := await(t, requestDone); err != nil {
		t.Fatal(err)
	}
	if err := await(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestShutdownTimeout(t *testing.T) {
	listener := listen(t)
	started := make(chan struct{})
	handlerDone := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(handlerDone)
	}), Config{ShutdownTimeout: 30 * time.Millisecond})
	t.Cleanup(func() { _ = s.http.Close() })
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, listener) }()
	requestDone := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 3 * time.Second}
		defer client.CloseIdleConnections()
		resp, err := client.Get("http://" + listener.Addr().String())
		if resp != nil {
			_ = resp.Body.Close()
		}
		requestDone <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not start")
	}
	cancel()
	if err := await(t, done); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want DeadlineExceeded", err)
	}
	select {
	case <-handlerDone:
	case <-time.After(3 * time.Second):
		t.Fatal("connection was not force-closed")
	}
	if err := await(t, requestDone); err == nil {
		t.Fatal("expected request connection to close")
	}
}

func TestRunListenError(t *testing.T) {
	listener := listen(t)
	s := New(http.NotFoundHandler(), Config{Addr: listener.Addr().String()})
	if err := s.Run(context.Background()); err == nil {
		t.Fatal("expected address-in-use error")
	}
}

func TestCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := New(http.NotFoundHandler(), Config{})
	if err := s.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Serve(ctx, listen(t)); err != nil {
		t.Fatal(err)
	}
}
