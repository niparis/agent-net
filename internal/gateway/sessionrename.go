package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/niparis/agent-net/internal/protocol"
)

// ErrSessionName is a session name the owner may not use.
type ErrSessionName struct{ Reason string }

func (e ErrSessionName) Error() string { return e.Reason }

// RenameSession gives a session the name the owner chose, or with an empty
// name returns it to its automatic project name (decision 0033). A name set
// here survives every later report: name_fixed tells a report to keep it. It may not be the name of another live session: agent_message_send matches
// names exactly, so a duplicate would be ambiguous.
func (s *Store) RenameSession(sessionID, name string, validate ...bool) (protocol.SessionRenameResult, error) {
	var res protocol.SessionRenameResult
	var auto, project, cwd, workerJSON string
	err := s.db.QueryRow(`SELECT name, repo_project, cwd, worker FROM sessions WHERE id = ?`, sessionID).
		Scan(&auto, &project, &cwd, &workerJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return res, reject("unknown session %q", sessionID)
	}
	if err != nil {
		return res, err
	}
	if auto == "CTO" {
		return res, ErrSessionName{"the CTO session has the reserved name CTO"}
	}
	name = strings.TrimSpace(name)
	if strings.EqualFold(name, "CTO") {
		return res, ErrSessionName{"CTO is a reserved session name"}
	}
	if name != "" {
		if len([]rune(name)) > protocol.MaxModelLen {
			return res, ErrSessionName{fmt.Sprintf("a session name is at most %d characters", protocol.MaxModelLen)}
		}
		for _, r := range name {
			if !unicode.IsPrint(r) {
				return res, ErrSessionName{"a session name has only printable characters"}
			}
		}
		if validSessionID(name) {
			return res, ErrSessionName{"a session name cannot look like a session ID"}
		}
		var live string
		err := s.db.QueryRow(`SELECT s.name FROM sessions s
			WHERE s.online = 1 AND s.id <> ? AND s.name = ? LIMIT 1`,
			sessionID, name).Scan(&live)
		if err == nil {
			return res, ErrSessionName{fmt.Sprintf("another live session is already named %q; use a different name or its session_id", name)}
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return res, err
		}
		if len(validate) > 0 && validate[0] {
			return res, nil
		}
		if _, err = s.db.Exec(`UPDATE sessions SET name = ?, name_fixed = 1 WHERE id = ?`, name, sessionID); err != nil {
			return res, err
		}
		res.Name = name
	} else {
		// Back to the automatic name. If another live session took it while
		// this one had a name of its own, take the lowest free number.
		var worker *protocol.WorkerInfo
		if workerJSON != "" {
			worker = &protocol.WorkerInfo{}
			json.Unmarshal([]byte(workerJSON), worker)
		}
		next, err := chooseSessionName(s.db, "", sessionID, auto, false, protocol.SessionInfo{Cwd: cwd,
			Repo: protocol.RepoInfo{Project: project}, Worker: worker})
		if err != nil {
			return res, err
		}
		if len(validate) > 0 && validate[0] {
			return res, nil
		}
		if _, err = s.db.Exec(`UPDATE sessions SET name = ?, name_fixed = 0 WHERE id = ?`, next, sessionID); err != nil {
			return res, err
		}
		res.Name = next
	}
	res.SessionID = sessionID
	s.Event("session.renamed", "", sessionID, "", res.Name)
	return res, nil
}
