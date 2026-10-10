// Package server runs HTTP handlers with timeouts and graceful shutdown.
package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

// Config configures a server. Zero values use the defaults below.
type Config struct {
	Addr              string
	ReadTimeout       time.Duration
	ReadHeaderTimeout time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
}

type Server struct {
	http            *http.Server
	shutdownTimeout time.Duration
}

func New(handler http.Handler, config Config) *Server {
	if config.Addr == "" {
		config.Addr = ":8080"
	}
	if config.ReadTimeout == 0 {
		config.ReadTimeout = 15 * time.Second
	}
	if config.ReadHeaderTimeout == 0 {
		config.ReadHeaderTimeout = 5 * time.Second
	}
	if config.WriteTimeout == 0 {
		config.WriteTimeout = 30 * time.Second
	}
	if config.IdleTimeout == 0 {
		config.IdleTimeout = 60 * time.Second
	}
	if config.ShutdownTimeout <= 0 {
		config.ShutdownTimeout = 10 * time.Second
	}
	return &Server{
		http: &http.Server{Addr: config.Addr, Handler: handler,
			ReadTimeout: config.ReadTimeout, ReadHeaderTimeout: config.ReadHeaderTimeout,
			WriteTimeout: config.WriteTimeout, IdleTimeout: config.IdleTimeout},
		shutdownTimeout: config.ShutdownTimeout,
	}
}

// Serve takes ownership of listener. Cancellation stops accepting requests and
// waits for active requests; exceeding ShutdownTimeout force-closes connections.
// A Server is intended for one serving lifecycle.
func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	if ctx.Err() != nil {
		return listener.Close()
	}
	done := make(chan error, 1)
	go func() { done <- s.http.Serve(listener) }()
	select {
	case err := <-done:
		return servingError(err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
		defer cancel()
		err := s.http.Shutdown(shutdownCtx)
		if err != nil {
			err = errors.Join(err, s.http.Close())
		}
		return errors.Join(err, servingError(<-done))
	}
}

func servingError(err error) error {
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
