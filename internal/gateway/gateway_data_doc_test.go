package gateway

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// gatewayDataDocPath is the page that says what each gateway table holds. A
// person writes it, so this test keeps its table headings in step with the
// schema: a migration that adds, renames or drops a table fails here until the
// page follows.
const gatewayDataDocPath = "../../docs/gateway-data.md"

// ftsShadowSuffixes are the tables SQLite creates behind an FTS5 virtual table.
// The page leaves them out: a reader uses the virtual table, not its storage.
var ftsShadowSuffixes = []string{"_config", "_content", "_data", "_docsize", "_idx"}

// journeyStepPattern matches a numbered journey step that starts with a table
// name in backticks, for example: 1. `sessions`: The gateway checks ...
var journeyStepPattern = regexp.MustCompile("^[0-9]+\\. `([a-z0-9_]+)`")

// liveSchemaTables lists the tables of a migrated store, without SQLite's own
// tables and without the storage tables of FTS5 virtual tables.
func liveSchemaTables(t *testing.T, s *Store) map[string]bool {
	t.Helper()
	rows, err := s.db.Query(`SELECT name, sql FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type entry struct{ name, sql string }
	var all []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.name, &e.sql); err != nil {
			t.Fatal(err)
		}
		all = append(all, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	shadow := map[string]bool{}
	for _, e := range all {
		if !strings.HasPrefix(e.sql, "CREATE VIRTUAL TABLE") {
			continue
		}
		for _, suffix := range ftsShadowSuffixes {
			shadow[e.name+suffix] = true
		}
	}
	tables := map[string]bool{}
	for _, e := range all {
		if !shadow[e.name] {
			tables[e.name] = true
		}
	}
	return tables
}

// gatewayDataDocLines reads the page. A missing page fails the test at once.
func gatewayDataDocLines(t *testing.T) []string {
	t.Helper()
	body, err := os.ReadFile(gatewayDataDocPath)
	if err != nil {
		t.Fatalf("read %s: %v", gatewayDataDocPath, err)
	}
	return strings.Split(string(body), "\n")
}

// Every "### " heading on the page names one table, and every table of the
// schema has one. The page has no other third-level headings.
func TestGatewayDataDocHasOneHeadingPerTable(t *testing.T) {
	s, _ := newTestStore(t)
	live := liveSchemaTables(t, s)
	if len(live) == 0 {
		t.Fatal("the migrated store has no tables")
	}
	headed := map[string]bool{}
	for _, line := range gatewayDataDocLines(t) {
		if !strings.HasPrefix(line, "### ") {
			continue
		}
		name := strings.TrimSpace(strings.TrimPrefix(line, "### "))
		headed[name] = true
		if !live[name] {
			t.Errorf("%s has a heading for %q, which is not a table of the schema", gatewayDataDocPath, name)
		}
	}
	for name := range live {
		if !headed[name] {
			t.Errorf("table %q has no heading in %s", name, gatewayDataDocPath)
		}
	}
}

// The journeys name the tables they touch. A journey step that names a table
// the schema no longer has fails here.
func TestGatewayDataDocJourneysNameLiveTables(t *testing.T) {
	s, _ := newTestStore(t)
	live := liveSchemaTables(t, s)
	steps := 0
	for _, line := range gatewayDataDocLines(t) {
		m := journeyStepPattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		steps++
		if !live[m[1]] {
			t.Errorf("%s has a journey step on %q, which is not a table of the schema", gatewayDataDocPath, m[1])
		}
	}
	if steps == 0 {
		t.Fatalf("%s has no journey steps in the form: 1. `table`: ...", gatewayDataDocPath)
	}
}
