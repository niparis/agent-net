package gateway

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

const compactionText = "/compact Keep: the current task, the open questions and the files in play."

// seedJudgment inserts one owner judgment row straight into the table.
func seedJudgment(t *testing.T, s *Store, id, kind, text string) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO standing_orders(id,text,saved_at,source,tags,pinned,updated_at,kind,area,ref) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		id, text, time.Now().Unix(), protocol.StandingSourceOwner, "[]", 0, 0, kind, "tenebrae-strats", "i_"+id); err != nil {
		t.Fatal(err)
	}
}

func judgmentIDs(rows []protocol.StandingOrder) []string {
	ids := []string{}
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	return ids
}

// A voided judgment stays in the table but no search, listing or brief shows it.
func TestVoidedJudgmentsAreHiddenFromReads(t *testing.T) {
	s, _ := newTestStore(t)
	seedJudgment(t, s, "so_keep", protocol.StandingKindAnswer, "Q: ship it? / A: yes ship the release")
	seedJudgment(t, s, "so_void", protocol.StandingKindAnswer, "Q: ship it? / A: yes ship the false release")
	if _, err := s.db.Exec(`UPDATE standing_orders SET voided_at=?,void_reason='not the owner' WHERE id='so_void'`, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}

	for _, q := range []protocol.JudgmentsFindRequest{{}, {Query: "release"}, {Kind: protocol.StandingKindAnswer, Area: "tenebrae-strats"}} {
		got := judgmentIDs(mustFind(t, s, q))
		if len(got) != 1 || got[0] != "so_keep" {
			t.Fatalf("find %+v returned %v, want only so_keep", q, got)
		}
	}
	facts, err := s.briefFacts(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(facts, "false release") || !strings.Contains(facts, "yes ship the release") {
		t.Fatalf("brief facts: %q", facts)
	}
	var kept int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM standing_orders WHERE id='so_void'`).Scan(&kept); err != nil || kept != 1 {
		t.Fatalf("voided row must stay in the table: %d %v", kept, err)
	}
}

func mustFind(t *testing.T, s *Store, q protocol.JudgmentsFindRequest) []protocol.StandingOrder {
	t.Helper()
	rows, err := s.FindJudgments(q)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// The migration voids the idle compaction command that was stored as the
// owner's answer, in every shape it was seen, and leaves other rows alone.
func TestMigrationVoidsCompactionJudgments(t *testing.T) {
	s, _ := newTestStore(t)
	rewind := migrationVersion(t, "void idle compaction judgments") - 1
	if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version>?`, rewind); err != nil {
		t.Fatal(err)
	}
	seedJudgment(t, s, "so_bare", protocol.StandingKindAnswer, compactionText)
	seedJudgment(t, s, "so_escaped", protocol.StandingKindAnswer, `\`+compactionText)
	seedJudgment(t, s, "so_qa", protocol.StandingKindAnswer, "Q: tenebrae-strats stopped with work left: triage / A: "+compactionText)
	seedJudgment(t, s, "so_qa_escaped", protocol.StandingKindAnswer, `Q: stopped / A: \`+compactionText)
	seedJudgment(t, s, "so_normal", protocol.StandingKindAnswer, "Q: compact the db? / A: yes, compact it")
	seedJudgment(t, s, "so_mention", protocol.StandingKindAnswer, "Q: what did the agent type? / A: it typed /compact Keep: the current task earlier")

	if err := s.migrate(); err != nil {
		t.Fatal(err)
	}
	rows, err := s.db.Query(`SELECT id,voided_at,void_reason FROM standing_orders ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	voided := map[string]string{}
	for rows.Next() {
		var id, reason string
		var at int64
		if err := rows.Scan(&id, &at, &reason); err != nil {
			t.Fatal(err)
		}
		if at != 0 {
			voided[id] = reason
		} else if reason != "" {
			t.Fatalf("%s has a reason but is not voided", id)
		}
	}
	const reason = "agent-net idle compaction prompt, not the owner (a_64e7e272d7794cec)"
	for _, id := range []string{"so_bare", "so_escaped", "so_qa", "so_qa_escaped"} {
		if voided[id] != reason {
			t.Fatalf("%s: void reason %q, want %q", id, voided[id], reason)
		}
	}
	if len(voided) != 4 {
		t.Fatalf("voided %v, want only the four compaction rows", voided)
	}
	if got := judgmentIDs(mustFind(t, s, protocol.JudgmentsFindRequest{Query: "compact"})); len(got) != 2 {
		t.Fatalf("search after the migration: %v, want the two normal rows", got)
	}
}

// The gateway never stores a slash command as the owner's words.
func TestSlashCommandIsNotStoredAsOwnerWords(t *testing.T) {
	s, _ := newTestStore(t)
	item := protocol.InboxItem{ID: "i_slash", Text: "stopped with work left: triage", Kind: protocol.AskKindQuestion}
	for _, words := range []string{compactionText, "  " + compactionText, `\` + compactionText} {
		if err := s.recordOwnerWords(s.db, item, words); err != nil {
			t.Fatal(err)
		}
	}
	if rows := mustFind(t, s, protocol.JudgmentsFindRequest{Ref: "i_slash"}); len(rows) != 0 {
		t.Fatalf("slash command stored as owner words: %+v", rows)
	}
	if err := s.recordOwnerWords(s.db, item, "yes, triage it"); err != nil {
		t.Fatal(err)
	}
	if rows := mustFind(t, s, protocol.JudgmentsFindRequest{Ref: "i_slash"}); len(rows) != 1 {
		t.Fatalf("a normal answer must be stored: %+v", rows)
	}

	_, err := s.AddJudgment(protocol.JudgmentAddRequest{Kind: protocol.StandingKindAnswer, Text: compactionText, Ref: "chat:s_1"})
	if err == nil {
		t.Fatal("a slash command was recorded as an owner answer")
	}
}
