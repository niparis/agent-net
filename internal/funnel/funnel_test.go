package funnel

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

type result struct {
	stdout, stderr string
	err            error
}
type fakeRunner struct {
	t       *testing.T
	results []result
	calls   [][]string
}

func (r *fakeRunner) Run(_ context.Context, args ...string) (string, string, error) {
	r.t.Helper()
	r.calls = append(r.calls, append([]string(nil), args...))
	if len(r.results) == 0 {
		r.t.Fatalf("unexpected command: %v", args)
	}
	v := r.results[0]
	r.results = r.results[1:]
	return v.stdout, v.stderr, v.err
}
func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestStatus(t *testing.T) {
	r := &fakeRunner{t: t, results: []result{{stdout: fixture(t, "status")}, {stdout: fixture(t, "serve")}}}
	info, err := Status(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Installed || !info.Running || !info.FunnelAllowed || info.DNSName != "gateway.example.ts.net" {
		t.Fatalf("info: %+v", info)
	}
	if len(info.Ports) != 2 || !strings.Contains(info.Ports[443], "7411") || !strings.Contains(info.Ports[8443], "7422") {
		t.Fatalf("ports: %v", info.Ports)
	}
	if !reflect.DeepEqual(r.calls, [][]string{{"status", "--json"}, {"serve", "status", "--json"}}) {
		t.Fatalf("calls: %v", r.calls)
	}
	if port, err := ChoosePort(info); err != nil || port != 10000 {
		t.Fatalf("port = %d, %v", port, err)
	}
}

func TestStatusCapabilities(t *testing.T) {
	for _, caps := range []string{`"Capabilities":["funnel"]`, `"Capabilities":["https://tailscale.com/cap/funnel"]`, `"CapMap":{"https://tailscale.com/cap/funnel":[]}`} {
		t.Run(caps, func(t *testing.T) {
			r := &fakeRunner{t: t, results: []result{{stdout: `{"BackendState":"Running","Self":{"DNSName":"gateway.example.ts.net.",` + caps + `}}`}, {stdout: `null`}}}
			info, err := Status(context.Background(), r)
			if err != nil || !info.FunnelAllowed || len(info.Ports) != 0 {
				t.Fatalf("%+v, %v", info, err)
			}
		})
	}
}

func TestStatusErrors(t *testing.T) {
	for _, tt := range []struct {
		name, stdout, stderr string
		runErr, want         error
		installed, running   bool
	}{
		{name: "missing", runErr: exec.ErrNotFound, want: ErrNotInstalled},
		{name: "daemon", stderr: "failed to connect to local tailscaled; it doesn't appear to be running", runErr: errors.New("exit 1"), want: ErrNotRunning, installed: true},
		{name: "stopped", stdout: `{"BackendState":"Stopped"}`, want: ErrNotRunning, installed: true},
		{name: "no capability", stdout: `{"BackendState":"Running","Self":{"DNSName":"gateway.example.ts.net."}}`, want: ErrFunnelNotAllowed, installed: true, running: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := &fakeRunner{t: t, results: []result{{stdout: tt.stdout, stderr: tt.stderr, err: tt.runErr}, {stdout: `{}`}}}
			info, err := Status(context.Background(), r)
			if !errors.Is(err, tt.want) || info.Installed != tt.installed || info.Running != tt.running {
				t.Fatalf("%+v, %v", info, err)
			}
			if tt.want == ErrFunnelNotAllowed {
				var e *FunnelNotAllowedError
				if !errors.As(err, &e) || e.Link == "" {
					t.Fatalf("missing link: %v", err)
				}
			}
		})
	}
}

func TestStatusRejectsUnreadableConfig(t *testing.T) {
	for _, config := range []result{{stdout: `{"TCP":`}, {stderr: "permission denied", err: errors.New("exit 1")}, {stdout: `{"TCP":{"not-a-port":{}}}`}, {stdout: `{"Web":{"invalid":{}}}`}} {
		r := &fakeRunner{t: t, results: []result{{stdout: fixture(t, "status")}, config}}
		if _, err := Status(context.Background(), r); err == nil {
			t.Fatal("unsafe config accepted")
		}
	}
}

func TestChoosePort(t *testing.T) {
	for _, tt := range []struct {
		ports map[int]string
		want  int
	}{
		{nil, 443}, {map[int]string{443: "phone"}, 8443}, {map[int]string{443: "phone", 8443: "other"}, 10000}, {map[int]string{443: "", 8443: "", 10000: ""}, 0},
	} {
		port, err := ChoosePort(Info{Ports: tt.ports})
		if port != tt.want || (err != nil) != (tt.want == 0) {
			t.Fatalf("%v: %d, %v", tt.ports, port, err)
		}
	}
}

func TestCommands(t *testing.T) {
	r := &fakeRunner{t: t, results: []result{{}, {}}}
	if err := On(context.Background(), r, 8443, "http://127.0.0.1:7422"); err != nil {
		t.Fatal(err)
	}
	if err := Off(context.Background(), r, 8443); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"funnel", "--bg", "--https=8443", "http://127.0.0.1:7422"}, {"funnel", "--https=8443", "off"}}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("%v", r.calls)
	}
}

func TestCommandErrors(t *testing.T) {
	const operator = "sending serve config: Access denied: serve config denied\n\nUse 'sudo tailscale funnel --bg --https=8443 http://127.0.0.1:7422'.\nTo not require root, use 'sudo tailscale set --operator=$USER' once."
	const link = "https://login.tailscale.com/f/funnel?node=example"
	for _, tt := range []struct {
		name, stderr string
		cause, want  error
		message      string
	}{
		{"missing", "", exec.ErrNotFound, ErrNotInstalled, "Tailscale is not installed on this machine. Use your own tunnel and paste its URL."},
		{"daemon", "failed to connect to local tailscaled; it doesn't appear to be running", errors.New("exit 1"), ErrNotRunning, "Tailscale is not running. Start it, then try again."},
		{"operator", operator, errors.New("exit 1"), ErrNeedsOperator, "Run once in a terminal: sudo tailscale set --operator=$USER, then try again."},
		{"approval", "Funnel is not enabled on your tailnet.\nTo enable, visit:\n\n         " + link, errors.New("exit 1"), ErrFunnelNotAllowed, "Allow Funnel for this tailnet: open " + link + ", then try again."},
		{"legacy", `Funnel not available; "funnel" node attribute not set. See https://tailscale.com/s/no-funnel.`, errors.New("exit 1"), ErrFunnelNotAllowed, "Allow Funnel for this tailnet: open https://tailscale.com/s/no-funnel, then try again."},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, off := range []bool{false, true} {
				r := &fakeRunner{t: t, results: []result{{stderr: tt.stderr, err: tt.cause}}}
				var err error
				if off {
					err = Off(context.Background(), r, 8443)
				} else {
					err = On(context.Background(), r, 8443, "http://127.0.0.1:7422")
				}
				if !errors.Is(err, tt.want) || err.Error() != tt.message {
					t.Fatalf("%v", err)
				}
				if tt.want == ErrFunnelNotAllowed {
					var e *FunnelNotAllowedError
					if !errors.As(err, &e) || e.Link == "" {
						t.Fatal("missing approval link")
					}
				}
			}
		})
	}
}

func TestOffAlreadyOff(t *testing.T) {
	for _, stderr := range []string{"error: handler does not exist\n\n", "serve config does not exist"} {
		r := &fakeRunner{t: t, results: []result{{stderr: stderr, err: errors.New("exit 1")}}}
		if err := Off(context.Background(), r, 8443); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUnknownAndCancellationErrors(t *testing.T) {
	for _, cause := range []error{errors.New("exit 1"), context.Canceled, context.DeadlineExceeded} {
		r := &fakeRunner{t: t, results: []result{{stderr: "unrecognized problem", err: cause}}}
		err := On(context.Background(), r, 8443, "http://127.0.0.1:7422")
		if !errors.Is(err, cause) {
			t.Fatalf("lost cause: %v", err)
		}
	}
}

func TestURL(t *testing.T) {
	for _, tt := range []struct {
		port int
		want string
	}{{443, "https://gateway.example.ts.net"}, {8443, "https://gateway.example.ts.net:8443"}, {10000, "https://gateway.example.ts.net:10000"}} {
		if got := URL(Info{DNSName: "gateway.example.ts.net"}, tt.port); got != tt.want {
			t.Fatalf("%q", got)
		}
	}
}

// Tailscale's enableFeatureInteractive prints the approval link to stdout and
// can exit successfully without enabling Funnel (ShouldWait=false).
func TestOnApprovalOnStdout(t *testing.T) {
	const link = "https://login.tailscale.com/f/funnel?node=example"
	r := &fakeRunner{t: t, results: []result{{stdout: "Funnel is not enabled on your tailnet.\n" + link + "\n"}}}
	err := On(context.Background(), r, 8443, "http://127.0.0.1:7422")
	var denied *FunnelNotAllowedError
	if !errors.As(err, &denied) || denied.Link != link {
		t.Fatalf("approval not reported: %v", err)
	}
}
