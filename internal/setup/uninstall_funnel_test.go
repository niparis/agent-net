package setup

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/funnel"
)

type uninstallFunnelRunner struct {
	calls  []string
	target string
	fail   bool
}

func (r *uninstallFunnelRunner) Run(_ context.Context, args ...string) (string, string, error) {
	r.calls = append(r.calls, strings.Join(args, " "))
	if args[0] == "status" {
		return `{"BackendState":"Running","Self":{"DNSName":"test.example.ts.net.","Capabilities":["funnel"]}}`, "", nil
	}
	if args[0] == "serve" {
		return `{"Web":{"test.example.ts.net:8443":{"Handlers":{"/":{"Proxy":"` + r.target + `"}}}}}`, "", nil
	}
	if r.fail {
		return "", "failed", errors.New("off failed")
	}
	return "", "", nil
}
func TestUninstallFunnelOwnership(t *testing.T) {
	for _, tc := range []struct {
		name, target           string
		fail, wantOff, wantErr bool
	}{{"ours", "http://127.0.0.1:7422", false, true, false}, {"repurposed", "http://127.0.0.1:9000", false, false, false}, {"failure", "http://127.0.0.1:7422", true, true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("AGENT_NET_CONFIG_DIR", home+"/config")
			t.Setenv("AGENT_NET_STATE_DIR", home+"/state")
			t.Setenv("AGENT_NET_CLAUDE_SESSIONS_DIR", home+"/claude-sessions")
			dir := home + "/gateway"
			if err := funnel.SaveRecord(dir, funnel.Record{Port: 8443, Target: "http://127.0.0.1:7422"}); err != nil {
				t.Fatal(err)
			}
			fake := &uninstallFunnelRunner{target: tc.target, fail: tc.fail}
			var output bytes.Buffer
			err := Uninstall(Options{Data: dir, NoService: true, Out: &output, FunnelRunner: fake})
			if (err != nil) != tc.wantErr || strings.Contains(strings.Join(fake.calls, "\n"), "funnel --https=8443 off") != tc.wantOff {
				t.Fatalf("err=%v calls=%v output=%s", err, fake.calls, output.String())
			}
		})
	}
}
