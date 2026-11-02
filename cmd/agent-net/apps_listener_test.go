package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
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

func TestAppsListenerReadDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := startAppsListenerWithTimeout(ctx, "127.0.0.1:0", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "body timed out", http.StatusRequestTimeout)
		}
	}), 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	conn, err := net.Dial("tcp", srv.Addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err = conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = io.WriteString(conn, "POST /mcp HTTP/1.1\r\nHost: example.test\r\nContent-Length: 5\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	response, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("stalled body did not close: %v", err)
	}
	if !strings.Contains(string(response), "408") {
		t.Fatalf("response %s", response)
	}
}

func TestAppsListenerConcurrentLimit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{}, 64)
	release := make(chan struct{})
	srv, err := startAppsListener(ctx, "127.0.0.1:0", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { entered <- struct{}{}; <-release }))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	done := make(chan struct{}, 64)
	for i := 0; i < 64; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			res, err := http.Get("http://" + srv.Addr)
			if err == nil {
				res.Body.Close()
			}
		}()
	}
	defer func() {
		close(release)
		for i := 0; i < 64; i++ {
			<-done
		}
	}()
	deadline := time.After(2 * time.Second)
	for i := 0; i < 64; i++ {
		select {
		case <-entered:
		case <-deadline:
			t.Fatal("requests did not reach the handler")
		}
	}
	client := &http.Client{Timeout: time.Second}
	res, err := client.Get("http://" + srv.Addr)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 503 {
		t.Fatalf("overflow status %d", res.StatusCode)
	}
}
