package gateway

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/inventory"
	"github.com/niparis/agent-net/internal/protocol"
)

// The skill archive (decision 0169) stores skill versions as database rows.
// Nothing is delivered from it yet; the Tools tab and the existing skill code
// keep working until task 5.

// skillArchiveSource says where one version came from.
type skillArchiveSource struct {
	Kind    string
	Node    string
	Project string
	Path    string
	URL     string
	Commit  string
	Session string
}

// sourceLine is the one line the CTO and the owner see for a version.
func (s skillArchiveSource) sourceLine() string {
	switch s.Kind {
	case "git":
		line := "git " + s.URL
		if s.Path != "" {
			line += " " + s.Path
		}
		if len(s.Commit) >= 8 {
			line += " @" + s.Commit[:8]
		}
		return line
	case "project":
		line := "project " + s.Project
		if s.Path != "" {
			line += " " + s.Path
		}
		return line
	case "agent":
		return "agent " + s.Session
	case "agent-net":
		return "agent-net"
	case "plugin":
		// The plugin owns the folder; the archive keeps a copy.
		if name := inventory.PluginName(s.Path); name != "" {
			return "plugin " + name
		}
		return "plugin"
	}
	return "machine " + s.Node + ":" + s.Path
}

// skillArchiveEpoch is the record time. UnixNano, like the skill store.
func (g *Gateway) skillArchiveEpoch() int64 { return g.store.now().UnixNano() }

// validSkillName keeps one name usable as a database key and a folder name.
// The archive has no length cap: the owner asked for none.
func validSkillName(name string) bool {
	if name == "" || name == "." || name == ".." || strings.TrimSpace(name) != name {
		return false
	}
	if strings.ContainsAny(name, "/\\\x00") {
		return false
	}
	for _, r := range name {
		if r < 0x20 {
			return false
		}
	}
	return true
}

// skillArchiveContent is one skill read from a folder, bundle or repository.
type skillArchiveContent struct {
	Name        string
	Hash        string
	Bundle      []byte
	Description string
	// SizeTokens is the whole bundle; ContextTokens is the name plus the
	// description, which every session loads.
	SizeTokens    int
	ContextTokens int
}

// tokenEstimate is bytes divided by four, rounded up.
func tokenEstimate(bytes int64) int { return int((bytes + 3) / 4) }

// skillContextTokens is what a session loads for a skill before it is used: its
// name and its description.
func skillContextTokens(name, description string) int {
	return tokenEstimate(int64(len(name) + len(description)))
}

// skillContentFromDir reads and packs one skill folder. The token counts are
// estimates (see tokenEstimate): the whole bundle, and the name plus the
// description.
func skillContentFromDir(name, dir string) (skillArchiveContent, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return skillArchiveContent{}, err
	}
	defer root.Close()
	description := inventory.SkillDescription(root)
	hash, err := inventory.SkillHash(dir)
	if err != nil {
		return skillArchiveContent{}, err
	}
	bundle, err := inventory.PackSkillLimit(dir, inventory.ArchiveLimit)
	if err != nil {
		return skillArchiveContent{}, err
	}
	var bytes int64
	err = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type().IsRegular() {
			info, e := entry.Info()
			if e != nil {
				return e
			}
			bytes += info.Size()
		}
		return nil
	})
	if err != nil {
		return skillArchiveContent{}, err
	}
	return skillArchiveContent{Name: name, Hash: hash, Bundle: bundle, Description: description,
		SizeTokens: tokenEstimate(bytes), ContextTokens: skillContextTokens(name, description)}, nil
}

// archiveQuerier is what the latest rule reads: a database or a transaction.
type archiveQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

// archiveLatestOf answers the version a node following latest gets: the one the
// latest row names when that version is live, else the highest live version.
// It is 0 when the name has no live version.
func archiveLatestOf(q archiveQuerier, name string) (int, error) {
	var v int
	err := q.QueryRow(`SELECT COALESCE(
		(SELECT p.version FROM skill_archive_latest p WHERE p.name=? AND EXISTS(SELECT 1 FROM skill_archive a WHERE a.name=p.name AND a.version=p.version AND a.removed_at=0)),
		(SELECT MAX(version) FROM skill_archive WHERE name=? AND removed_at=0), 0)`, name, name).Scan(&v)
	return v, err
}

// archivePointers reads the latest row of every name: the version it names,
// which may be removed. The list and the lists index apply the live check.
func archivePointers(q interface {
	Query(query string, args ...any) (*sql.Rows, error)
}) (map[string]int, error) {
	rows, err := q.Query(`SELECT name, version FROM skill_archive_latest`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var name string
		var v int
		if err = rows.Scan(&name, &v); err != nil {
			return nil, err
		}
		out[name] = v
	}
	return out, rows.Err()
}

// setArchiveLatest names version as the latest of its name.
func setArchiveLatest(tx *sql.Tx, name string, version int) error {
	_, err := tx.Exec(`INSERT INTO skill_archive_latest(name,version) VALUES(?,?) ON CONFLICT(name) DO UPDATE SET version=excluded.version`, name, version)
	return err
}

// restoreArchiveLatest takes back a latest choice that a change made. It acts
// only while that version is still the latest: a later choice is kept. With no
// earlier latest, the row goes and the highest live version is used.
func restoreArchiveLatest(tx *sql.Tx, name string, version, previous int) error {
	var current int
	err := tx.QueryRow(`SELECT version FROM skill_archive_latest WHERE name=?`, name).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && current != version) {
		return nil
	}
	if err != nil {
		return err
	}
	if previous == 0 {
		_, err = tx.Exec(`DELETE FROM skill_archive_latest WHERE name=?`, name)
		return err
	}
	return setArchiveLatest(tx, name, previous)
}

// archiveCounts answers how many versions of one name are live and how many
// were removed. Version numbers are never reused, so the two add up to the
// highest number unless an undo deleted the newest row.
func archiveCounts(q archiveQuerier, name string) (live, removed int, err error) {
	err = q.QueryRow(`SELECT COALESCE(SUM(removed_at=0),0), COALESCE(SUM(removed_at<>0),0) FROM skill_archive WHERE name=?`, name).Scan(&live, &removed)
	return live, removed, err
}

// archiveRemovedCounts reads the removed versions of every name.
func archiveRemovedCounts(q interface {
	Query(query string, args ...any) (*sql.Rows, error)
}) (map[string]int, error) {
	rows, err := q.Query(`SELECT name, COUNT(*) FROM skill_archive WHERE removed_at<>0 GROUP BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var name string
		var n int
		if err = rows.Scan(&name, &n); err != nil {
			return nil, err
		}
		out[name] = n
	}
	return out, rows.Err()
}

// archiveBlock says whether intake must leave this content out, and who removed
// it: the content was removed, or its name was removed as a whole. An explicit
// add clears the block.
func archiveBlock(q archiveQuerier, name, hash string) (actor string, blocked bool, err error) {
	err = q.QueryRow(`SELECT actor FROM skill_archive_blocks WHERE name=? AND hash IN ('', ?) ORDER BY hash DESC LIMIT 1`, name, hash).Scan(&actor)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return actor, err == nil, err
}

// skillArchivePut stores one version. Same name and same content is the same
// version; a version that was removed before is restored, not duplicated.
// intake says the copy came from the intake. Then a name that already has a live
// version keeps its latest, and the copy is a candidate (decision 0169). An
// explicit add, a new version or a copy of the old store makes the new version
// the latest.
func (g *Gateway) skillArchivePut(c skillArchiveContent, source skillArchiveSource, by string, intake bool) (protocol.SkillArchiveAddResult, error) {
	out := protocol.SkillArchiveAddResult{Name: c.Name, Hash: c.Hash, Description: c.Description, SizeTokens: c.SizeTokens, ContextTokens: c.ContextTokens, NoDescription: c.Description == ""}
	if !validSkillName(c.Name) {
		return out, reject("bad skill name")
	}
	if len(c.Bundle) == 0 {
		return out, reject("skill bundle is empty")
	}
	if int64(len(c.Bundle)) > inventory.ArchiveLimit.Bytes {
		return out, reject("the skill is larger than the %d MB archive limit", inventory.ArchiveLimit.Bytes>>20)
	}
	if c.Hash == "" {
		return out, reject("skill has no content hash")
	}
	tx, err := g.store.db.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	now := g.skillArchiveEpoch()
	var version, removed int64
	var stored skillArchiveSource
	err = tx.QueryRow(`SELECT version, removed_at, source_kind, source_node, source_project, source_path, source_url, source_commit, source_session FROM skill_archive WHERE name=? AND hash=?`, c.Name, c.Hash).
		Scan(&version, &removed, &stored.Kind, &stored.Node, &stored.Project, &stored.Path, &stored.URL, &stored.Commit, &stored.Session)
	restored := false
	out.Source = source.sourceLine()
	// The latest and the live count are read before this add changes them.
	previous, latestErr := archiveLatestOf(tx, c.Name)
	if latestErr != nil {
		return out, latestErr
	}
	var live int
	if latestErr = tx.QueryRow(`SELECT COUNT(*) FROM skill_archive WHERE name=? AND removed_at=0`, c.Name).Scan(&live); latestErr != nil {
		return out, latestErr
	}
	switch {
	case err == nil && removed == 0:
		// An unchanged add keeps the source the version first came with.
		out.Version, out.State, out.Source = int(version), "unchanged", stored.sourceLine()
	case err == nil:
		if _, err = tx.Exec(`UPDATE skill_archive SET removed_at=0, added_by=?, added_at=?, source_kind=?, source_node=?, source_project=?, source_path=?, source_url=?, source_commit=?, source_session=? WHERE name=? AND hash=?`,
			by, now, source.Kind, source.Node, source.Project, source.Path, source.URL, source.Commit, source.Session, c.Name, c.Hash); err != nil {
			return out, err
		}
		out.Version, out.State, restored = int(version), "restored", true
	case !errors.Is(err, sql.ErrNoRows):
		return out, err
	default:
		if err = tx.QueryRow(`SELECT COALESCE(MAX(version),0)+1 FROM skill_archive WHERE name=?`, c.Name).Scan(&version); err != nil {
			return out, err
		}
		if _, err = tx.Exec(`INSERT INTO skill_archive(name,version,hash,content,description,tokens,context_tokens,source_kind,source_node,source_project,source_path,source_url,source_commit,source_session,added_by,added_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			c.Name, version, c.Hash, c.Bundle, c.Description, c.SizeTokens, c.ContextTokens, source.Kind, source.Node, source.Project, source.Path, source.URL, source.Commit, source.Session, by, now); err != nil {
			return out, err
		}
		out.Version, out.State = int(version), "added"
	}
	if !intake {
		// An explicit add is the way back for a removed name or content.
		if _, err = tx.Exec(`DELETE FROM skill_archive_blocks WHERE name=? AND (hash='' OR hash=?)`, c.Name, c.Hash); err != nil {
			return out, err
		}
	}
	if out.State != "unchanged" {
		if !intake || live == 0 {
			if err = setArchiveLatest(tx, c.Name, out.Version); err != nil {
				return out, err
			}
		}
		out.Change = protocol.NewID("sc")
		if _, err = tx.Exec(`INSERT INTO skill_archive_changes(id,action,name,version,versions,restored,actor,at,previous) VALUES(?,?,?,?,?,?,?,?,?)`,
			out.Change, "add", c.Name, out.Version, "[]", restored, by, now, previous); err != nil {
			return out, err
		}
	}
	if out.Versions, out.Removed, err = archiveCounts(tx, c.Name); err != nil {
		return out, err
	}
	if out.Latest, err = archiveLatestOf(tx, c.Name); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

// skillArchiveRemove takes the live versions of one name out of use: every
// one, or only the given version. What it removes stays out of later intakes
// until an explicit add (decision 0169).
func (g *Gateway) skillArchiveRemove(name string, only int, by string) (protocol.SkillArchiveAddResult, error) {
	out := protocol.SkillArchiveAddResult{Name: name}
	if !validSkillName(name) {
		return out, reject("bad skill name")
	}
	tx, err := g.store.db.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT version, hash FROM skill_archive WHERE name=? AND removed_at=0 AND (?=0 OR version=?) ORDER BY version`, name, only, only)
	if err != nil {
		return out, err
	}
	var versions []int
	var hashes []string
	for rows.Next() {
		var v int
		var hash string
		if err = rows.Scan(&v, &hash); err != nil {
			rows.Close()
			return out, err
		}
		versions = append(versions, v)
		hashes = append(hashes, hash)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(versions) == 0 && only > 0 {
		return out, reject("skill %q has no live version %d", name, only)
	}
	if len(versions) == 0 {
		return out, reject("skill %q is not in the archive", name)
	}
	now := g.skillArchiveEpoch()
	for i, v := range versions {
		if _, err = tx.Exec(`UPDATE skill_archive SET removed_at=? WHERE name=? AND version=?`, now, name, v); err != nil {
			return out, err
		}
		if _, err = tx.Exec(`INSERT INTO skill_archive_blocks(name,hash,actor,at) VALUES(?,?,?,?) ON CONFLICT(name,hash) DO UPDATE SET actor=excluded.actor, at=excluded.at`, name, hashes[i], by, now); err != nil {
			return out, err
		}
	}
	var live int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM skill_archive WHERE name=? AND removed_at=0`, name).Scan(&live); err != nil {
		return out, err
	}
	if live == 0 {
		// The whole name is gone: other content under it stays out too.
		if _, err = tx.Exec(`INSERT INTO skill_archive_blocks(name,hash,actor,at) VALUES(?,'',?,?) ON CONFLICT(name,hash) DO UPDATE SET actor=excluded.actor, at=excluded.at`, name, by, now); err != nil {
			return out, err
		}
	}
	list, _ := json.Marshal(versions)
	out.Change = protocol.NewID("sc")
	out.State, out.Version = "removed", versions[len(versions)-1]
	if out.Versions, out.Removed, err = archiveCounts(tx, name); err != nil {
		return out, err
	}
	if _, err = tx.Exec(`INSERT INTO skill_archive_changes(id,action,name,version,versions,actor,at) VALUES(?,?,?,?,?,?,?)`,
		out.Change, "remove", name, out.Version, string(list), by, now); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

// skillArchiveUndo takes back one recorded add, remove or promote.
func (g *Gateway) skillArchiveUndo(change, by string) (protocol.SkillArchiveAddResult, error) {
	out := protocol.SkillArchiveAddResult{}
	if change == "" {
		return out, reject("undo needs a change id")
	}
	tx, err := g.store.db.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var action, name, versions string
	var version, restored, undone, previous int64
	if err = tx.QueryRow(`SELECT action,name,version,versions,restored,undone_at,previous FROM skill_archive_changes WHERE id=?`, change).
		Scan(&action, &name, &version, &versions, &restored, &undone, &previous); err != nil {
		return out, reject("unknown archive change")
	}
	if undone != 0 {
		return out, reject("archive change was already undone")
	}
	now := g.skillArchiveEpoch()
	out.Name, out.Version = name, int(version)
	switch action {
	case "add":
		if restored != 0 {
			_, err = tx.Exec(`UPDATE skill_archive SET removed_at=? WHERE name=? AND version=?`, now, name, version)
			out.State = "removed"
		} else {
			_, err = tx.Exec(`DELETE FROM skill_archive WHERE name=? AND version=?`, name, version)
			out.State = "removed"
		}
	case "remove":
		var list []int
		if json.Unmarshal([]byte(versions), &list) != nil {
			return out, reject("archive change is unreadable")
		}
		for _, v := range list {
			if _, err = tx.Exec(`UPDATE skill_archive SET removed_at=0 WHERE name=? AND version=?`, name, v); err != nil {
				return out, err
			}
			if _, err = tx.Exec(`DELETE FROM skill_archive_blocks WHERE name=? AND hash IN ('', (SELECT hash FROM skill_archive WHERE name=? AND version=?))`, name, name, v); err != nil {
				return out, err
			}
		}
		out.State = "restored"
	case "promote":
		out.State = "reset"
	default:
		return out, reject("archive change is not undoable")
	}
	if err != nil {
		return out, err
	}
	if action == "add" || action == "promote" {
		if err = restoreArchiveLatest(tx, name, int(version), int(previous)); err != nil {
			return out, err
		}
	}
	if _, err = tx.Exec(`UPDATE skill_archive_changes SET undone_at=? WHERE id=?`, now, change); err != nil {
		return out, err
	}
	if out.Versions, out.Removed, err = archiveCounts(tx, name); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

// skillArchivePromote makes one live version the latest of its name. The
// change is recorded with the earlier latest, so the owner can undo it.
func (g *Gateway) skillArchivePromote(name string, version int, by string) (protocol.SkillArchiveAddResult, error) {
	out := protocol.SkillArchiveAddResult{Name: name, Version: version}
	if !validSkillName(name) {
		return out, reject("bad skill name")
	}
	if version <= 0 {
		return out, reject("promote needs a version")
	}
	tx, err := g.store.db.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	err = tx.QueryRow(`SELECT hash, description, tokens, context_tokens FROM skill_archive WHERE name=? AND version=? AND removed_at=0`, name, version).
		Scan(&out.Hash, &out.Description, &out.SizeTokens, &out.ContextTokens)
	if errors.Is(err, sql.ErrNoRows) {
		return out, reject("skill %q has no live version %d", name, version)
	}
	if err != nil {
		return out, err
	}
	out.NoDescription = out.Description == ""
	previous, err := archiveLatestOf(tx, name)
	if err != nil {
		return out, err
	}
	if previous == version {
		out.State = "unchanged"
	} else {
		if err = setArchiveLatest(tx, name, version); err != nil {
			return out, err
		}
		out.State = "promoted"
		out.Change = protocol.NewID("sc")
		if _, err = tx.Exec(`INSERT INTO skill_archive_changes(id,action,name,version,versions,restored,actor,at,previous) VALUES(?,?,?,?,?,?,?,?,?)`,
			out.Change, "promote", name, version, "[]", 0, by, g.skillArchiveEpoch(), previous); err != nil {
			return out, err
		}
	}
	out.Latest = version
	if out.Versions, out.Removed, err = archiveCounts(tx, name); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

// skillArchiveList answers one row per name: its latest version, with the
// other live versions as candidates. A latest that was removed falls back to
// the highest live version, as archiveLatestOf does.
func (g *Gateway) skillArchiveList() (any, error) {
	rows, err := g.store.db.Query(`
		SELECT name, version, hash, description, tokens, context_tokens, added_by, added_at,
		 source_kind, source_node, source_project, source_path, source_url, source_commit, source_session
		FROM skill_archive WHERE removed_at=0 ORDER BY name, version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byName := map[string][]protocol.ArchivedSkill{}
	var names []string
	for rows.Next() {
		var s protocol.ArchivedSkill
		if err = rows.Scan(&s.Name, &s.Version, &s.Hash, &s.Description, &s.SizeTokens, &s.ContextTokens, &s.AddedBy, &s.AddedAt,
			&s.SourceKind, &s.SourceNode, &s.SourceProject, &s.SourcePath, &s.SourceURL, &s.SourceCommit, &s.SourceSession); err != nil {
			return nil, err
		}
		if _, seen := byName[s.Name]; !seen {
			names = append(names, s.Name)
		}
		byName[s.Name] = append(byName[s.Name], s)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	pointers, err := archivePointers(g.store.db)
	if err != nil {
		return nil, err
	}
	removed, err := archiveRemovedCounts(g.store.db)
	if err != nil {
		return nil, err
	}
	out := []protocol.ArchivedSkill{}
	for _, name := range names {
		group := byName[name]
		chosen := len(group) - 1
		if p := pointers[name]; p > 0 {
			for i := range group {
				if group[i].Version == p {
					chosen = i
				}
			}
		}
		s := group[chosen]
		s.Versions, s.Removed = len(group), removed[name]
		for i, v := range group {
			if i != chosen {
				s.Candidates = append(s.Candidates, v.Version)
			}
		}
		s.NoDescription = s.Description == ""
		s.Source = skillArchiveSource{Kind: s.SourceKind, Node: s.SourceNode, Project: s.SourceProject, Path: s.SourcePath, URL: s.SourceURL, Commit: s.SourceCommit, Session: s.SourceSession}.sourceLine()
		out = append(out, s)
	}
	return map[string]any{"skills": out}, nil
}

// skillArchiveAdd takes one skill folder from an online machine.
func (g *Gateway) skillArchiveAdd(ctx context.Context, p protocol.SkillArchiveAdd, by string) (protocol.SkillArchiveAddResult, error) {
	if p.Location.NodeID == "" || p.Location.Location == "" {
		return protocol.SkillArchiveAddResult{}, reject("add needs a machine and a folder")
	}
	name, hash, bundle, err := g.fetchSkillBundle(ctx, "", p.Location.NodeID, p.Location.Location, true)
	if err != nil {
		return protocol.SkillArchiveAddResult{}, err
	}
	dir, err := os.MkdirTemp("", "agent-net-archive-add-")
	if err != nil {
		return protocol.SkillArchiveAddResult{}, errors.New("cannot stage skill bundle")
	}
	defer os.RemoveAll(dir)
	if inventory.UnpackSkillLimit(bundle, dir, inventory.ArchiveLimit) != nil {
		return protocol.SkillArchiveAddResult{}, reject("invalid or oversized skill bundle")
	}
	content, err := skillContentFromDir(name, dir)
	if err != nil {
		return protocol.SkillArchiveAddResult{}, reject("cannot read skill: %v", err)
	}
	content.Hash = hash
	return g.skillArchivePut(content, skillArchiveSource{Kind: "machine", Node: p.Location.NodeID, Path: p.Location.Location}, by, false)
}

// skillArchiveBundled stores a skill folder that the daemon sent in its own
// bundle (decision 0171). The bytes must unpack to one skill folder, which is
// checked by its name and content hash, as an add from a machine is.
func (g *Gateway) skillArchiveBundled(p protocol.SkillArchiveBundled, by string) (protocol.SkillArchiveAddResult, error) {
	if !validSkillName(p.Name) || len(p.Bundle) == 0 {
		return protocol.SkillArchiveAddResult{}, reject("bundled skill needs a name and a bundle")
	}
	dir, err := os.MkdirTemp("", "agent-net-archive-bundled-")
	if err != nil {
		return protocol.SkillArchiveAddResult{}, errors.New("cannot stage skill bundle")
	}
	defer os.RemoveAll(dir)
	folder := filepath.Join(dir, p.Name)
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return protocol.SkillArchiveAddResult{}, errors.New("cannot stage skill bundle")
	}
	if inventory.UnpackSkillLimit(p.Bundle, folder, inventory.ArchiveLimit) != nil {
		return protocol.SkillArchiveAddResult{}, reject("invalid or oversized skill bundle")
	}
	content, err := skillContentFromDir(p.Name, folder)
	if err != nil {
		return protocol.SkillArchiveAddResult{}, reject("cannot read skill: %v", err)
	}
	if content.Hash, err = inventory.SkillHash(folder); err != nil {
		return protocol.SkillArchiveAddResult{}, reject("cannot verify skill bundle")
	}
	return g.skillArchivePut(content, skillArchiveSource{Kind: "agent-net"}, by, false)
}

// fetchSkillBundle reads and checks one skill file tree from a machine. The
// item may be empty: the location alone names it. kinds limits the identity
// kinds, and defaults to skills.
func (g *Gateway) fetchSkillBundle(ctx context.Context, item, nodeID, location string, archive bool, kinds ...string) (name, hash string, bundle []byte, err error) {
	if len(kinds) == 0 {
		kinds = []string{"skill"}
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(kinds)), ",")
	kindArgs := make([]any, len(kinds))
	for i, kind := range kinds {
		kindArgs[i] = kind
	}
	query := `SELECT i.name,i.hash FROM tool_identities i JOIN tool_locations l ON l.identity_id=i.id
		WHERE i.kind IN (` + placeholders + `) AND l.node_id=? AND json_extract(l.observation,'$.location')=? AND json_extract(l.observation,'$.state')='ok'`
	args := append(kindArgs, nodeID, location)
	if item != "" {
		query = `SELECT i.name,i.hash FROM tool_identities i JOIN tool_locations l ON l.identity_id=i.id
			WHERE i.id=? AND i.kind IN (` + placeholders + `) AND l.node_id=? AND json_extract(l.observation,'$.location')=? AND json_extract(l.observation,'$.state')='ok'`
		args = append([]any{item}, args...)
	}
	if err = g.store.db.QueryRow(query+` LIMIT 1`, args...).Scan(&name, &hash); err != nil || hash == "" {
		return "", "", nil, reject("skill location is not a current observation")
	}
	return g.requestSkillBundle(ctx, nodeID, name, hash, location, archive)
}

// requestSkillBundle asks one machine for the folder it listed. An import checks
// the bytes against the hash it asked for. An archive request gets the archive
// limit, a bundle over one part comes in parts, and the folder is stored under
// the hash of the content the machine packed now (decision 0169).
func (g *Gateway) requestSkillBundle(ctx context.Context, nodeID, name, hash, location string, archive bool) (string, string, []byte, error) {
	host := g.conn(nodeID)
	if host == nil {
		return "", "", nil, reject("skill location is offline")
	}
	lim := inventory.ImportLimit
	if archive {
		lim = inventory.ArchiveLimit
	}
	raw, err := host.request(ctx, "node.skill_bundle", protocol.SkillBundleRequest{Name: name, Hash: hash, Location: location, Archive: archive}, 60*time.Second)
	if err != nil {
		return "", "", nil, reject("cannot obtain skill bundle: %v", err)
	}
	var first protocol.SkillBundle
	if json.Unmarshal(raw, &first) != nil {
		return "", "", nil, reject("invalid skill bundle")
	}
	data, err := readBundleParts(ctx, host, first)
	if err != nil {
		return "", "", nil, err
	}
	dir, err := os.MkdirTemp("", "agent-net-skill-bundle-")
	if err != nil {
		return "", "", nil, errors.New("cannot stage skill bundle")
	}
	defer os.RemoveAll(dir)
	if inventory.UnpackSkillLimit(data, dir, lim) != nil {
		return "", "", nil, reject("invalid or oversized skill bundle")
	}
	got, err := inventory.SkillHash(dir)
	if err != nil {
		return "", "", nil, reject("cannot verify skill bundle")
	}
	if !archive && got != hash {
		return "", "", nil, reject("skill changed since its inventory scan")
	}
	canonical, err := inventory.PackSkillLimit(dir, lim)
	if err != nil {
		return "", "", nil, reject("cannot verify skill bundle")
	}
	return name, got, canonical, nil
}

// readBundleParts joins a bundle that the machine sent in parts (decision
// 0169). The first answer holds part 0 and names a snapshot for the others. The
// size and the SHA-256 of the whole are checked before anything is unpacked.
func readBundleParts(ctx context.Context, host *nodeConn, first protocol.SkillBundle) ([]byte, error) {
	if first.Parts == 0 {
		return first.Data, nil
	}
	if first.Snapshot == "" || first.Size <= 0 || first.Size > inventory.ArchiveLimit.Bytes ||
		protocol.PartCount(first.Size) != first.Parts ||
		int64(len(first.Data)) != min(first.Size, protocol.TransferPartSize) {
		return nil, reject("invalid skill bundle parts")
	}
	out := make([]byte, 0, first.Size)
	out = append(out, first.Data...)
	for i := 1; i < first.Parts; i++ {
		raw, err := host.request(ctx, "node.skill_bundle", protocol.SkillBundleRequest{Snapshot: first.Snapshot, Part: i}, 60*time.Second)
		if err != nil {
			return nil, reject("cannot obtain skill bundle part %d: %v", i, err)
		}
		var part protocol.SkillBundle
		if json.Unmarshal(raw, &part) != nil {
			return nil, reject("invalid skill bundle part %d", i)
		}
		remaining := first.Size - int64(i)*protocol.TransferPartSize
		if int64(len(part.Data)) != min(remaining, protocol.TransferPartSize) {
			return nil, reject("invalid skill bundle part %d", i)
		}
		out = append(out, part.Data...)
	}
	sum := sha256.Sum256(out)
	if hex.EncodeToString(sum[:]) != first.SHA256 {
		return nil, reject("skill bundle changed while it was read")
	}
	return out, nil
}

// validGitURL accepts the transports that fetch a repository. ext:: and other
// git helpers run programs, so they are refused.
func validGitURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "file", "git", "http", "https", "ssh":
		return u.Path != "" || u.Opaque != ""
	}
	return false
}

// skillArchiveAddGit clones one repository, takes every folder that holds a
// SKILL.md and stores it. No approval step: the owner said none.
func (g *Gateway) skillArchiveAddGit(ctx context.Context, p protocol.SkillArchiveAddGit, by string) (protocol.SkillArchiveMultiResult, error) {
	out := protocol.SkillArchiveMultiResult{Skills: []protocol.SkillArchiveAddResult{}}
	if !validGitURL(p.URL) {
		return out, reject("add a repository with a file, git, http, https or ssh URL")
	}
	dir, err := os.MkdirTemp("", "agent-net-skill-git-")
	if err != nil {
		return out, errors.New("cannot stage the repository")
	}
	defer os.RemoveAll(dir)
	clone := filepath.Join(dir, "repo")
	cmd := exec.CommandContext(ctx, "git", "clone", "--quiet", "--depth", "1", "--", p.URL, clone)
	cmd.Env = gitEnv(nil)
	if output, err := cmd.CombinedOutput(); err != nil {
		return out, reject("cannot clone the repository: %s", firstLine(string(output)))
	}
	head := exec.CommandContext(ctx, "git", "-C", clone, "rev-parse", "HEAD")
	head.Env = gitEnv(nil)
	raw, err := head.Output()
	if err != nil {
		return out, reject("cannot read the repository commit")
	}
	commit := strings.TrimSpace(string(raw))
	if sub := p.Path; sub != "" {
		clean := filepath.Clean(sub)
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return out, reject("the subfolder must stay inside the repository")
		}
		clone = filepath.Join(clone, clean)
	}
	// A skill bundle must not carry Git data, so the clone's metadata goes
	// before the folders are packed.
	_ = filepath.WalkDir(clone, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr == nil && entry.IsDir() && entry.Name() == ".git" {
			_ = os.RemoveAll(path)
			return filepath.SkipDir
		}
		return walkErr
	})
	roots, err := skillRoots(clone)
	if err != nil {
		return out, err
	}
	if len(roots) == 0 {
		return out, reject("no folder under the URL holds a SKILL.md")
	}
	for _, root := range roots {
		name := filepath.Base(root)
		if root == clone {
			name = strings.TrimSuffix(filepath.Base(strings.TrimSuffix(p.URL, "/")), ".git")
			if name == "" || strings.Contains(name, ":") {
				name = filepath.Base(dir)
			}
		}
		content, err := skillContentFromDir(name, root)
		if err != nil {
			return out, reject("cannot read %s: %v", name, err)
		}
		rel, _ := filepath.Rel(clone, root)
		sourcePath := ""
		if rel != "." {
			sourcePath = filepath.ToSlash(rel)
		}
		added, err := g.skillArchivePut(content, skillArchiveSource{Kind: "git", URL: p.URL, Path: sourcePath, Commit: commit}, by, false)
		if err != nil {
			return out, err
		}
		out.Skills = append(out.Skills, added)
	}
	out.Shared = sharedArchiveNames(out.Skills)
	return out, nil
}

// skillRoots lists the folders that hold a SKILL.md, sorted by path.
func skillRoots(root string) ([]string, error) {
	var roots []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			return nil
		}
		info, err := os.Stat(filepath.Join(path, "SKILL.md"))
		if err == nil && info.Mode().IsRegular() {
			roots = append(roots, path)
		}
		return nil
	})
	sort.Strings(roots)
	return roots, err
}

// sharedArchiveNames lists the names that gained a second content in this run.
func sharedArchiveNames(results []protocol.SkillArchiveAddResult) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range results {
		if r.Versions > 1 && (r.State == "added" || r.State == "restored") && !seen[r.Name] {
			seen[r.Name] = true
			out = append(out, r.Name)
		}
	}
	sort.Strings(out)
	return out
}

func gitEnv(extra []string) []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=/bin/false", "LC_ALL=C",
	}
	return append(env, extra...)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// archiveNameForItem names a skill version of the old store. The identity row
// is the normal source; the item id itself is a JSON array of kind, name, hash.
func (g *Gateway) archiveNameForItem(item string) string {
	var name string
	if g.store.db.QueryRow(`SELECT name FROM tool_identities WHERE id=? LIMIT 1`, item).Scan(&name) == nil && name != "" {
		return name
	}
	var parts []string
	if json.Unmarshal([]byte(item), &parts) == nil && len(parts) > 1 {
		return parts[1]
	}
	return ""
}

// intakeSkillGit copies every version of the old skills.git into the archive.
// It is safe to repeat: a known name and hash is unchanged.
func (g *Gateway) intakeSkillGit() (protocol.SkillArchiveMultiResult, error) {
	out := protocol.SkillArchiveMultiResult{Skills: []protocol.SkillArchiveAddResult{}}
	repo, err := g.skillRepository()
	if err != nil {
		return out, err
	}
	rows, err := g.store.db.Query(`SELECT item, hash, node_id, location FROM skill_versions ORDER BY imported_at, hash`)
	if err != nil {
		return out, err
	}
	type row struct {
		item, hash, node, location string
	}
	var list []row
	for rows.Next() {
		var r row
		if err = rows.Scan(&r.item, &r.hash, &r.node, &r.location); err != nil {
			rows.Close()
			return out, err
		}
		list = append(list, r)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	for _, r := range list {
		name := g.archiveNameForItem(r.item)
		if name == "" {
			continue
		}
		bundle, err := repo.read(r.item, r.hash)
		if err != nil {
			// A stored row that no longer holds a valid bundle cannot be
			// copied; the rest of the intake still runs.
			continue
		}
		dir, err := os.MkdirTemp("", "agent-net-archive-intake-")
		if err != nil {
			return out, err
		}
		content, err := func() (skillArchiveContent, error) {
			defer os.RemoveAll(dir)
			if inventory.UnpackSkillLimit(bundle, dir, inventory.ArchiveLimit) != nil {
				return skillArchiveContent{}, errors.New("invalid bundle")
			}
			return skillContentFromDir(name, dir)
		}()
		if err != nil {
			continue
		}
		source := skillArchiveSource{Kind: "machine", Node: r.node, Path: r.location}
		if r.node == "" && r.location == "" {
			source = skillArchiveSource{Kind: "git_store"}
		}
		added, err := g.skillArchivePut(content, source, "intake", false)
		if err != nil {
			return out, err
		}
		out.Skills = append(out.Skills, added)
	}
	if len(out.Skills) > 0 {
		if _, err = g.store.db.Exec(`INSERT INTO skill_archive_meta(key,value) VALUES('git_intake_at',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, strconv.FormatInt(g.skillArchiveEpoch(), 10)); err != nil {
			return out, err
		}
	}
	out.Shared = sharedArchiveNames(out.Skills)
	return out, nil
}

// skillArchiveStartIntake runs the copy of the old store once, at gateway
// start. A marker keeps later starts from repeating the work; a second run
// would store nothing new anyway.
func (g *Gateway) skillArchiveStartIntake() {
	var done string
	if err := g.store.db.QueryRow(`SELECT value FROM skill_archive_meta WHERE key='git_intake_done'`).Scan(&done); err == nil && done != "" {
		return
	}
	result, err := g.intakeSkillGit()
	if err != nil {
		g.logLine("error", "where", "skill archive intake", "err", err)
		return
	}
	if _, err = g.store.db.Exec(`INSERT INTO skill_archive_meta(key,value) VALUES('git_intake_done',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, strconv.FormatInt(g.skillArchiveEpoch(), 10)); err != nil {
		g.logLine("error", "where", "skill archive intake marker", "err", err)
		return
	}
	g.logLine("skill_archive.intake", "skills", len(result.Skills), "shared", strings.Join(result.Shared, ","))
}
