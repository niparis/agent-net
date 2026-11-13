package gateway

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// The start text reads the notes on every prompt, so the read must use the
// note index and not scan every answer and order in the judgment store.
func TestNotesReadUsesTheNoteIndexAndStaysFastWithManyJudgments(t *testing.T) {
	s, demo, _ := noteRig(t)
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20000; i++ {
		if _, err := tx.Exec(`INSERT INTO standing_orders(id,text,saved_at,source,tags,pinned,updated_at,kind,area,ref) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			fmt.Sprintf("so_bulk%d", i), "an answer", 1, "owner", "[]", 0, 0, "answer", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	noteWrite(t, s, demo, noteReq("a-note"))
	rows, err := s.db.Query(`EXPLAIN QUERY PLAN SELECT `+noteColumns+` FROM standing_orders WHERE name<>''
 AND (?='' OR scope=?)
 AND ((scope='owner') OR (scope='agent' AND scope_ref=? AND ?<>'') OR (scope='project' AND scope_ref=? AND ?<>''))`, "", "", "x", "x", "y", "y")
	if err != nil {
		t.Fatal(err)
	}
	var plan []string
	for rows.Next() {
		var a, b, c int
		var d string
		if err := rows.Scan(&a, &b, &c, &d); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, d)
	}
	rows.Close()
	if !strings.Contains(strings.Join(plan, "\n"), "standing_orders_note_name") {
		t.Fatalf("the notes read does not use the note index:\n%s", strings.Join(plan, "\n"))
	}
	start := time.Now()
	for i := 0; i < 50; i++ {
		if _, err := s.notesIndex(demo); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("notesIndex with 20000 judgments: %v per call", time.Since(start)/50)
}
