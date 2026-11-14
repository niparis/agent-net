package daemon

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/daemon/ctoskill"
)

func TestCTOSkillFilesWrittenForBothCTOHarnesses(t *testing.T) {
	state := t.TempDir()
	d := &Daemon{cfg: Config{StateDir: state}}
	for _, tc := range []struct{ harness, root string }{
		{"claude", filepath.Join(state, "cto", "claude")},
		{"opencode", filepath.Join(state, "cto")},
	} {
		if err := d.writeCTOSkill(tc.harness); err != nil {
			t.Fatalf("%s CTO skill: %v", tc.harness, err)
		}
		for _, name := range ctoskill.Names {
			path := filepath.Join(tc.root, ".claude", "skills", ctoskill.Name, name)
			got, err := os.ReadFile(path)
			want, readErr := ctoskill.File(name)
			if err != nil || readErr != nil || !bytes.Equal(got, want) {
				t.Fatalf("%s CTO skill file %s = %q, %v; want %q", tc.harness, path, got, err, want)
			}
		}
	}
	for _, harness := range []string{"pi", "codex", "agy"} {
		if dir, ok := ctoSkillDir(state, harness); ok || dir != "" {
			t.Fatalf("harness %s got a CTO skill folder %q", harness, dir)
		}
		if err := d.writeCTOSkill(harness); err != nil {
			t.Fatalf("harness %s: %v", harness, err)
		}
		if _, err := os.Stat(filepath.Join(state, harness)); !os.IsNotExist(err) {
			t.Fatalf("harness %s got a folder", harness)
		}
	}
}

func TestCTOSkillRewrittenOnlyWhenDifferent(t *testing.T) {
	state := t.TempDir()
	d := &Daemon{cfg: Config{StateDir: state}}
	if err := d.writeCTOSkill("claude"); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(state, "cto", "claude", ".claude", "skills", ctoskill.Name)
	past := time.Now().Add(-time.Hour).Truncate(time.Second)
	for _, name := range ctoskill.Names {
		if err := os.Chtimes(filepath.Join(dir, name), past, past); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.writeCTOSkill("claude"); err != nil {
		t.Fatal(err)
	}
	for _, name := range ctoskill.Names {
		st, err := os.Stat(filepath.Join(dir, name))
		if err != nil || !st.ModTime().Equal(past) {
			t.Fatalf("%s was rewritten although its content was current", name)
		}
	}

	reference := filepath.Join(dir, "reference.md")
	if err := os.WriteFile(reference, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(reference, past, past); err != nil {
		t.Fatal(err)
	}
	if err := d.writeCTOSkill("claude"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(reference)
	want, readErr := ctoskill.File("reference.md")
	if err != nil || readErr != nil || !bytes.Equal(got, want) {
		t.Fatalf("reference.md was not restored: %q, %v", got, err)
	}
	if st, err := os.Stat(reference); err != nil || !st.ModTime().After(past) {
		t.Fatalf("reference.md was not rewritten after its content changed")
	}
	if st, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil || !st.ModTime().Equal(past) {
		t.Fatalf("SKILL.md was rewritten although its content was current")
	}
}

// The skill belongs to the CTO role only, so both role start paths must write
// it before they launch anything.
func TestCTOSkillIsWrittenWhenTheCTOStarts(t *testing.T) {
	for _, harness := range []string{"claude", "opencode"} {
		t.Run(harness, func(t *testing.T) {
			state := t.TempDir()
			bin := filepath.Join(t.TempDir(), harness)
			if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
				t.Fatal(err)
			}
			d := &Daemon{cfg: Config{StateDir: state, HarnessPaths: map[string]string{harness: bin}}}
			var err error
			if harness == "claude" {
				err = d.startClaudeRole(context.Background(), roleCTO, "", "opus")
			} else {
				d.cto.mu.Lock()
				err = d.startOpenCodeRole(context.Background(), roleCTO)
				d.cto.mu.Unlock()
			}
			if err == nil {
				t.Fatal("the fake harness must fail the start")
			}
			dir, ok := ctoSkillDir(state, harness)
			if !ok {
				t.Fatalf("%s has no CTO skill folder", harness)
			}
			for _, name := range ctoskill.Names {
				got, readErr := os.ReadFile(filepath.Join(dir, name))
				want, fileErr := ctoskill.File(name)
				if readErr != nil || fileErr != nil || !bytes.Equal(got, want) {
					t.Fatalf("%s start did not write %s: %q, %v", harness, name, got, readErr)
				}
			}
		})
	}
}
