package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

// startPhoneListener accepts only the IPv4 loopback address, never a hostname
// or a wildcard. Tailscale Serve is the only intended network entry point.
func startPhoneListener(ctx context.Context, addr string, h http.Handler) (*http.Server, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host != "127.0.0.1" {
		return nil, fmt.Errorf("phone listener must bind to 127.0.0.1:port")
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() { <-ctx.Done(); srv.Close() }()
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "phone listener: %v\n", err)
		}
	}()
	return srv, nil
}
