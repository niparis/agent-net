package protocol

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Gateway notes (decision 0157) are named notes that live in the owner
// judgment store (standing_orders). An agent writes one with agent_note_write
// and finds them with agent_notes_find.
const (
	OpNoteWrite = "note_write" // takes NoteWriteRequest, answers NoteWriteResult
	OpNotesFind = "notes_find" // takes NotesFindRequest, answers NotesFindResult

	// Scope says who sees a note: one agent, one project, or every session.
	NoteScopeAgent   = "agent"
	NoteScopeProject = "project"
	NoteScopeOwner   = "owner"

	// A note is an owner rule (StandingKindRule, only the owner writes it),
	// feedback (how to work), a project fact, or a reference (where to look).
	StandingKindFeedback  = "feedback"
	StandingKindFact      = "fact"
	StandingKindReference = "reference"

	StandingSourceAgent = "agent"

	// NotesIndexPerScope is how many titles a session sees for each scope, and
	// NotesIndexLineMax the longest title line in characters. Three scopes give
	// at most 24 lines. The rest stays behind agent_notes_find.
	NotesIndexPerScope = 8
	NotesIndexLineMax  = 160
	// NotesSimilarMax is how many similar notes a write answers with.
	NotesSimilarMax = 3
	// NotesStaleDays is the age after which an unread, unwritten note is
	// flagged for review. Nothing is deleted by age.
	NotesStaleDays = 30
	// NotesFindMax is the most notes one find answers with.
	NotesFindMax = 20
)

// NoteWriteRequest is agent_note_write. A note is named inside its scope:
// writing the same name again updates it.
type NoteWriteRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
	Scope       string `json:"scope,omitempty"`
	Kind        string `json:"kind,omitempty"`
}

// NoteWriteFrame is "note.write", daemon to gateway. Session is the session
// that writes; the gateway reads its agent and project from it.
type NoteWriteFrame struct {
	NoteWriteRequest
	Session string `json:"session_id"`
}

// NoteRef is the short form of a note: enough to decide whether to merge.
type NoteRef struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Scope       string `json:"scope"`
	Kind        string `json:"kind"`
}

// NoteWriteResult says whether the note was added or updated, and lists up to
// NotesSimilarMax other notes of the same scope that look alike, so the agent
// can merge into one instead of adding another.
type NoteWriteResult struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Scope   string    `json:"scope"`
	Kind    string    `json:"kind"`
	Created bool      `json:"created"`
	Similar []NoteRef `json:"similar"`
	Note    string    `json:"note,omitempty"`
}

// NotesFindRequest is agent_notes_find. Text matches names, descriptions and
// bodies; empty text lists the most used. Scope narrows to one scope.
type NotesFindRequest struct {
	Text  string `json:"text,omitempty"`
	Scope string `json:"scope,omitempty"`
	// Stale lists only the notes nobody read or wrote for NotesStaleDays.
	Stale bool `json:"stale,omitempty"`
}

// NotesFindFrame is "notes.find", daemon to gateway.
type NotesFindFrame struct {
	NotesFindRequest
	Session string `json:"session_id"`
}

// Note is one note with its body, as find answers.
type Note struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
	Scope       string `json:"scope"`
	Kind        string `json:"kind"`
	Source      string `json:"source"`
	SavedAt     int64  `json:"saved_at"`
	UsedAt      int64  `json:"used_at"`
	Uses        int    `json:"uses"`
	// Stale is true when nothing read or wrote the note for NotesStaleDays.
	Stale bool `json:"stale,omitempty"`
}

// NotesFindResult is the answer of agent_notes_find.
type NotesFindResult struct {
	Notes []Note `json:"notes"`
	Total int    `json:"total"`
	// StaleCount is how many of the notes a session sees are stale.
	StaleCount int    `json:"stale_count"`
	Note       string `json:"note,omitempty"`
}

var noteNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// CleanNoteName turns a name into its stored form: lower case, words joined
// by hyphens. It refuses an empty name or one with other characters.
func CleanNoteName(name string) (string, error) {
	name = strings.ToLower(strings.Join(strings.Fields(name), "-"))
	if name == "" || len(name) > 80 || !noteNameRE.MatchString(name) {
		return "", errors.New("a note name is 1 to 80 letters, digits, . _ - (spaces become hyphens)")
	}
	return name, nil
}

// CleanNoteDescription makes the description one line.
func CleanNoteDescription(desc string) (string, error) {
	desc = strings.Join(strings.Fields(desc), " ")
	if desc == "" {
		return "", errors.New("a note needs a one-line description")
	}
	return desc, nil
}

// ValidNoteScope reports whether s is a scope.
func ValidNoteScope(s string) bool {
	return s == NoteScopeAgent || s == NoteScopeProject || s == NoteScopeOwner
}

// ValidNoteKind reports whether k is a kind an agent may write. The owner
// rule kind is not: the owner saves it through the existing path.
func ValidNoteKind(k string) bool {
	return k == StandingKindFeedback || k == StandingKindFact || k == StandingKindReference
}

// NotesIndexLine is one title line of a session's start text.
func NotesIndexLine(n Note) string {
	line := fmt.Sprintf("- %s (%s): %s", n.Name, n.Kind, n.Description)
	if r := []rune(line); len(r) > NotesIndexLineMax {
		line = string(r[:NotesIndexLineMax-3]) + "..."
	}
	return line
}
