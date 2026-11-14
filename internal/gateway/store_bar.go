package gateway

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// A project's bar is the short list of rules that every worker and reviewer of
// the project checks a diff against. The project's lead and the owner (and the
// CTO, who acts for the owner) write it. Every other session only reads it,
// in the graph, in the worker brief and in the accept and review tasks.

// barDB is what the bar reads need, so they run on the store or in a
// transaction.
type barDB interface {
	Query(string, ...any) (*sql.Rows, error)
}

func barID(n int) string { return fmt.Sprintf("B%d", n) }

// barNumber reads "B3" or "b3".
func barNumber(id string) (int, bool) {
	id = strings.TrimSpace(id)
	if len(id) < 2 || (id[0] != 'B' && id[0] != 'b') {
		return 0, false
	}
	n, err := strconv.Atoi(id[1:])
	return n, err == nil && n > 0
}

// barRules lists the rules of one project in order.
func barRules(q barDB, project string) ([]protocol.BarRule, error) {
	rows, err := q.Query(`SELECT n,text,source,created_at FROM project_bar_rules WHERE project=? ORDER BY n`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []protocol.BarRule
	for rows.Next() {
		var n int
		var r protocol.BarRule
		if err = rows.Scan(&n, &r.Text, &r.Source, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.ID = barID(n)
		out = append(out, r)
	}
	return out, rows.Err()
}

// BarRules returns the bar of a project, empty when it has none.
func (s *Store) BarRules(project string) ([]protocol.BarRule, error) {
	return barRules(s.db, project)
}

// allBarRules reads every project's bar in one query, for the graph.
func (s *Store) allBarRules() (map[string][]protocol.BarRule, error) {
	rows, err := s.db.Query(`SELECT project,n,text,source,created_at FROM project_bar_rules ORDER BY project,n`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]protocol.BarRule{}
	for rows.Next() {
		var project string
		var n int
		var r protocol.BarRule
		if err = rows.Scan(&project, &n, &r.Text, &r.Source, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.ID = barID(n)
		out[project] = append(out[project], r)
	}
	return out, rows.Err()
}

// barEditor works out who the caller is and which project it may edit. The
// owner and the CTO name the project. A lead edits its own project only. A
// worker never edits, even when its caller is the lead.
func (s *Store) barEditor(session, named string) (project, source string, lead bool, err error) {
	sessProject, agent, harness, worker, err := s.sessionIdentity(session)
	if err == sql.ErrNoRows {
		return "", "", false, reject("unknown session")
	}
	if err != nil {
		return "", "", false, err
	}
	switch {
	case worker:
		return "", "", false, reject("a worker cannot change a project's bar")
	case protocol.IsOwner(harness), harness == "cto":
		if named == "" {
			return "", "", false, reject("name the project")
		}
		g, err := s.Graph()
		if err != nil {
			return "", "", false, err
		}
		if chartedProject(g, named) != named {
			return "", "", false, reject("unknown project %q", named)
		}
		return named, protocol.BarSourceOwner, false, nil
	case harness == "" || harness == "bot":
		return "", "", false, reject("only the project's lead, the owner or the CTO may change a project's bar")
	}
	if sessProject == "" || agent == "" {
		return "", "", false, reject("this session has no project")
	}
	g, err := s.Graph()
	if err != nil {
		return "", "", false, err
	}
	project = chartedProject(g, sessProject)
	if named != "" && named != project {
		return "", "", false, reject("a lead changes the bar of its own project only")
	}
	isLead, err := sessionLeadsNamedRepoQ(s.db, project, agent, session)
	if err != nil {
		return "", "", false, err
	}
	if isLead {
		return project, protocol.BarSourceLead, true, nil
	}
	return "", "", false, reject("only the project's lead, the owner or the CTO may change a project's bar")
}

// EditBar adds, edits and removes rules of a project's bar in one transaction.
// Either every change in the call applies or none does.
func (s *Store) EditBar(session string, q protocol.BarEditRequest) (protocol.BarEditResult, error) {
	var res protocol.BarEditResult
	if len(q.Add)+len(q.Edit)+len(q.Remove) == 0 {
		return res, reject("nothing to change: give add, edit or remove")
	}
	project, source, lead, err := s.barEditor(session, q.Project)
	if err != nil {
		return res, err
	}
	adds := make([]string, 0, len(q.Add))
	for _, t := range q.Add {
		text, err := protocol.CleanBarRule(t)
		if err != nil {
			return res, reject("%v", err)
		}
		adds = append(adds, text)
	}
	edits := map[int]string{}
	for _, e := range q.Edit {
		n, ok := barNumber(e.ID)
		if !ok {
			return res, reject("bad rule ID %q", e.ID)
		}
		text, err := protocol.CleanBarRule(e.Text)
		if err != nil {
			return res, reject("%v", err)
		}
		edits[n] = text
	}
	removes := map[int]bool{}
	for _, id := range q.Remove {
		n, ok := barNumber(id)
		if !ok {
			return res, reject("bad rule ID %q", id)
		}
		removes[n] = true
	}
	tx, err := s.db.Begin()
	if err != nil {
		return res, err
	}
	defer tx.Rollback()
	have, err := barRules(tx, project)
	if err != nil {
		return res, err
	}
	exists := map[int]bool{}
	for _, r := range have {
		n, _ := barNumber(r.ID)
		exists[n] = true
	}
	for n := range edits {
		if !exists[n] || removes[n] {
			return res, reject("no rule %s to edit", barID(n))
		}
	}
	for n := range removes {
		if !exists[n] {
			return res, reject("no rule %s to remove", barID(n))
		}
	}
	// A lead that fills an empty bar is seeding it.
	if lead && len(have) == 0 {
		source = protocol.BarSourceSeeded
	}
	now := s.now().UnixNano()
	for n, text := range edits {
		if _, err = tx.Exec(`UPDATE project_bar_rules SET text=? WHERE project=? AND n=?`, text, project, n); err != nil {
			return res, err
		}
	}
	for n := range removes {
		if _, err = tx.Exec(`DELETE FROM project_bar_rules WHERE project=? AND n=?`, project, n); err != nil {
			return res, err
		}
	}
	if len(adds) > 0 {
		next := 1
		err = tx.QueryRow(`SELECT next FROM project_bar_next WHERE project=?`, project).Scan(&next)
		if err != nil && err != sql.ErrNoRows {
			return res, err
		}
		if err == sql.ErrNoRows {
			// A first call, or a bar written before the counter: go past every rule.
			for n := range exists {
				if n >= next {
					next = n + 1
				}
			}
		}
		for _, text := range adds {
			if _, err = tx.Exec(`INSERT INTO project_bar_rules(project,n,text,source,created_at) VALUES(?,?,?,?,?)`, project, next, text, source, now); err != nil {
				return res, err
			}
			next++
		}
		if _, err = tx.Exec(`INSERT INTO project_bar_next(project,next) VALUES(?,?) ON CONFLICT(project) DO UPDATE SET next=excluded.next`, project, next); err != nil {
			return res, err
		}
	}
	rules, err := barRules(tx, project)
	if err != nil {
		return res, err
	}
	if err = tx.Commit(); err != nil {
		return res, err
	}
	s.charterNotify()
	return protocol.BarEditResult{Project: project, Rules: rules}, nil
}

// barAskText asks the project's lead to draft its bar, once per version of
// AGENTS.md. It is "" when the project has a bar, when the session is not the
// lead's, or when this version was already asked about.
func (s *Store) barAskText(tx *sql.Tx, session string, c resolvedContext, hash string, record func(level, scope, hash string) error) (string, error) {
	if hash == "" || c.Repo == "" || c.Agent == "" || c.CallerContext {
		return "", nil
	}
	asked, err := draftAsked(tx, "bar", c.Repo, hash)
	if err != nil || asked {
		return "", err
	}
	rules, err := barRules(tx, c.Repo)
	if err != nil || len(rules) > 0 {
		return "", err
	}
	lead, err := sessionLeadsNamedRepoQ(tx, c.Repo, c.Agent, session)
	if err != nil {
		return "", err
	}
	if !lead {
		return "", nil
	}
	if err = record("bar", c.Repo, hash); err != nil {
		return "", err
	}
	return fmt.Sprintf("[agent-net] Project rules request. You lead %s, and it has no project rules yet.\n"+
		"Workers read these rules in their brief and check their diff against each one. The accept and review steps check the same list.\n"+
		"Read AGENTS.md and README.md. Then write 5 to 15 short rules with one agent_bar_edit call (add: [...]). "+
		"Each rule is one line, concrete and checkable against a diff. Take them from what AGENTS.md and README.md say, and from rules reviewers would reject a change for. Do not guess.\n"+
		"agent-net asks once, and again only if AGENTS.md changes. You and the owner can change the rules later with agent_bar_edit.", c.Repo), nil
}
