package gateway

import (
	"fmt"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestAskMarkRefusesUnknownMark(t *testing.T) {
	s, _, _, asker, _ := chiefRig(t)
	id := chiefAsk(t, s, asker, "Merge PR 3?")
	if err := s.MarkAsk(protocol.AskMarkRequest{Ask: id, Mark: "bogus"}, "owner"); err == nil {
		t.Fatal("an unknown mark was stored")
	}
	if err := s.MarkAsk(protocol.AskMarkRequest{Ask: "i_missing", Mark: protocol.MarkShouldHaveAsked}, "owner"); err == nil {
		t.Fatal("a mark on an unknown ask was stored")
	}
}

// Marks of the old chief_corrections table move into the judgment store, and the
// drop migration then removes the table. A replay does not copy a row twice.
func TestChiefCorrectionsMigrateToJudgments(t *testing.T) {
	s, _ := newTestStore(t)
	rewind := migrationVersion(t, "Owner marks move to the judgment store") - 1
	for i := 0; i < 2; i++ {
		recreateDeadTables(t, s)
		for j, text := range []string{"first mark", "second mark"} {
			if _, err := s.db.Exec(`INSERT INTO chief_corrections(at,mark,text,area,ref) VALUES(?,?,?,?,?)`,
				int64(1700000000+j)*1e9, protocol.MarkDidntNeedMe, text, "demo", fmt.Sprintf("i_%d", j)); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version>?`, rewind); err != nil {
			t.Fatal(err)
		}
		if err := replayMigrations(t, s); err != nil {
			t.Fatal(err)
		}
		found, err := s.FindJudgments(protocol.JudgmentsFindRequest{Kind: protocol.StandingKindCorrection, Area: "chief"})
		if err != nil {
			t.Fatal(err)
		}
		if len(found) != 2 || found[0].Text != "second mark" || found[1].Text != "first mark" || found[0].Ref != "i_1" || found[0].SavedAt != 1700000001 {
			t.Fatalf("round %d: %+v", i, found)
		}
		if tableExists(t, s, "chief_corrections") {
			t.Fatalf("round %d: the drop migration kept chief_corrections", i)
		}
	}
}
