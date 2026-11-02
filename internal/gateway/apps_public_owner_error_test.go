package gateway

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
)

type ownerErrorRunner struct {
	kind string
}

func (r ownerErrorRunner) Run(_ context.Context, args ...string) (string, string, error) {
	if r.kind == "missing" {
		return "", "", exec.ErrNotFound
	}
	if args[0] == "status" {
		if r.kind == "not-running" {
			return `{"BackendState":"Stopped"}`, "", nil
		}
		if r.kind == "not-allowed" {
			return `{"BackendState":"Running","Self":{"DNSName":"test.example.ts.net."}}`, "", nil
		}
		return `{"BackendState":"Running","Self":{"DNSName":"test.example.ts.net.","Capabilities":["funnel"]}}`, "", nil
	}
	if args[0] == "serve" {
		return `{}`, "", nil
	}
	if r.kind == "operator" {
		return "", "Access denied. To change settings, use --operator", errors.New("exit 1")
	}
	if r.kind == "not-allowed" {
		return "", "Funnel is not enabled on your tailnet. https://login.tailscale.com/admin/funnel", errors.New("exit 1")
	}
	return "", "", nil
}

func TestAppsPublicOwnerFacingFunnelErrors(t *testing.T) {
	for _, tt := range []struct {
		kind string
		want string
	}{
		{"operator", "On research-gateway (the gateway machine, where internet access runs), run once in a terminal: sudo tailscale set --operator=$USER. Then press p again."},
		{"missing", "Tailscale is not installed on research-gateway (the gateway machine). Install it there, or use your own tunnel URL (u)."},
		{"not-running", "Tailscale is not running on research-gateway (the gateway machine). Start it there, then press p again."},
		{"not-allowed", "Allow Funnel for your tailnet: open https://tailscale.com/s/no-funnel, then press p again."},
	} {
		t.Run(tt.kind, func(t *testing.T) {
			host, err := os.Hostname()
			if err != nil {
				t.Fatal(err)
			}
			g, err := New(Options{DataDir: t.TempDir(), AppsListen: "127.0.0.1:0", FunnelRunner: ownerErrorRunner{kind: tt.kind}})
			if err != nil {
				t.Fatal(err)
			}
			defer g.Close()
			node, _, err := g.store.CreateNode(host, "linux")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := g.store.SetNodeDisplayName(node, "research-gateway"); err != nil {
				t.Fatal(err)
			}
			_, err = g.publicOperation(context.Background(), "apps.public.on", nil)
			if err == nil || err.Error() != tt.want {
				t.Fatalf("operation error = %v, want %q", err, tt.want)
			}
			if g.public.Error != tt.want {
				t.Fatalf("status error = %q, want %q", g.public.Error, tt.want)
			}
		})
	}
}
