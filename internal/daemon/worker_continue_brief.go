package daemon

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Headings of a continue's task text. The record of a continue keeps the same
// text, so the next continue can read the round before it back.
const (
	briefOriginalHeading = "Original problem (context):"
	briefRoundHeading    = "This round:"
	briefHistoryHeading  = "Done in earlier rounds:"
	briefProblemHeading  = "Problem this round:"
)

// continueStateBlock tells a continued worker the real state of the branch it
// continues, read from git when the continue starts. The caller writes none of
// it, so it cannot be copied wrong. dir is the old worktree (or the project
// folder) and ref the branch or HEAD to describe.
func continueStateBlock(ctx context.Context, dir, ref string) string {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	lines := []string{"Current state (read from git by agent-net when this round started):"}
	branch := ref
	if ref == "HEAD" {
		if b, err := gitOut(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil && b != "" {
			branch = b
		}
	}
	head, err := gitOut(ctx, dir, "rev-parse", ref+"^{commit}")
	if err != nil {
		return strings.Join(append(lines, "- branch: "+branch, "- head: unknown ("+err.Error()+")"), "\n")
	}
	lines = append(lines, "- branch: "+branch, "- head: "+head)
	if def := continueDefaultRef(ctx, dir); def != "" {
		name := strings.TrimPrefix(strings.TrimPrefix(def, "refs/remotes/"), "refs/heads/")
		if base, err := gitOut(ctx, dir, "merge-base", head, def); err == nil && base != "" {
			ahead, _ := gitOut(ctx, dir, "rev-list", "--count", base+".."+def, "--")
			lines = append(lines, "- merge-base with "+name+": "+base,
				fmt.Sprintf("- %s is %s commits ahead of that base", name, first(ahead, "?")))
		} else {
			lines = append(lines, "- merge-base with "+name+": none")
		}
	} else {
		lines = append(lines, "- merge-base with the default branch: unknown (no default branch found)")
	}
	dirty := "unknown"
	if st, err := gitOut(ctx, dir, "status", "--porcelain", "--untracked-files=no"); err == nil {
		dirty = "no"
		if st != "" {
			dirty = "yes"
		}
	}
	lines = append(lines, "- uncommitted tracked changes in the worktree: "+dirty)
	return strings.Join(lines, "\n")
}

// continueDefaultRef is origin's default branch when the repository has it,
// else the local one.
func continueDefaultRef(ctx context.Context, dir string) string {
	refs := defaultBranchRefs(ctx, dir)
	if len(refs) == 0 {
		return ""
	}
	return refs[len(refs)-1]
}

// continueStateDir is where the state block is read: the old worktree when it
// is there, else the project folder, with the branch to describe.
func continueStateDir(r *workerRow, where string) (dir, ref string) {
	if where == "branch" && r.Branch != "" {
		if info, err := os.Stat(r.Worktree); err == nil && info.IsDir() {
			return r.Worktree, r.Branch
		}
		return first(r.MainPath, r.Path), r.Branch
	}
	if r.Worktree != "" {
		if info, err := os.Stat(r.Worktree); err == nil && info.IsDir() {
			return r.Worktree, "HEAD"
		}
	}
	return first(r.MainPath, r.Path), "HEAD"
}

type briefSection struct{ head, body string }

var briefHeadings = map[string]bool{
	"Problem:": true, "Evidence:": true, "Done:": true, "Hypothesis (yours to drop if wrong):": true,
	"Scope:": true, "Notes:": true, briefOriginalHeading: true, briefRoundHeading: true,
	briefHistoryHeading: true, briefProblemHeading: true,
}

// parseBriefSections reads a task text that WorkerBrief.Text or a continue
// wrote. A block with no known heading belongs to the section before it, or
// is a headless first section (the old free text).
func parseBriefSections(text string) []briefSection {
	var out []briefSection
	for _, block := range strings.Split(strings.TrimSpace(text), "\n\n") {
		head, rest, _ := strings.Cut(block, "\n")
		if briefHeadings[head] {
			out = append(out, briefSection{head: head, body: rest})
			continue
		}
		if len(out) == 0 {
			out = append(out, briefSection{body: block})
			continue
		}
		out[len(out)-1].body += "\n\n" + block
	}
	return out
}

func briefList(body string) []string {
	var items []string
	for _, line := range strings.Split(body, "\n") {
		if line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- ")); line != "" {
			items = append(items, line)
		}
	}
	return items
}

// continueRound builds the round of a continue that brings its own done list:
// the original problem as context, the round's problem, evidence and scope,
// the new done list as current, and every older done list once as history. It
// returns the text and what the record keeps, which are the same.
func continueRound(oldTask string, b protocol.WorkerBrief) string {
	var original string
	var oldDone, history []string
	var rest []string
	for _, s := range parseBriefSections(oldTask) {
		switch s.head {
		case briefOriginalHeading, "Problem:":
			if original == "" {
				original = s.body
			}
		case "Done:", briefRoundHeading:
			oldDone = briefList(s.body)
		case briefHistoryHeading:
			history = briefList(s.body)
		case briefProblemHeading, "Evidence:", "Hypothesis (yours to drop if wrong):", "Scope:", "Notes:":
			// Details of an earlier round, or of the first start: not current.
		default:
			if s.head == "" {
				rest = append(rest, s.body)
			}
		}
	}
	if original == "" {
		original = strings.Join(rest, "\n\n")
	}
	var parts []string
	block := func(heading, text string) {
		if text = strings.TrimSpace(text); text != "" {
			parts = append(parts, heading+"\n"+text)
		}
	}
	list := func(heading string, lines []string) {
		if len(lines) > 0 {
			parts = append(parts, heading+"\n- "+strings.Join(lines, "\n- "))
		}
	}
	block(briefOriginalHeading, original)
	block(briefProblemHeading, b.Problem)
	list("Evidence:", b.EvidenceLines())
	block("Hypothesis (yours to drop if wrong):", b.Hypothesis)
	block("Scope:", b.Scope)
	block("Notes:", b.Notes)
	list(briefRoundHeading, b.DoneLines())
	list(briefHistoryHeading, append(oldDone, history...))
	return strings.Join(parts, "\n\n")
}
