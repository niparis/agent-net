package sqlitecache

import (
	"database/sql"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// openColumns opens the tables of openTables and a table d with two columns
// that the tests change one at a time, with one row.
func openColumns(t testing.TB, size int) (*sql.DB, *Connector) {
	t.Helper()
	db, c := openTables(t, size, "")
	mustExec(t, db, `CREATE TABLE d (n INTEGER PRIMARY KEY, x TEXT NOT NULL DEFAULT '', y TEXT NOT NULL DEFAULT '')`)
	return db, c
}

// movedKeys returns the sorted keys whose stamp changed while run ran.
func movedKeys(t testing.TB, c *Connector, keys []string, run func()) []string {
	t.Helper()
	before := map[string]uint64{}
	for _, k := range keys {
		before[k] = c.Stamp(k)
	}
	run()
	var out []string
	for _, k := range keys {
		if c.Stamp(k) != before[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// An UPDATE moves the stamp of the table and of the columns whose value it
// changes. A column it leaves as it was keeps its stamp, so a cache that
// depends on that column keeps its value.
func TestTablesColumnStampMovesOnlyForTheColumnsAnUpdateChanges(t *testing.T) {
	db, c := openColumns(t, 16)
	keys := []string{"d", "d.x", "d.y", "a"}
	cases := []struct {
		name string
		sql  string
		want []string
	}{
		{"set x", `UPDATE d SET x='1' WHERE n=1`, []string{"d", "d.x"}},
		{"set y", `UPDATE d SET y='1' WHERE n=1`, []string{"d", "d.y"}},
		{"set both", `UPDATE d SET x='2', y='2' WHERE n=1`, []string{"d", "d.x", "d.y"}},
		// The row changed, but no value did: the table moves, no column does.
		{"set x to the value it has", `UPDATE d SET x=x WHERE n=1`, []string{"d"}},
		{"update that matches no row", `UPDATE d SET x='3' WHERE n=99`, nil},
		// An INSERT may add a row, and a row is every column of the table.
		{"insert", `INSERT INTO d(n,x,y) VALUES (3,'','')`, []string{"d", "d.x", "d.y"}},
		{"delete", `DELETE FROM d WHERE n=2`, []string{"d", "d.x", "d.y"}},
		// An upsert is an INSERT to SQLite, but on a row that exists it only
		// updates the columns whose values change.
		{"upsert that changes x", `INSERT INTO d(n,x,y) VALUES (1,'u','') ON CONFLICT(n) DO UPDATE SET x=excluded.x, y=excluded.y`, []string{"d", "d.x"}},
		{"upsert that changes nothing", `INSERT INTO d(n,x,y) VALUES (1,'','') ON CONFLICT(n) DO UPDATE SET x=excluded.x, y=excluded.y`, []string{"d"}},
	}
	// Every run starts from the same two rows.
	reset := func() {
		mustExec(t, db, `DELETE FROM d`)
		mustExec(t, db, `INSERT INTO d(n,x,y) VALUES (1,'',''),(2,'','')`)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Twice: the second run uses the kept statement.
			for i := 0; i < 2; i++ {
				reset()
				got := movedKeys(t, c, keys, func() { mustExec(t, db, tc.sql) })
				if !reflect.DeepEqual(nonNil(got), nonNil(tc.want)) {
					t.Fatalf("run %d moved %v, want %v", i, got, tc.want)
				}
			}
		})
	}
}

// A trigger writes the tables it names. Its writes move the stamp of the table
// it writes, for an UPDATE of the column the trigger watches.
func TestTablesTriggerWritesMoveTheColumnsTheyTouch(t *testing.T) {
	db, c := openColumns(t, 16)
	mustExec(t, db, `INSERT INTO d(n,x,y) VALUES (1,'','')`)
	mustExec(t, db, `CREATE TRIGGER d_x AFTER UPDATE OF x ON d BEGIN INSERT OR REPLACE INTO a(n,v) VALUES (new.n+100,'t'); END`)
	keys := []string{"d", "d.x", "d.y", "a", "a.v"}
	if got := movedKeys(t, c, keys, func() { mustExec(t, db, `UPDATE d SET x='1' WHERE n=1`) }); !reflect.DeepEqual(got, []string{"a", "a.v", "d", "d.x"}) {
		t.Fatalf("set x moved %v", got)
	}
	if got := movedKeys(t, c, keys, func() { mustExec(t, db, `UPDATE d SET y='1' WHERE n=1`) }); !reflect.DeepEqual(got, []string{"d", "d.y"}) {
		t.Fatalf("set y moved %v", got)
	}
}

// A statement that changes no row moves no column stamp either.
func TestTablesColumnStampIsUnmovedByAStatementThatChangesNothing(t *testing.T) {
	db, c := openColumns(t, 16)
	keys := []string{"d", "d.x"}
	got := movedKeys(t, c, keys, func() { mustExec(t, db, `DELETE FROM d WHERE n=99`) })
	if len(got) != 0 {
		t.Fatalf("a delete of no row moved %v", got)
	}
}

// The observer names the columns a statement reads, so a test can check the
// columns a cached read depends on. A statement that reads no column names none.
func TestTablesObserverReportsColumnsRead(t *testing.T) {
	db, c := openColumns(t, 16)
	var seen []Statement
	c.SetObserver(func(st Statement) { seen = append(seen, st) })
	defer c.SetObserver(nil)
	var x string
	if err := db.QueryRow(`SELECT x FROM d WHERE y<>''`).Scan(&x); err != nil && err != sql.ErrNoRows {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM d`).Scan(new(int)); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 {
		t.Fatalf("observed %d statements, want 2", len(seen))
	}
	if got := seen[0].ReadColumns; !reflect.DeepEqual(got, []string{"d.x", "d.y"}) {
		t.Fatalf("columns read by the first statement: %v", got)
	}
	if got := seen[1].Reads; !reflect.DeepEqual(got, []string{"d"}) || len(seen[1].ReadColumns) != 0 {
		t.Fatalf("second statement reads %v, columns %v", got, seen[1].ReadColumns)
	}
	for _, c := range seen[0].ReadColumns {
		if !strings.Contains(c, ".") {
			t.Fatalf("column %q is not named table.column", c)
		}
	}
}

// Dropping a column moves the columns after it to new positions. The hook
// numbers columns by position, so a write after the change must count the
// column it really changed.
func TestTablesColumnStampFollowsADroppedColumn(t *testing.T) {
	db, c := openColumns(t, 16)
	mustExec(t, db, `INSERT INTO d(n,x,y) VALUES (1,'','')`)
	mustExec(t, db, `UPDATE d SET x='1' WHERE n=1`) // the names of d are known now
	mustExec(t, db, `ALTER TABLE d DROP COLUMN x`)  // y is the second column now
	for i := 0; i < 2; i++ {
		got := movedKeys(t, c, []string{"d.x", "d.y"}, func() { mustExec(t, db, `UPDATE d SET y=? WHERE n=1`, fmt.Sprint(i)) })
		if !reflect.DeepEqual(got, []string{"d.y"}) {
			t.Fatalf("run %d moved %v, want [d.y]", i, got)
		}
	}
}
