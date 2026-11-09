package gateway

import (
	"database/sql"
	"errors"

	"github.com/niparis/agent-net/internal/protocol"
)

// citedNotes builds the notes for the messages that m's body cites by ID
// (decision 0121). A cited message gets a note only when the sender of m was
// its sender or its recipient. For any other ID nothing is added, so an
// unrelated session learns nothing, not even that the ID exists.
func (s *Store) citedNotes(m protocol.Message) ([]string, error) {
	var notes []string
	for _, id := range protocol.CitedIDs(m.Body) {
		if id == m.ID {
			continue
		}
		cited, ok, err := s.Message(id)
		if err != nil {
			return nil, err
		}
		if !ok || (cited.From != m.From && cited.To != m.From) {
			continue
		}
		toName, err := s.sessionDisplayName(cited.To)
		if err != nil {
			return nil, err
		}
		notes = append(notes, protocol.CitedNote(cited, cited.FromName, toName))
	}
	return notes, nil
}

// sessionDisplayName is the profile name, else the session name, else the title.
func (s *Store) sessionDisplayName(id string) (string, error) {
	var name, title string
	err := s.db.QueryRow(`SELECT COALESCE(NULLIF(p.name,''), s.name, ''), COALESCE(s.title,'')
		FROM sessions s LEFT JOIN profiles p ON p.session_id = s.id WHERE s.id=?`, id).Scan(&name, &title)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	if name == "" {
		name = title
	}
	return name, nil
}
