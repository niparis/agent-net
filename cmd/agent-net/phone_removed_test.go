package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestPhoneOwnerCommandRemoved(t *testing.T) {
	t.Setenv("AGENT_NET_GATEWAY_DATA", t.TempDir())
	var out bytes.Buffer
	if code := run(context.Background(), []string{"phone", "owner", "owner@example.test"}, &out); code != 2 || !strings.Contains(out.String(), "unknown command") {
		t.Fatalf("phone command: %d %s", code, out.String())
	}
}
