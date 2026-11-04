package setup

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/tsnode"
)

func TestSetupGatewaySavesTailscaleChoiceBeforeRestart(t *testing.T) {
	data := t.TempDir()
	var out bytes.Buffer
	var choiceAtInstall error = errors.New("not installed")
	r := &runner{
		Options: Options{Out: &out, Gateway: true, Data: data, Exe: os.Args[0], Dist: t.TempDir(),
			TailscaleName: "agent-net", TailscaleAuthKeyFile: "/run/secrets/ts-key"},
		installServiceFn: func(Service) (string, error) {
			_, choiceAtInstall = tsnode.ReadChoice(data)
			return "ok", nil
		},
	}
	r.setupGateway(context.Background())
	if choiceAtInstall != nil {
		t.Fatalf("choice not saved before the gateway restart: %v", choiceAtInstall)
	}
	c, err := tsnode.ReadChoice(data)
	if err != nil || c != (tsnode.Choice{Name: "agent-net", AuthKeyFile: "/run/secrets/ts-key"}) {
		t.Fatalf("choice %+v %v", c, err)
	}
	if !strings.Contains(out.String(), "Tailscale name") || !strings.Contains(out.String(), "agent-net") {
		t.Fatal(out.String())
	}

	// A later setup without the flag keeps the choice.
	out.Reset()
	r = &runner{Options: Options{Out: &out, Gateway: true, Data: data, Exe: os.Args[0], Dist: t.TempDir()},
		installServiceFn: func(Service) (string, error) { return "ok", nil }}
	r.setupGateway(context.Background())
	if _, err := tsnode.ReadChoice(data); err != nil {
		t.Fatal("choice lost by a plain setup:", err)
	}

	// --apps-tailscale-machine goes back to the machine's node.
	out.Reset()
	r = &runner{Options: Options{Out: &out, Gateway: true, Data: data, Exe: os.Args[0], Dist: t.TempDir(), TailscaleMachine: true},
		installServiceFn: func(Service) (string, error) { return "ok", nil }}
	r.setupGateway(context.Background())
	if _, err := tsnode.ReadChoice(data); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("choice kept: %v", err)
	}
	if !strings.Contains(out.String(), "machine") {
		t.Fatal(out.String())
	}

	// A bad name fails the step and writes nothing.
	out.Reset()
	r = &runner{Options: Options{Out: &out, Gateway: true, Data: data, Exe: os.Args[0], Dist: t.TempDir(), TailscaleName: "Not Valid"},
		installServiceFn: func(Service) (string, error) { return "ok", nil }}
	r.setupGateway(context.Background())
	if r.failed == nil {
		t.Fatal("bad name accepted")
	}
	if _, err := tsnode.ReadChoice(data); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("bad choice written: %v", err)
	}
}

type uninstallTSNode struct{ loggedOut bool }

func (n *uninstallTSNode) Up(context.Context) (string, error)     { return "agent-net.example.ts.net", nil }
func (n *uninstallTSNode) ListenFunnel(int) (net.Listener, error) { return nil, errors.New("unused") }
func (n *uninstallTSNode) Logout(context.Context) error           { n.loggedOut = true; return nil }
func (n *uninstallTSNode) Close() error                           { return nil }

func TestUninstallRemovesOwnedTailscaleNode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AGENT_NET_CONFIG_DIR", home+"/config")
	t.Setenv("AGENT_NET_STATE_DIR", home+"/state")
	t.Setenv("AGENT_NET_CLAUDE_SESSIONS_DIR", home+"/claude-sessions")
	data := home + "/gateway"
	node := &uninstallTSNode{}
	open := func(string, tsnode.Choice) (tsnode.Node, error) { return node, nil }
	if err := tsnode.SaveChoice(data, tsnode.Choice{Name: "agent-net"}); err != nil {
		t.Fatal(err)
	}
	if _, err := tsnode.Start(data, tsnode.Choice{Name: "agent-net"}, open); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := Uninstall(Options{Data: data, NoService: true, Out: &out, FunnelRunner: &uninstallFunnelRunner{}, TailscaleNode: open})
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !node.loggedOut {
		t.Fatal("node not logged out")
	}
	if _, err := os.Stat(tsnode.StateDir(data)); !os.IsNotExist(err) {
		t.Fatalf("state folder kept: %v", err)
	}
	if _, err := tsnode.ReadChoice(data); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("choice kept: %v", err)
	}
	if !strings.Contains(out.String(), "Tailscale node") {
		t.Fatal(out.String())
	}

	// Nothing owned: the step is skipped and nothing is opened.
	out.Reset()
	opened := false
	err = Uninstall(Options{Data: data, NoService: true, Out: &out, FunnelRunner: &uninstallFunnelRunner{},
		TailscaleNode: func(string, tsnode.Choice) (tsnode.Node, error) { opened = true; return node, nil }})
	if err != nil || opened || !strings.Contains(out.String(), "skipped (no owned node)") {
		t.Fatalf("err=%v opened=%v\n%s", err, opened, out.String())
	}
}
