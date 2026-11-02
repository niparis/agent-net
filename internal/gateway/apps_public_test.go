package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"
)

type publicRunner struct {
	calls   []string
	missing bool
	failOff bool
	target  string
}

func (r *publicRunner) Run(_ context.Context, args ...string) (string, string, error) {
	r.calls = append(r.calls, strings.Join(args, " "))
	if r.missing {
		return "", "", exec.ErrNotFound
	}
	if args[0] == "status" {
		return `{"BackendState":"Running","Self":{"DNSName":"test.example.ts.net.","Capabilities":["funnel"]}}`, "", nil
	}
	if args[0] == "serve" {
		if r.target == "" {
			return `{"TCP":{"443":{"HTTPS":true}},"Web":{"test.example.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:7411"}}}}}`, "", nil
		}
		return fmt.Sprintf(`{"TCP":{"443":{"HTTPS":true}},"Web":{"test.example.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:7411"}}},"test.example.ts.net:8443":{"Handlers":{"/":{"Proxy":%q}}}}}`, r.target), "", nil
	}
	if len(args) > 1 && args[len(args)-1] == "off" {
		if r.failOff {
			return "", "failed", errors.New("off failed")
		}
		r.target = ""
	} else if len(args) > 0 && args[0] == "funnel" {
		r.target = args[len(args)-1]
	}
	return "", "", nil
}
func publicCall(t *testing.T, g *Gateway, op string, p any) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(p)
	v, err := g.handle(context.Background(), &nodeConn{}, op, raw)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(v)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}
func TestAppsPublicLifecycle(t *testing.T) {
	dir := t.TempDir()
	runner := &publicRunner{}
	opts := Options{DataDir: dir, AppsListen: "127.0.0.1:0", FunnelRunner: runner}
	g, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	on := publicCall(t, g, "apps.public.on", nil)
	if on["on"] != true || on["port"] != float64(8443) || on["url"] != "https://test.example.ts.net:8443" {
		t.Fatalf("status: %v", on)
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/mcp", nil)
	req.Host = "test.example.ts.net:8443"
	g.AppsHandler().ServeHTTP(w, req)
	if !strings.Contains(w.Header().Get("WWW-Authenticate"), "https://test.example.ts.net:8443/") {
		t.Fatal(w.Header())
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	g, err = New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if s := publicCall(t, g, "apps.public.status", nil); s["on"] != true || g.publicServer == nil {
		t.Fatalf("restart: %v", s)
	}
	publicCall(t, g, "apps.public.off", nil)
	if g.publicServer != nil {
		t.Fatal("listener remains")
	}
	if !strings.Contains(strings.Join(runner.calls, "\n"), "funnel --https=8443 off") {
		t.Fatal(runner.calls)
	}
	publicCall(t, g, "apps.public.on", nil)
	app, err := g.store.CreateApp("last", "bot", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	publicCall(t, g, "apps.revoke", map[string]string{"app_id": app.ID})
	if s := publicCall(t, g, "apps.public.status", nil); s["on"] != false || g.publicServer != nil {
		t.Fatalf("last Funnel app: %v", s)
	}
	if strings.Count(strings.Join(runner.calls, "\n"), "funnel --https=8443 off") != 3 {
		t.Fatal(runner.calls)
	}
}
func TestAppsPublicPastedURLAndLastRevoke(t *testing.T) {
	g, err := New(Options{DataDir: t.TempDir(), AppsListen: "127.0.0.1:0", FunnelRunner: &publicRunner{missing: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	_, err = g.handle(context.Background(), &nodeConn{}, "apps.public.on", nil)
	if err == nil || !strings.HasPrefix(err.Error(), "Tailscale is not installed on ") || !strings.HasSuffix(err.Error(), " (the gateway machine). Install it there, or use your own tunnel URL (u).") {
		t.Fatal(err)
	}
	publicCall(t, g, "apps.public.url", map[string]string{"url": "https://tunnel.example/"})
	a, _ := g.store.CreateApp("one", "bot", "", "", nil)
	b, _ := g.store.CreateApp("two", "bot", "", "", nil)
	publicCall(t, g, "apps.revoke", map[string]string{"app_id": a.ID})
	if s := publicCall(t, g, "apps.public.status", nil); s["on"] != true || s["url"] != "https://tunnel.example" {
		t.Fatal(s)
	}
	publicCall(t, g, "apps.revoke", map[string]string{"app_id": b.ID})
	if s := publicCall(t, g, "apps.public.status", nil); s["on"] != false || g.publicServer != nil {
		t.Fatal(s)
	}
}

func TestBotsCountsUseLastCall(t *testing.T) {
	s, c := newTestStore(t)
	app, err := s.CreateApp("counter", "bot", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.touchBot(app); err != nil {
		t.Fatal(err)
	}
	counts, err := s.SessionCounts(24 * time.Hour)
	if err != nil || counts.Online != 1 {
		t.Fatalf("fresh: %+v %v", counts, err)
	}
	c.t = c.t.Add(11 * time.Minute)
	counts, err = s.SessionCounts(24 * time.Hour)
	if err != nil || counts.Online != 0 || counts.Offline24h != 1 {
		t.Fatalf("stale: %+v %v", counts, err)
	}
}
