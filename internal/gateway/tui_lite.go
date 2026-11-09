package gateway

import "github.com/niparis/agent-net/internal/protocol"

// liteAgentViews removes what no TUI list screen shows. The owner's TUI asks
// for it on every refresh; the full rows stay for every other reader. The
// fields left out are read again by opening the session or its card, which
// goes through a different call.
func liteAgentViews(agents []protocol.AgentView) {
	for i := range agents {
		a := &agents[i]
		a.NativeID = ""
		a.PeerTrust = false
		a.FirstSeen = 0
		a.RepoOverMax = false
		a.LimitedReason = ""
		// The effective charter repeats the flat fields below and adds the
		// whole source list and rules. The TUI reads the graph for those.
		a.Charter.Effective = nil
		a.Charter.Rules = nil
		a.Profile.Capabilities = nil
	}
}

// liteChangeView removes the long parts of a list row: evidence notes and
// fields that the list does not draw, and change columns it does not read.
// The TUI reads the change in full when the owner opens it.
func liteChangeView(v *protocol.ChangeView) {
	c := &v.Change
	c.Machine = ""
	c.TargetBranch = ""
	c.BaseCommit = ""
	c.Uncommitted = false
	c.Difficulty = ""
	c.Risk = 0
	c.SensitivePath = ""
	for i := range v.Steps {
		st := &v.Steps[i]
		st.Missing = nil
		if st.Evidence == nil {
			continue
		}
		// The view copies the record before it writes, so mutating it here
		// never reaches the stored evidence.
		e := *st.Evidence
		// The list shows the finding of a failed step and the note of one
		// that needs the owner. Every other note is for the detail screen.
		if e.Result != protocol.StepResultFail && e.Result != protocol.StepResultNeedsOwner {
			e.Note = ""
		}
		if len(e.Fields) > 0 {
			fields := map[string]string{}
			// These three are drawn in the list: the reconciliation kind in
			// the status line, and the CI run and release tag in the outcome.
			for _, key := range []string{"reconcile_kind", protocol.FieldRun, protocol.FieldTag} {
				if value, ok := e.Fields[key]; ok {
					fields[key] = value
				}
			}
			e.Fields = fields
		}
		e.ChangeID = ""
		e.Revision = 0
		e.ReportedBy = ""
		e.ByModelFamily = ""
		st.Evidence = &e
	}
}

// liteLearnedRows keeps only what the TUI reads of the owner's learned list:
// pending major suggestions for the quiet count, applied learned changes for
// the graph cards, and pending charter drafts. Each row loses the fields no
// screen shows, and the evidence keeps its first excerpt only.
func liteLearnedRows(rows []protocol.LearnedSuggestion) []protocol.LearnedSuggestion {
	out := rows[:0]
	for _, v := range rows {
		draft := v.Origin == "draft" && v.Status == "pending"
		pendingMajor := v.Status == "pending" && v.Severity == "major"
		applied := v.Status == "applied" && v.By == "learned"
		if !draft && !pendingMajor && !applied {
			continue
		}
		v.Kind = ""
		v.OldValue = ""
		v.RawPhrase = ""
		v.CreatedAt = 0
		v.ReadyAt = 0
		if len(v.Evidence) > 1 {
			v.Evidence = v.Evidence[:1]
		}
		out = append(out, v)
	}
	return out
}
