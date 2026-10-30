package main

import (
	"path/filepath"
	"testing"

	"github.com/niparis/agent-net/internal/gateway"
)

func TestPhoneOwnerCommand(t *testing.T) {
	dir := t.TempDir()
	if err := cmdPhone([]string{"owner", "--data", dir, "owner@example.test"}); err != nil {
		t.Fatal(err)
	}
	s, err := gateway.OpenStore(filepath.Join(dir, "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	owner, err := s.PhoneOwner()
	if err != nil || owner != "owner@example.test" {
		t.Fatalf("owner %q: %v", owner, err)
	}
	if err := cmdPhone([]string{"owner", "--data", dir, "other@example.test"}); err == nil {
		t.Fatal("changed owner")
	}
}
