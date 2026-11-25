package peermessages

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

// A recorded "set by agent-net" answer and a live accept value agree.
func TestCompareAgreesWhenLiveIsAccept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	writeFile(t, path, `{"crossSessionInbound": "accept"}`, 0o600)
	r := Compare(State{SetByAgentNet: true}, path)
	if r.Problem != "" || !r.LiveSet || r.Live != "accept" || r.Linked || r.ReadOnly {
		t.Fatalf("report = %+v", r)
	}
}

// A managed file that lost the key is a problem, and the report gives the line
// to declare in the dotfile that owns it. Nothing is written.
func TestCompareNamesTheLineForALinkedReadOnlyFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "store", "settings.json")
	writeFile(t, target, `{"model": "opus"}`, 0o444)
	link := filepath.Join(dir, "settings.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	r := Compare(State{SetByAgentNet: true}, link)
	for _, want := range []string{"has no \"crossSessionInbound\"", "link to the read-only file " + target, target,
		"Nix or Home Manager", "rebuild", `"crossSessionInbound": "accept"`} {
		if !strings.Contains(r.Problem, want) {
			t.Fatalf("report %q missing %q", r.Problem, want)
		}
	}
	if !r.Linked || !r.ReadOnly || r.Final != target {
		t.Fatalf("file kind = linked %v read-only %v final %q", r.Linked, r.ReadOnly, r.Final)
	}
	if b, _ := os.ReadFile(target); strings.Contains(string(b), "crossSessionInbound") {
		t.Fatal("the check wrote the settings file")
	}
}

// A link to a writable file is not managed by a package manager: the report
// names the file to edit, and does not ask for a rebuild.
func TestCompareSendsAWritableLinkToItsFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles", "settings.json")
	writeFile(t, target, `{"model": "opus"}`, 0o644)
	link := filepath.Join(dir, "settings.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	r := Compare(State{SetByAgentNet: true}, link)
	for _, want := range []string{"link to " + target, "writable", "Edit that file", `"crossSessionInbound": "accept"`} {
		if !strings.Contains(r.Problem, want) {
			t.Fatalf("report %q missing %q", r.Problem, want)
		}
	}
	for _, unwanted := range []string{"Nix", "Home Manager", "rebuild", "read-only"} {
		if strings.Contains(r.Problem, unwanted) {
			t.Fatalf("a writable link is called %q: %s", unwanted, r.Problem)
		}
	}
}

// A plain file that says hold is a difference, and the report names the setup
// flag instead of a dotfile.
func TestCompareReportsAHoldValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	writeFile(t, path, `{"crossSessionInbound": "hold"}`, 0o600)
	r := Compare(State{SetByAgentNet: true}, path)
	for _, want := range []string{`says "hold"`, "--accept-peer-messages"} {
		if !strings.Contains(r.Problem, want) {
			t.Fatalf("report %q missing %q", r.Problem, want)
		}
	}
}

// A plain read-only file names the line to declare, not a link.
func TestCompareCallsAPlainReadOnlyFileReadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	writeFile(t, path, `{"model": "opus"}`, 0o444)
	r := Compare(State{SetByAgentNet: true}, path)
	for _, want := range []string{"read-only", "Declare this line", "rebuild"} {
		if !strings.Contains(r.Problem, want) {
			t.Fatalf("report %q missing %q", r.Problem, want)
		}
	}
	if strings.Contains(r.Problem, "link") {
		t.Fatalf("a plain file is called a link: %s", r.Problem)
	}
}

// A missing file is named, and a record that did not set the key is not a
// problem: the owner chose to hold messages.
func TestCompareTreatsAMissingFileAsTheRecordedChoice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if r := Compare(State{SetByAgentNet: true}, path); !strings.Contains(r.Problem, "does not exist") {
		t.Fatalf("report = %+v", r)
	}
	if r := Compare(State{Asked: true}, path); r.Problem != "" {
		t.Fatalf("owner's hold choice reported: %+v", r)
	}
}
