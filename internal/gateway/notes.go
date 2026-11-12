package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/niparis/agent-net/internal/markers"
	"github.com/niparis/agent-net/internal/protocol"
)

// Gateway notes (decision 0157) are rows of standing_orders that have a name.
// They share the owner judgment store, so the owner sees and deletes them where
// he sees his judgments. A note belongs to a scope: one agent, one project or
// the owner (every session).

// gatewayNotesMigration adds the columns of a note. They are added in the
// migration loop so replaying the migration is safe. The unique index keeps one
// note per name in a scope; rows without a name (orders, answers) are free.
const gatewayNotesMigration = `
-- Gateway notes: scope, scope_ref, name, description, used_at and uses on the
-- owner judgment store (columns added replay-safely below).
SELECT 1 /* gateway notes columns */;
CREATE UNIQUE INDEX IF NOT EXISTS standing_orders_note_name ON standing_orders(scope, scope_ref, name) WHERE name<>'';
-- Reading a note only changes used_at and uses. That is not a new version, so
-- the history and the search index are left alone.
DROP TRIGGER IF EXISTS standing_orders_au;
CREATE TRIGGER standing_orders_au AFTER UPDATE ON standing_orders
 WHEN old.text IS NOT new.text OR old.tags IS NOT new.tags OR old.kind IS NOT new.kind OR old.area IS NOT new.area OR old.ref IS NOT new.ref OR old.description IS NOT new.description BEGIN
 INSERT INTO standing_orders_fts(standing_orders_fts,rowid,text,tags) VALUES('delete',old.rowid,old.text,old.tags);
 INSERT INTO standing_orders_fts(rowid,text,tags) VALUES(new.rowid,new.text,new.tags);
 INSERT INTO standing_order_history(order_id,text,tags,changed_at,kind,area,ref) VALUES(old.id,old.text,old.tags,unixepoch(),old.kind,old.area,old.ref); END;
`

// notesUseTop is how many of the best matches of a search count as a use.
const notesUseTop = 3

const noteColumns = `id,text,saved_at,source,tags,pinned,updated_at,kind,area,ref,scope,scope_ref,name,description,used_at,uses`

func scanNote(sc interface{ Scan(...any) error }) (protocol.StandingOrder, error) {
	var o protocol.StandingOrder
	var tags string
	err := sc.Scan(&o.ID, &o.Text, &o.SavedAt, &o.Source, &tags, &o.Pinned, &o.UpdatedAt, &o.Kind, &o.Area, &o.Ref, &o.Scope, &o.ScopeRef, &o.Name, &o.Description, &o.UsedAt, &o.Uses)
	if err == nil {
		json.Unmarshal([]byte(tags), &o.Tags)
	}
	return o, err
}

func noteOf(o protocol.StandingOrder, now time.Time) protocol.Note {
	return protocol.Note{ID: o.ID, Name: o.Name, Description: o.Description, Body: o.Text, Scope: o.Scope, Kind: o.Kind,
		Source: o.Source, SavedAt: o.SavedAt, UsedAt: o.UsedAt, Uses: o.Uses, Stale: noteStale(o, now)}
}

// noteStale is true when nothing read or wrote the note for NotesStaleDays.
func noteStale(o protocol.StandingOrder, now time.Time) bool {
	last := max(o.UsedAt, o.SavedAt, o.UpdatedAt)
	return o.Name != "" && now.Unix()-last > int64(protocol.NotesStaleDays)*86400
}

// noteScopes names the agent and the project whose notes a session sees. Either
// may be empty: a session with no agent or no project has no notes of that
// scope. A worker sees the notes of the agent that called it.
func (s *Store) noteScopes(session string) (agent, project string, err error) {
	c, err := s.resolveContext(session, "", false)
	if err != nil {
		return "", "", err
	}
	agent = c.Agent
	if agent == "" {
		if _, _, harness, _, e := s.sessionIdentity(session); e == nil && harness == "cto" {
			agent = "cto"
		}
	}
	return agent, c.Project, nil
}

// noteRefFor is the scope_ref a write or find in this scope uses.
func noteRefFor(scope, agent, project string) (string, error) {
	switch scope {
	case protocol.NoteScopeAgent:
		if agent == "" {
			return "", errors.New("this session has no agent, so it has no agent notes: use scope project or owner")
		}
		return agent, nil
	case protocol.NoteScopeProject:
		if project == "" {
			return "", errors.New("this session has no project, so it has no project notes: use scope agent or owner")
		}
		return project, nil
	}
	return "", nil
}

// visibleNotes loads the named notes a session sees, optionally one scope only.
func (s *Store) visibleNotes(agent, project, scope string) ([]protocol.StandingOrder, error) {
	rows, err := s.db.Query(`SELECT `+noteColumns+` FROM standing_orders WHERE name<>''
 AND (?='' OR scope=?)
 AND ((scope='owner') OR (scope='agent' AND scope_ref=? AND ?<>'') OR (scope='project' AND scope_ref=? AND ?<>''))`,
		scope, scope, agent, agent, project, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []protocol.StandingOrder
	for rows.Next() {
		o, err := scanNote(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// WriteNote adds a note, or updates the one with the same name in the same
// scope. It answers with up to NotesSimilarMax other visible notes that look
// alike, so the agent can merge into one instead of adding another.
func (s *Store) WriteNote(session string, q protocol.NoteWriteRequest) (protocol.NoteWriteResult, error) {
	return s.writeNote(session, q, time.Now())
}

func (s *Store) writeNote(session string, q protocol.NoteWriteRequest, now time.Time) (protocol.NoteWriteResult, error) {
	var res protocol.NoteWriteResult
	name, err := protocol.CleanNoteName(q.Name)
	if err != nil {
		return res, err
	}
	desc, err := protocol.CleanNoteDescription(q.Description)
	if err != nil {
		return res, err
	}
	body, err := protocol.CleanStandingText(q.Body)
	if err != nil {
		return res, errors.New("a note needs a body")
	}
	scope := firstNonEmpty(q.Scope, protocol.NoteScopeAgent)
	if !protocol.ValidNoteScope(scope) {
		return res, errors.New("scope must be agent, project or owner")
	}
	kind := firstNonEmpty(q.Kind, protocol.StandingKindFact)
	if kind == protocol.StandingKindRule {
		return res, errors.New("an owner rule is saved by the owner, not by an agent: use kind feedback, fact or reference")
	}
	if !protocol.ValidNoteKind(kind) {
		return res, errors.New("kind must be feedback, fact or reference")
	}
	agent, project, err := s.noteScopes(session)
	if err != nil {
		return res, err
	}
	ref, err := noteRefFor(scope, agent, project)
	if err != nil {
		return res, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return res, err
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRow(`SELECT id FROM standing_orders WHERE scope=? AND scope_ref=? AND name=?`, scope, ref, name).Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		id = protocol.NewID("so")
		res.Created = true
		_, err = tx.Exec(`INSERT INTO standing_orders(id,text,saved_at,source,tags,pinned,updated_at,kind,area,ref,scope,scope_ref,name,description,used_at,uses)
 VALUES(?,?,?,?,?,0,0,?,?,'',?,?,?,?,?,1)`, id, body, now.Unix(), protocol.StandingSourceAgent, "[]", kind, project, scope, ref, name, desc, now.Unix())
	case err == nil:
		_, err = tx.Exec(`UPDATE standing_orders SET text=?,kind=?,description=?,updated_at=?,used_at=?,uses=uses+1,source=? WHERE id=?`,
			body, kind, desc, now.Unix(), now.Unix(), protocol.StandingSourceAgent, id)
	}
	if err != nil {
		return res, err
	}
	if err := tx.Commit(); err != nil {
		return res, err
	}
	res.ID, res.Name, res.Scope, res.Kind = id, name, scope, kind
	res.Similar = []protocol.NoteRef{}
	all, err := s.visibleNotes(agent, project, "")
	if err != nil {
		return res, err
	}
	res.Similar = similarNotes(all, id, name, desc, body)
	if len(res.Similar) > 0 {
		res.Note = "Similar notes exist. If one says the same thing, merge: write its name again with the joined text instead of keeping two."
	}
	return res, nil
}

var noteStopWords = map[string]bool{"the": true, "and": true, "for": true, "with": true, "that": true, "this": true, "from": true,
	"are": true, "was": true, "not": true, "use": true, "when": true, "into": true, "its": true, "has": true, "have": true,
	"will": true, "all": true, "any": true, "can": true, "but": true, "you": true, "your": true, "our": true, "then": true}

// noteTokens is the set of lower-case words of a text, without short words and
// common ones. Name hyphens and dots separate words.
func noteTokens(text string) map[string]bool { return noteTokensMin(text, 3) }

func noteTokensMin(text string, min int) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len([]rune(w)) >= min && !noteStopWords[w] {
			out[w] = true
		}
	}
	return out
}

// similarNotes ranks the other notes by how much their words overlap the new
// note (Jaccard over name, description and body). A shared word of the names
// counts as similar too: two notes about "release" are likely one.
func similarNotes(all []protocol.StandingOrder, selfID, name, desc, body string) []protocol.NoteRef {
	mine := noteTokens(name + " " + desc + " " + body)
	mineName := noteTokens(name)
	type scored struct {
		o protocol.StandingOrder
		s float64
	}
	var hits []scored
	for _, o := range all {
		if o.ID == selfID {
			continue
		}
		theirs := noteTokens(o.Name + " " + o.Description + " " + o.Text)
		inter := 0
		for w := range mine {
			if theirs[w] {
				inter++
			}
		}
		union := len(mine) + len(theirs) - inter
		score := 0.0
		if union > 0 {
			score = float64(inter) / float64(union)
		}
		nameShared := false
		for w := range noteTokens(o.Name) {
			if mineName[w] && len(w) >= 4 {
				nameShared = true
			}
		}
		if score >= 0.2 || nameShared {
			if nameShared {
				score += 1
			}
			hits = append(hits, scored{o, score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].s != hits[j].s {
			return hits[i].s > hits[j].s
		}
		return hits[i].o.Name < hits[j].o.Name
	})
	if len(hits) > protocol.NotesSimilarMax {
		hits = hits[:protocol.NotesSimilarMax]
	}
	out := []protocol.NoteRef{}
	for _, h := range hits {
		out = append(out, protocol.NoteRef{ID: h.o.ID, Name: h.o.Name, Description: h.o.Description, Scope: h.o.Scope, Kind: h.o.Kind})
	}
	return out
}

// FindNotes answers agent_notes_find: the notes a session sees that match the
// text, bodies included. With no text it lists the most used. Reading with a
// text counts as a use, which keeps a note out of the review list.
func (s *Store) FindNotes(session string, q protocol.NotesFindRequest) (protocol.NotesFindResult, error) {
	return s.findNotes(session, q, time.Now())
}

func (s *Store) findNotes(session string, q protocol.NotesFindRequest, now time.Time) (protocol.NotesFindResult, error) {
	res := protocol.NotesFindResult{Notes: []protocol.Note{}}
	if q.Scope != "" && !protocol.ValidNoteScope(q.Scope) {
		return res, errors.New("scope must be agent, project or owner")
	}
	agent, project, err := s.noteScopes(session)
	if err != nil {
		return res, err
	}
	all, err := s.visibleNotes(agent, project, q.Scope)
	if err != nil {
		return res, err
	}
	for _, o := range all {
		if noteStale(o, now) {
			res.StaleCount++
		}
	}
	terms := noteTokensMin(q.Text, 2)
	phrase := strings.ToLower(strings.TrimSpace(q.Text))
	type scored struct {
		o protocol.StandingOrder
		s int
	}
	var hits []scored
	for _, o := range all {
		if q.Stale && !noteStale(o, now) {
			continue
		}
		score := 0
		if len(terms) > 0 {
			name, desc, body := strings.ToLower(o.Name), strings.ToLower(o.Description), strings.ToLower(o.Text)
			if strings.Contains(name, phrase) {
				// The whole text names the note: it ranks above partial matches.
				score += 5
			}
			for t := range terms {
				switch {
				case strings.Contains(name, t):
					score += 3
				case strings.Contains(desc, t):
					score += 2
				case strings.Contains(body, t):
					score++
				}
			}
			if score == 0 {
				continue
			}
		}
		hits = append(hits, scored{o, score})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].s != hits[j].s {
			return hits[i].s > hits[j].s
		}
		return moreUsed(hits[i].o, hits[j].o)
	})
	res.Total = len(hits)
	if len(hits) > protocol.NotesFindMax {
		hits = hits[:protocol.NotesFindMax]
	}
	var read []any
	for i, h := range hits {
		res.Notes = append(res.Notes, noteOf(h.o, now))
		// Only the best matches count as read. If every note a broad search
		// returned counted, use would no longer tell the useful ones apart.
		if len(terms) > 0 && i < notesUseTop {
			read = append(read, h.o.ID)
		}
	}
	if len(read) > 0 {
		if _, err := s.db.Exec(`UPDATE standing_orders SET used_at=?,uses=uses+1 WHERE id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(read)), ",")+`)`, append([]any{now.Unix()}, read...)...); err != nil {
			return res, err
		}
	}
	if res.StaleCount > 0 {
		res.Note = fmt.Sprintf("%d notes were not read or written for %d days. They are flagged stale; the owner reviews them. Nothing is deleted by age.", res.StaleCount, protocol.NotesStaleDays)
	}
	return res, nil
}

// moreUsed orders notes by use, then by last use, then by name.
func moreUsed(a, b protocol.StandingOrder) bool {
	if a.Uses != b.Uses {
		return a.Uses > b.Uses
	}
	if a.UsedAt != b.UsedAt {
		return a.UsedAt > b.UsedAt
	}
	return a.Name < b.Name
}

// notesIndex is the block a session reads at its start and with its charter:
// the titles of its own agent's notes, its project's and the owner-wide ones.
// Each scope shows at most NotesIndexPerScope titles, the most used first, so
// the block has a fixed short length however many notes there are. The shown
// titles are listed by name, so a change in use does not change the text. It is
// empty when the session has no notes.
func (s *Store) notesIndex(session string) (string, error) {
	agent, project, err := s.noteScopes(session)
	if err != nil {
		// A session with no resolvable identity has no notes. Its charter
		// does not fail because of that.
		return "", nil
	}
	all, err := s.visibleNotes(agent, project, "")
	if err != nil || len(all) == 0 {
		return "", err
	}
	var b strings.Builder
	more := 0
	for _, g := range []struct{ scope, title string }{
		{protocol.NoteScopeAgent, "Notes of your agent"},
		{protocol.NoteScopeProject, "Notes of your project"},
		{protocol.NoteScopeOwner, "Notes for every session"},
	} {
		var in []protocol.StandingOrder
		for _, o := range all {
			if o.Scope == g.scope {
				in = append(in, o)
			}
		}
		if len(in) == 0 {
			continue
		}
		sort.SliceStable(in, func(i, j int) bool { return moreUsed(in[i], in[j]) })
		if len(in) > protocol.NotesIndexPerScope {
			more += len(in) - protocol.NotesIndexPerScope
			in = in[:protocol.NotesIndexPerScope]
		}
		sort.SliceStable(in, func(i, j int) bool { return in[i].Name < in[j].Name })
		fmt.Fprintf(&b, "%s:\n", g.title)
		for _, o := range in {
			// A description is another agent's text in this session's start
			// text, so it cannot carry agent-net's own framing marker.
			o.Description = markers.Neutralize(o.Description)
			b.WriteString(protocol.NotesIndexLine(noteOf(o, time.Time{})) + "\n")
		}
	}
	text := "[agent-net] Your notes: titles only. Read a body with agent_notes_find; save one with agent_note_write (same name updates it).\n" + b.String()
	if more > 0 {
		text += fmt.Sprintf("%d more notes: search them with agent_notes_find.\n", more)
	}
	return strings.TrimRight(text, "\n"), nil
}
