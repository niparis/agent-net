package gateway

import (
	"strings"
	"unicode"

	"github.com/niparis/agent-net/internal/protocol"
)

// RecordSessionStartFailure records a start that the daemon answered as
// starting and then could not start. Only the owner of this machine may report
// one. The event is what the TUI feed and `agent-net` history show.
func (s *Store) RecordSessionStartFailure(node string, p protocol.SessionStartFailure) error {
	var allowed bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions WHERE id=? AND node_id=? AND harness='owner' AND worker='')`, p.Owner, node).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return reject("session start failure requires the owner of this machine")
	}
	if p.NoticeOnly {
		return nil
	}
	if len(p.Error) > 300 {
		p.Error = p.Error[:300]
	}
	detail := strings.TrimPrefix(pairs("start", p.StartID, "project", p.Project, "harness", p.Harness, "error", p.Error), " ")
	s.Event("session.start_failed", node, "", "", detail)
	return nil
}

// sessionStartFailureNotice runs only after the machine's owner was verified.
func (s *Store) sessionStartFailureNotice(p protocol.SessionStartFailure) (protocol.Message, error) {
	if p.Requester == "" || p.Requester == p.Owner {
		return protocol.Message{}, nil
	}
	name := protocol.CleanWaitingText(p.Project)
	if host := protocol.CleanWaitingText(p.Hostname); host != "" {
		name += " on " + host
	}
	body := "[agent-net] Session start failed: " + name + " (start " + p.StartID + "). "
	if p.ErrorCode != "" {
		body += "Reason code: " + protocol.CleanWaitingText(p.ErrorCode) + ". "
	}
	// Failure details contain the server, file and recovery action. They are
	// message content, so the waiting-field limit must not truncate them.
	reason := strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		if unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, p.Error)), " ")
	if reason == "" {
		reason = "no reason was reported"
	}
	m, _, err := s.storeAutomaticMessage(protocol.MsgSend{Recipient: p.Requester, Type: protocol.TypeNotification, Body: body + reason, IdempotencyKey: "start-failed-" + p.StartID})
	return m, err
}
