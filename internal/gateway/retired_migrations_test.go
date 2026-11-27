package gateway

import (
	"path/filepath"
	"testing"
)

// retiredTables are the tables of the removed subsystems: session dues,
// learned suggestions, findings and permission proposals.
var retiredTables = []string{"session_dues", "learned_suggestions", "findings", "permission_proposals"}

// retiredDropVersion is the version of the retired tables drop.
func retiredDropVersion(t *testing.T) int {
	t.Helper()
	return migrationVersion(t, "retired subsystems: session dues")
}

// A database from before the drop keeps its other data: the retired tables go,
// the automatic playbook notes that findings made go unless pinned, and the
// owner's notes stay.
func TestRetiredTablesDropKeepsOwnerNotes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.db")
	old := migrateOnly(t, path, retiredDropVersion(t)-1)
	for _, q := range []string{
		`INSERT INTO session_dues(id,session_id,what,created_at) VALUES('d_1','s_1','flip',1)`,
		`INSERT INTO learned_suggestions(node,field,edge_kind,kind,phrase,evidence,status,severity,created_at,ready_at) VALUES('demo','rules','','add','p','[]','pending','minor',1,1)`,
		`INSERT INTO findings(subject_kind,subject,finding,evidence,reporter,node_id,at,status) VALUES('model','terra','slow','[]','s_1','n_1',1,'new')`,
		`INSERT INTO permission_proposals(id,targets,reason,proposed_by,created_at) VALUES('pp_1','[]','r','s_1',1)`,
		`INSERT INTO playbook(subject_kind,subject,harness,note,source,support,track_line,pinned,stale,created_at,updated_at) VALUES
			('model','terra','','auto note','auto',3,'',0,0,1,1),
			('model','luna','','pinned auto note','auto',3,'',1,0,1,1),
			('model','sol','','owner note','owner',0,'',0,0,1,1)`,
	} {
		if _, err := old.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, name := range retiredTables {
		if tableExists(t, s, name) {
			t.Errorf("table %s is still in the schema", name)
		}
	}
	notes, err := s.PlaybookNotes()
	if err != nil {
		t.Fatal(err)
	}
	kept := map[string]bool{}
	for _, n := range notes {
		kept[n.Subject] = true
	}
	if len(notes) != 2 || !kept["luna"] || !kept["sol"] {
		t.Fatalf("playbook notes after the drop: %+v", notes)
	}
}
