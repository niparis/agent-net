package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

func startAppsListener(ctx context.Context, addr string, h http.Handler) (*http.Server, error) {
	return startAppsListenerWithTimeout(ctx, addr, h, 30*time.Second)
}

func startAppsListenerWithTimeout(ctx context.Context, addr string, h http.Handler, readTimeout time.Duration) (*http.Server, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host != "127.0.0.1" {
		return nil, fmt.Errorf("apps listener must bind to 127.0.0.1:port")
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	slots := make(chan struct{}, 64)
	limited := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
			h.ServeHTTP(w, r)
		default:
			http.Error(w, "too many requests", http.StatusServiceUnavailable)
		}
	})
	srv := &http.Server{Addr: ln.Addr().String(), Handler: limited, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: readTimeout, IdleTimeout: 60 * time.Second}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = srv.Close()
		case <-done:
		}
	}()
	go func() {
		defer close(done)
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "apps listener: %v\n", err)
		}
	}()
	return srv, nil
}
