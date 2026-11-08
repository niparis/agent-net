package gateway

import (
	"github.com/niparis/agent-net/internal/protocol"
)

// The global level of the charter, alone. A session whose folder has a node
// already reads it in its effective charter (rules and refuses join across
// levels). It is read apart for a session with no node, and for a connection
// that has no session yet.

// globalLevel reads the global level of the charter.
func (r *charterResolver) globalLevel() (protocol.GlobalCharter, error) {
	var g protocol.GlobalCharter
	values, err := r.globalCharter()
	if err != nil {
		return g, err
	}
	for _, v := range values {
		switch v.attr {
		case "rules":
			g.Rules = v.value
		case "refuses":
			g.Refuses = v.value
		case "escalate":
			g.Escalate = v.value
		}
	}
	return g, nil
}

// GlobalCharter is the global level alone, for a connection that has no
// session yet.
func (s *Store) GlobalCharter() (protocol.GlobalCharter, error) {
	return s.newCharterResolver().globalLevel()
}

// withGlobalLevel gives a session that no node covers the global level. A
// session with node text already has it there, so shown is returned as it is.
func (s *Store) withGlobalLevel(shown string) (string, error) {
	if shown != "" {
		return shown, nil
	}
	g, err := s.GlobalCharter()
	if err != nil {
		return shown, err
	}
	return protocol.GlobalCharterText(g), nil
}
