package main

import (
	"context"
	"net/http"
	"testing"
)

func TestAppsListenerLoopbackOnly(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:0", "localhost:0", "[::1]:0"} {
		if _, err := startAppsListener(context.Background(), addr, http.NotFoundHandler()); err == nil {
			t.Errorf("accepted %q", addr)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := startAppsListener(ctx, "127.0.0.1:0", http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
}
