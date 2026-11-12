package gateway

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/dbfile"
	"github.com/niparis/agent-net/internal/sqlitecache"
)

// migrateOnly opens the file like OpenStore does but applies only the first
// steps and skips the start-up repairs, which need the newest schema. It is
// how a test makes the file an older release left behind.
func migrateOnly(t *testing.T, path string, steps int) *Store {
	t.Helper()
	if err := dbfile.Create(path); err != nil {
		t.Fatal(err)
	}
	connector, err := sqlitecache.New("file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)", stmtCacheSize)
	if err != nil {
		t.Fatal(err)
	}
	wdb := sql.OpenDB(connector)
	wdb.SetMaxOpenConns(1)
	s := &Store{db: &storeDB{w: wdb}, connector: connector, path: path, now: time.Now, logf: func(string, ...any) {}, buckets: map[string]*bucket{}}
	full := migrations
	migrations = full[:steps]
	defer func() { migrations = full }()
	if err := s.migrate(); err != nil {
		s.Close()
		t.Fatal(err)
	}
	return s
}

// schemaShape lists every table, index and trigger with the columns of each
// table, so two databases can be compared without caring about row order.
func schemaShape(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT type, name, tbl_name FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY type, name`)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	var tables []string
	for rows.Next() {
		var typ, name, tbl string
		if err := rows.Scan(&typ, &name, &tbl); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%s %s on %s", typ, name, tbl))
		if typ == "table" {
			tables = append(tables, name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	for _, table := range tables {
		cols, err := db.Query(`SELECT name, type, "notnull", COALESCE(dflt_value, ''), pk FROM pragma_table_info(?)`, table)
		if err != nil {
			t.Fatal(err)
		}
		for cols.Next() {
			var name, typ, dflt string
			var notnull, pk int
			if err := cols.Scan(&name, &typ, &notnull, &dflt, &pk); err != nil {
				t.Fatal(err)
			}
			out = append(out, fmt.Sprintf("column %s.%s %s notnull=%d default=%q pk=%d", table, name, typ, notnull, dflt, pk))
		}
		cols.Close()
	}
	sort.Strings(out)
	return out
}

// A new database and one upgraded from an old version must end with the same
// schema. The fresh file is migrated for real (no test template). Each
// upgraded file first gets only the early steps, like a file that an older
// release left behind, and then opens with the full list.
func TestFreshAndUpgradedSchemasMatch(t *testing.T) {
	fresh, err := openStore(filepath.Join(t.TempDir(), "fresh.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if v, err := fresh.SchemaVersion(); err != nil || v != len(migrations) {
		t.Fatalf("fresh schema version = %d, %v; want %d", v, err, len(migrations))
	}

	// The test template must give the same schema as a real migration, and
	// each seeded file must be its own copy.
	seeded, err := OpenStore(filepath.Join(t.TempDir(), "seeded.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer seeded.Close()
	if fmt.Sprint(schemaShape(t, fresh.db.w)) != fmt.Sprint(schemaShape(t, seeded.db.w)) {
		t.Fatal("the seeded database has another schema than a migrated one")
	}
	other, err := OpenStore(filepath.Join(t.TempDir(), "other.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	code, err := seeded.CreateEnrollCode(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if valid, err := other.EnrollCodeValid(code); err != nil || valid {
		t.Fatalf("a seeded file shares data with another: valid=%v err=%v", valid, err)
	}

	for _, oldVersion := range []int{1, len(migrations) / 2} {
		t.Run(fmt.Sprintf("from_%d", oldVersion), func(t *testing.T) {
			// The older release is the first steps run by the real migrator.
			path := filepath.Join(t.TempDir(), "old.db")
			old := migrateOnly(t, path, oldVersion)
			if v, err := old.SchemaVersion(); err != nil || v != oldVersion {
				t.Fatalf("old schema version = %d, %v; want %d", v, err, oldVersion)
			}
			if err := old.Close(); err != nil {
				t.Fatal(err)
			}
			upgraded, err := openStore(path, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer upgraded.Close()
			if v, err := upgraded.SchemaVersion(); err != nil || v != len(migrations) {
				t.Fatalf("upgraded schema version = %d, %v; want %d", v, err, len(migrations))
			}
			want, got := schemaShape(t, fresh.db.w), schemaShape(t, upgraded.db.w)
			if len(want) < 100 {
				t.Fatalf("schema listing has only %d entries", len(want))
			}
			if fmt.Sprint(want) != fmt.Sprint(got) {
				for i := 0; i < len(want) && i < len(got); i++ {
					if want[i] != got[i] {
						t.Fatalf("schemas differ at %d: fresh %q, upgraded %q", i, want[i], got[i])
					}
				}
				t.Fatalf("schemas differ in length: fresh %d, upgraded %d", len(want), len(got))
			}
		})
	}
}
