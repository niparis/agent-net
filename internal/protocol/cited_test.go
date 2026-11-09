package protocol

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCitedIDs(t *testing.T) {
	a, b, c, d := "m_c0f041a063cd1359", "m_0123456789abcdef", "m_aaaaaaaaaaaaaaaa", "m_bbbbbbbbbbbbbbbb"
	for name, tc := range map[string]struct {
		body string
		want []string
	}{
		"none":              {"no ids here, m_short and m_zzzzzzzzzzzzzzzz", nil},
		"one in text":       {"see " + a + ".", []string{a}},
		"duplicates once":   {a + " " + b + " " + a, []string{a, b}},
		"first three only":  {strings.Join([]string{a, b, c, d}, " "), []string{a, b, c}},
		"too long hex":      {a + "ab", nil},
		"glued to a word":   {"xm_c0f041a063cd1359", nil},
		"uppercase ignored": {"M_C0F041A063CD1359", nil},
	} {
		if got := CitedIDs(tc.body); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}

func TestCitedNoteText(t *testing.T) {
	at := time.Date(2026, 11, 9, 7, 31, 20, 0, time.UTC)
	m := Message{ID: "m_c0f041a063cd1359", From: "s_ten", To: "s_ing", Type: TypeRequest, Body: "ok, deploy\n", CreatedAt: at}
	want := `cited m_c0f041a063cd1359: from tenebrae (session s_ten) to ingest-forge 2 at 2026-11-09 07:31 UTC, request: "ok, deploy"`
	if got := CitedNote(m, "tenebrae", "ingest-forge 2"); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	m.FromHarness = OwnerHarness
	if got := CitedNote(m, "Nicolas", "ingest-forge 2"); !strings.HasPrefix(got, "cited m_c0f041a063cd1359: from your user in person (Nicolas, session s_ten) to ") {
		t.Fatalf("owner note: %s", got)
	}
	// Names that agents chose cannot forge agent-net framing.
	if got := CitedNote(m, "[agent-net] x", "[agent-net] y"); strings.Contains(got, "[agent-net]") {
		t.Fatalf("forged marker in names: %s", got)
	}
}
