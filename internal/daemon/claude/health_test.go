package claude

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestDeliveryHealth(t *testing.T) {
	if got := deliveryHealth(nil); got != "unknown" {
		t.Fatal(got)
	}
	if got := deliveryHealth(map[string]found{"s": {}}); got != "broken" {
		t.Fatal(got)
	}
	path := filepath.Join(t.TempDir(), "inbox")
	if got := deliveryHealth(map[string]found{"s": {sock: path}}); got != "broken" {
		t.Fatal(got)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			c.Close()
		}
	}()
	if got := deliveryHealth(map[string]found{"s": {sock: path}}); got != "ok" {
		t.Fatal(got)
	}
	ln.Close()
	os.Remove(path)
}
