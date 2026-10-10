package server

import (
	"context"
	"net"
	"os"
	"os/signal"
	"syscall"
)

// Run listens on the configured address and gracefully stops on context
// cancellation, SIGINT or SIGTERM. A normal shutdown returns nil.
func (s *Server) Run(ctx context.Context) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	if ctx.Err() != nil {
		return nil
	}
	listener, err := net.Listen("tcp", s.http.Addr)
	if err != nil {
		return err
	}
	return s.Serve(ctx, listener)
}
