package tsnode

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeNode struct {
	dir        string
	choice     Choice
	needsLogin bool
	failLogout bool
	loggedOut  bool
	closed     bool
}

func (n *fakeNode) Up(context.Context) (string, error) {
	if n.needsLogin {
		return "", &NeedsLoginError{URL: "https://login.tailscale.com/a/test"}
	}
	return n.choice.Name + ".example.ts.net", nil
}
func (n *fakeNode) ListenFunnel(int) (net.Listener, error) { return nil, errors.New("not used") }
func (n *fakeNode) Logout(context.Context) error {
	if n.failLogout {
		return errors.New("logout failed")
	}
	n.loggedOut = true
	return nil
}
func (n *fakeNode) Close() error { n.closed = true; return nil }

func TestChoiceRoundTripAndValidation(t *testing.T) {
	dir := t.TempDir()
	if _, err := ReadChoice(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing choice: %v", err)
	}
	for _, bad := range []Choice{{Name: ""}, {Name: "Agent_Net"}, {Name: "-x"}, {Name: "x-"}, {Name: "a.b"},
		{Name: "agent-net", AuthKeyFile: "relative/key"}} {
		if err := SaveChoice(dir, bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	want := Choice{Name: "agent-net", AuthKeyFile: "/run/secrets/ts-key"}
	if err := SaveChoice(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadChoice(dir)
	if err != nil || got != want {
		t.Fatalf("got %+v, %v", got, err)
	}
	fi, err := os.Stat(filepath.Join(dir, choiceFile))
	if err != nil || fi.Mode().Perm() != 0600 {
		t.Fatalf("mode %v, %v", fi, err)
	}
	if err := RemoveChoice(dir); err != nil {
		t.Fatal(err)
	}
	if err := RemoveChoice(dir); err != nil {
		t.Fatal("second remove:", err)
	}
	if _, err := ReadChoice(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("after remove: %v", err)
	}
}

func TestStartMarksOwnershipBeforeOpening(t *testing.T) {
	dir := t.TempDir()
	var opened *fakeNode
	open := func(stateDir string, c Choice) (Node, error) {
		if _, err := os.Stat(filepath.Join(stateDir, markerFile)); err != nil {
			t.Fatalf("marker not written before open: %v", err)
		}
		if stateDir != StateDir(dir) {
			t.Fatalf("state dir %q", stateDir)
		}
		opened = &fakeNode{dir: stateDir, choice: c}
		return opened, nil
	}
	n, err := Start(dir, Choice{Name: "agent-net"}, open)
	if err != nil || n != opened {
		t.Fatalf("start: %v", err)
	}
	fi, err := os.Stat(StateDir(dir))
	if err != nil || fi.Mode().Perm() != 0700 {
		t.Fatalf("state dir mode %v, %v", fi, err)
	}
}

func TestCleanupOnlyRemovesOwnedState(t *testing.T) {
	t.Run("no marker", func(t *testing.T) {
		dir := t.TempDir()
		// A folder named like ours but without the marker is not ours.
		if err := os.MkdirAll(StateDir(dir), 0700); err != nil {
			t.Fatal(err)
		}
		called := false
		removed, err := Cleanup(context.Background(), dir, func(string, Choice) (Node, error) {
			called = true
			return &fakeNode{}, nil
		})
		if err != nil || removed || called {
			t.Fatalf("removed=%v err=%v called=%v", removed, err, called)
		}
		if _, err := os.Stat(StateDir(dir)); err != nil {
			t.Fatal("unowned folder was removed")
		}
	})
	for _, tc := range []struct {
		name                 string
		needsLogin, fail     bool
		wantRemoved, wantErr bool
		wantLogout           bool
	}{
		{"logged in", false, false, true, false, true},
		{"never logged in", true, false, true, false, false},
		{"logout fails", false, true, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			node := &fakeNode{needsLogin: tc.needsLogin, failLogout: tc.fail}
			open := func(stateDir string, c Choice) (Node, error) {
				node.dir, node.choice = stateDir, c
				return node, nil
			}
			if _, err := Start(dir, Choice{Name: "agent-net"}, open); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(StateDir(dir), "tailscaled.state"), []byte("keys"), 0600); err != nil {
				t.Fatal(err)
			}
			node.closed = false
			removed, err := Cleanup(context.Background(), dir, open)
			if removed != tc.wantRemoved || (err != nil) != tc.wantErr || node.loggedOut != tc.wantLogout || !node.closed {
				t.Fatalf("removed=%v err=%v logout=%v closed=%v", removed, err, node.loggedOut, node.closed)
			}
			if node.choice.Name != "agent-net" {
				t.Fatalf("cleanup opened %+v, want the recorded name", node.choice)
			}
			_, statErr := os.Stat(StateDir(dir))
			if tc.wantRemoved != errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("state folder: %v", statErr)
			}
		})
	}
}

func TestNeedsLoginErrorKeepsLink(t *testing.T) {
	var err error = &NeedsLoginError{URL: "https://login.tailscale.com/a/abc"}
	var nl *NeedsLoginError
	if !errors.As(err, &nl) || nl.URL != "https://login.tailscale.com/a/abc" || !errors.Is(err, ErrNeedsLogin) {
		t.Fatal(err)
	}
}

func TestReadAuthKeyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "key")
	if err := os.WriteFile(path, []byte("  tskey-auth-test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	key, err := readAuthKey(path)
	if err != nil || key != "tskey-auth-test" {
		t.Fatalf("%q %v", key, err)
	}
	if err := os.WriteFile(path, []byte("\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readAuthKey(path); err == nil {
		t.Fatal("empty key accepted")
	}
	// The error names the file, never its content.
	if err := os.WriteFile(path, []byte("tskey-secret value with space"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = readAuthKey(path)
	if err == nil || strings.Contains(err.Error(), "tskey-secret") {
		t.Fatalf("bad key error: %v", err)
	}
}
