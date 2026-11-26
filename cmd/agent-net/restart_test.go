package main

import (
	"context"
	"strings"
	"testing"
)

func TestRestartRunsTheRequestedServices(t *testing.T) {
	for _, c := range []struct {
		name      string
		args      []string
		wantCalls []string
		wantOut   []string
	}{
		{"daemon only", []string{"restart"}, []string{"daemon"}, []string{"daemon restarted"}},
		{"gateway too", []string{"restart", "--gateway"}, []string{"daemon", "gateway"}, []string{"daemon restarted", "gateway restarted"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			oldDaemon, oldGateway := restartDaemonService, restartGatewayService
			t.Cleanup(func() { restartDaemonService, restartGatewayService = oldDaemon, oldGateway })
			var calls []string
			restartDaemonService = func() (string, error) {
				calls = append(calls, "daemon")
				return "daemon restarted", nil
			}
			restartGatewayService = func() (string, error) {
				calls = append(calls, "gateway")
				return "gateway restarted", nil
			}

			var stderr strings.Builder
			out := captureStdout(t, func() {
				if code := run(context.Background(), c.args, &stderr); code != 0 {
					t.Fatalf("exit code %d, stderr %q", code, stderr.String())
				}
			})
			if got := strings.TrimSpace(out); got != strings.Join(c.wantOut, "\n") {
				t.Fatalf("printed %q, want %v", out, c.wantOut)
			}
			if strings.Join(calls, ",") != strings.Join(c.wantCalls, ",") {
				t.Fatalf("restarted %v, want %v", calls, c.wantCalls)
			}
		})
	}
}

func TestUsageListsRestart(t *testing.T) {
	if !strings.Contains(usage, "\n  restart ") {
		t.Fatalf("usage does not list restart:\n%s", usage)
	}
}
