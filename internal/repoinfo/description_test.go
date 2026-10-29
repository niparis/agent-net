package repoinfo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestExtractDescription(t *testing.T) {
	long := strings.TrimSpace(strings.Repeat("word ", 60))
	cases := []struct{ name, in, want string }{
		{"template form",
			"# Project: Shop Front\nA storefront for the shop, built so customers can order without calling.\n\nBuilt with: Next.js 15, TypeScript.\n\n## North Star Preferences\n- Server components first.\n",
			"A storefront for the shop, built so customers can order without calling"},
		{"template form takes the first non-empty line only",
			"# Project: X\n\nA thing that does Y\nBuilt with: Go\n",
			"A thing that does Y"},
		{"template form after other headings and text",
			"# Notes\nsomething\n\n# Project: X\nA real description.\n",
			"A real description"},
		{"plain H1 and paragraph",
			"# Billing Service\n\nHandles invoices and payments for the platform. It talks to Stripe.\n\n## Build\nmake\n",
			"Handles invoices and payments for the platform"},
		{"a wrapped paragraph is joined",
			"# Billing\n\nHandles invoices and\npayments for the platform.\n",
			"Handles invoices and payments for the platform"},
		{"notes for coding agents first, then the description",
			"# AGENTS.md\n\nNotes for coding agents working on this repository. For what the project is, read README.md.\n\nA sync tool that mirrors photos to a NAS.\n\n## Build\nmake\n",
			"A sync tool that mirrors photos to a NAS"},
		{"only notes for agents",
			"# AGENTS.md\n\nNotes for coding agents working on this repository. For what the project is, read README.md.\n\n## Build\nmake\n",
			""},
		{"this file is for agents",
			"# Guide\n\nThis file tells AI agents how to work here.\n\nA compiler for toys.\n",
			"A compiler for toys"},
		{"a description after an H2 is not used",
			"# Title\n\n## Build\nA sentence under build.\n",
			""},
		{"lists, quotes, tables, html and rules are skipped",
			"# T\n\n- item\n\n> quote\n\n| a | b |\n\n<img src=x>\n\n---\n\n1. first\n\nThe real line.\n",
			"The real line"},
		{"an import line is skipped", "@AGENTS.md\n", ""},
		{"code blocks are skipped",
			"# T\n\n```\nnot a description.\n```\n\nThe real line.\n",
			"The real line"},
		{"a fence hides a fake project heading",
			"# Real\n\n```\n# Project: Fake\nfake line\n```\n\nThe real line.\n",
			"The real line"},
		{"front matter is ignored", "---\ntitle: x\n---\n# T\n\nA line.\n", "A line"},
		{"no heading at all: the top of the file", "A tool that does things.\n\n## Build\n", "A tool that does things"},
		{"CRLF and BOM", "\xef\xbb\xbf# T\r\n\r\nA line.\r\n", "A line"},
		{"markdown is stripped",
			"# Tools\n\nA **fast** [link checker](https://example.com/x) for `docs`, with *style*, _more_ and ![logo](a.png) <b>tags</b>. Second sentence here.\n",
			"A fast link checker for docs, with style, more and tags"},
		{"snake_case is not emphasis", "# T\n\nUses my_var_name and other_thing here.\n", "Uses my_var_name and other_thing here"},
		{"abbreviations do not end the sentence", "# T\n\nA tool for agents, e.g. Claude, and more. Next.\n", "A tool for agents, e.g. Claude, and more"},
		{"a version number does not end the sentence", "# T\n\nRuns on v0.3.10 of the thing. Next.\n", "Runs on v0.3.10 of the thing"},
		{"question marks end the sentence", "# T\n\nWhat is this? Nothing.\n", "What is this?"},
		{"unfilled template placeholder", "# Project: X\nA [TODO: describe]\n", ""},
		{"a todo after real text", "# Project: X\nA tool for parsing logs [TODO: refine]\n", "A tool for parsing logs"},
		{"empty", "", ""},
		{"only a heading", "# Project: X\n", ""},
		{"long line is cut", "# Project: Long\nA " + long + " that goes on.\n", ""}, // checked below
	}
	for _, c := range cases {
		got := ExtractDescription(c.in)
		if c.name == "long line is cut" {
			if n := utf8.RuneCountInString(got); n > protocol.MaxDescriptionLen || n < protocol.MaxDescriptionLen/2 || !strings.HasSuffix(got, "…") || !strings.HasPrefix(got, "A word word") {
				t.Errorf("%s: %d chars: %q", c.name, n, got)
			}
			if strings.HasSuffix(strings.TrimSuffix(got, "…"), " ") || strings.Contains(got, "wor…") {
				t.Errorf("%s: should cut at a word: %q", c.name, got)
			}
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestCutDescriptionOneLongWord(t *testing.T) {
	got := cutDescription(strings.Repeat("x", 300))
	if utf8.RuneCountInString(got) != protocol.MaxDescriptionLen || !strings.HasSuffix(got, "…") {
		t.Errorf("got %d chars", utf8.RuneCountInString(got))
	}
	exact := strings.Repeat("y", protocol.MaxDescriptionLen)
	if cutDescription(exact) != exact {
		t.Error("a description of exactly the limit must stay whole")
	}
}

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadDescriptionPrefersAgentsThenClaude(t *testing.T) {
	dir := t.TempDir()
	if got := ReadDescription(dir); got != "" {
		t.Fatalf("no files: %q", got)
	}
	write(t, dir, "CLAUDE.md", "# Project: C\nFrom CLAUDE.md.\n")
	if got := ReadDescription(dir); got != "From CLAUDE.md" {
		t.Fatalf("CLAUDE.md fallback: %q", got)
	}
	write(t, dir, "AGENTS.md", "# Project: A\nFrom AGENTS.md.\n")
	if got := ReadDescription(dir); got != "From AGENTS.md" {
		t.Fatalf("AGENTS.md first: %q", got)
	}
	// An AGENTS.md without a description falls back to CLAUDE.md.
	write(t, dir, "AGENTS.md", "# AGENTS.md\n\nNotes for coding agents working here.\n")
	if got := ReadDescription(dir); got != "From CLAUDE.md" {
		t.Fatalf("empty AGENTS.md: %q", got)
	}
	// A directory called AGENTS.md is not a file.
	dir2 := t.TempDir()
	os.Mkdir(filepath.Join(dir2, "AGENTS.md"), 0o755)
	if got := ReadDescription(dir2); got != "" {
		t.Fatalf("directory: %q", got)
	}
}

// This repository's own AGENTS.md starts with a note to agents, so it gives
// nothing. The test pins that, so a change to the rule or the file shows up.
func TestThisRepoAgentsMdGivesNothing(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "this-repo-AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if got := ExtractDescription(string(b)); got != "" {
		t.Fatalf("this repo's AGENTS.md now gives %q: update the report and docs", got)
	}
	// With the one-line fix suggested in the docs, it gives the project line.
	fixed := strings.Replace(string(b), "# AGENTS.md\n", "# Project: agent-net\nA gateway, a daemon and plugins that let coding agents on different machines message each other.\n", 1)
	if got := ExtractDescription(fixed); got != "A gateway, a daemon and plugins that let coding agents on different machines message each other" {
		t.Fatalf("with a description line: %q", got)
	}
}

func TestResolverDescriptionInRealRepo(t *testing.T) {
	needGit(t)
	root := filepath.Join(evalDir(t), "shop")
	sub := filepath.Join(root, "pkg", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	runIn(t, root, "init", "-q", "-b", "main")
	write(t, root, "AGENTS.md", "# Project: Shop\nA small shop front.\n")
	r := New()
	if got := r.Lookup(sub); got.Description != "A small shop front" || got.Root != root {
		t.Fatalf("from a subfolder: %+v", got)
	}
	if got := r.Lookup(root).Description; got != "A small shop front" {
		t.Fatalf("from the root: %q", got)
	}
}

func TestResolverDescriptionFromMainTreeInLinkedWorktree(t *testing.T) {
	needGit(t)
	base := evalDir(t)
	main := filepath.Join(base, "main-tree")
	os.MkdirAll(main, 0o755)
	runIn(t, main, "init", "-q", "-b", "main")
	write(t, main, "AGENTS.md", "# Project: M\nThe main tree description.\n")
	runIn(t, main, "add", "AGENTS.md")
	runIn(t, main, "commit", "-q", "-m", "one")
	wt := filepath.Join(base, "wt")
	runIn(t, main, "worktree", "add", "-q", "-b", "feat", wt)
	// The linked worktree has its own copy; the main tree wins.
	write(t, wt, "AGENTS.md", "# Project: W\nThe worktree copy.\n")
	got := New().Lookup(wt)
	if got.Root != main || got.Description != "The main tree description" {
		t.Fatalf("got %+v", got)
	}
}

func TestResolverDescriptionWithoutRepoUsesFolder(t *testing.T) {
	dir := filepath.Join(evalDir(t), "plain")
	os.MkdirAll(dir, 0o755)
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	write(t, dir, "CLAUDE.md", "# Project: P\nFolder without a repository.\n")
	got := New().Lookup(dir)
	if got.Root != "" || got.Description != "Folder without a repository" {
		t.Fatalf("got %+v", got)
	}
}

func TestResolverNoFileMeansEmpty(t *testing.T) {
	dir := filepath.Join(evalDir(t), "bare")
	os.MkdirAll(dir, 0o755)
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	if got := New().Lookup(dir).Description; got != "" {
		t.Fatalf("got %q", got)
	}
}

// The cache is keyed by path and modification time: a changed file is read
// again at the next lookup, with no TTL to wait for; an unchanged file is not
// read again.
func TestResolverDescriptionCacheFollowsMtime(t *testing.T) {
	dir := filepath.Join(evalDir(t), "cached")
	os.MkdirAll(dir, 0o755)
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	path := write(t, dir, "AGENTS.md", "# Project: C\nFirst version.\n")
	t0 := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	os.Chtimes(path, t0, t0)
	r := New()
	if got := r.Lookup(dir).Description; got != "First version" {
		t.Fatalf("first: %q", got)
	}

	// Same size, same mtime, different text: the cache is trusted.
	write(t, dir, "AGENTS.md", "# Project: C\nSecond versio.\n")
	os.Chtimes(path, t0, t0)
	if got := r.Lookup(dir).Description; got != "First version" {
		t.Fatalf("unchanged mtime and size must use the cache, got %q", got)
	}
	// A new mtime: read again, at once.
	t1 := t0.Add(time.Minute)
	os.Chtimes(path, t1, t1)
	if got := r.Lookup(dir).Description; got != "Second versio" {
		t.Fatalf("new mtime: %q", got)
	}
	// Refresh sees it too, and a deleted file clears the description.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if got := r.Refresh(dir).Description; got != "" {
		t.Fatalf("deleted: %q", got)
	}
	// A new CLAUDE.md is picked up.
	write(t, dir, "CLAUDE.md", "# Project: C\nFrom the other file.\n")
	if got := r.Lookup(dir).Description; got != "From the other file" {
		t.Fatalf("new CLAUDE.md: %q", got)
	}
}

func TestDescriptionCacheReadsOncePerVersion(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "AGENTS.md", "# Project: C\nOne.\n")
	var c descCache
	if c.get(dir) != "One" {
		t.Fatal("first read")
	}
	// Corrupt the cached value to prove the next get does not read the file.
	c.mu.Lock()
	e := c.m[path]
	e.desc = "cached"
	c.m[path] = e
	c.mu.Unlock()
	if got := c.get(dir); got != "cached" {
		t.Fatalf("unchanged file was read again: %q", got)
	}
}
