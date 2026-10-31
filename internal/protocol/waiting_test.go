package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCleanWaitingMakesOneShortLine(t *testing.T) {
	w := CleanWaiting(&Waiting{Kind: WaitingPermission, Text: "Deploy?\n\tplease\x00 approve  \r\nnow"})
	if w == nil {
		t.Fatal("a waiting with text was dropped")
	}
	if w.Kind != WaitingPermission {
		t.Errorf("kind = %q, want %q", w.Kind, WaitingPermission)
	}
	if strings.ContainsAny(w.Text, "\n\r\t\x00") {
		t.Errorf("control characters left: %q", w.Text)
	}
	if w.Text != "Deploy? please approve now" {
		t.Errorf("text = %q", w.Text)
	}
	long := CleanWaiting(&Waiting{Kind: WaitingInput, Text: strings.Repeat("x", 500)})
	if long == nil || len([]rune(long.Text)) != MaxWaitingTextLen {
		t.Fatalf("long text was not cut to %d characters: %v", MaxWaitingTextLen, long)
	}
}

func TestCleanWaitingDefaultsAndEmpty(t *testing.T) {
	if w := CleanWaiting(&Waiting{Text: "may I push?"}); w == nil || w.Kind != WaitingInput {
		t.Fatalf("a text with no kind should become an input question: %+v", w)
	}
	if w := CleanWaiting(&Waiting{Kind: WaitingQuestion}); w == nil || w.Kind != WaitingQuestion || w.Text != "" {
		t.Fatalf("a kind alone is a valid waiting state: %+v", w)
	}
	if w := CleanWaiting(nil); w != nil {
		t.Fatalf("nil should stay nil: %+v", w)
	}
	if w := CleanWaiting(&Waiting{Kind: "bogus", Text: "  "}); w != nil {
		t.Fatalf("an unknown kind with no text should be dropped: %+v", w)
	}
}

// A plugin may send "waiting" as the question in one line, and the MCP tool
// does. An object is accepted too, so a newer plugin can name the kind.
func TestWaitingUnmarshalAcceptsStringAndObject(t *testing.T) {
	var p struct {
		Waiting *Waiting `json:"waiting,omitempty"`
	}
	if err := json.Unmarshal([]byte(`{"waiting":"which branch?"}`), &p); err != nil {
		t.Fatal(err)
	}
	if p.Waiting == nil || p.Waiting.Kind != WaitingInput || p.Waiting.Text != "which branch?" {
		t.Fatalf("string form: %+v", p.Waiting)
	}
	if err := json.Unmarshal([]byte(`{"waiting":{"kind":"permission","text":"Bash(rm -rf x)","since":12}}`), &p); err != nil {
		t.Fatal(err)
	}
	if p.Waiting == nil || p.Waiting.Kind != WaitingPermission || p.Waiting.Since != 12 || p.Waiting.Text != "Bash(rm -rf x)" {
		t.Fatalf("object form: %+v", p.Waiting)
	}
	var q struct {
		Waiting *Waiting `json:"waiting,omitempty"`
	}
	if err := json.Unmarshal([]byte(`{"waiting":null}`), &q); err != nil || q.Waiting != nil {
		t.Fatalf("null form: %+v %v", q.Waiting, err)
	}
}

func TestCleanWaitingOptions(t *testing.T) {
	w := CleanWaiting(&Waiting{Kind: WaitingQuestion, Text: "Merge?", Options: []string{" yes\nnow ", "no", "later", "skip", "extra"}, Urgency: "high"})
	if len(w.Options) != 4 || w.Options[0] != "yes now" || w.Urgency != "high" {
		t.Fatalf("waiting: %+v", w)
	}
}
