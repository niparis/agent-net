package gateway

import (
	"sort"

	"github.com/niparis/agent-net/internal/protocol"
)

// liteAgentViews removes what no TUI list screen shows. The owner's TUI asks
// for it on every refresh; the full rows stay for every other reader. The
// fields left out are read again by opening the session or its card, which
// goes through a different call. It also puts the rows in an order that a
// report from a session does not change (see the end of the function).
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
		// The list draws the scores and labels of a worker's difficulty line.
		// The judge's reasons and the picker's note are the longest text of the
		// row, and the worker log shows them from its own call.
		if w := a.Worker; w != nil && w.Assessment != nil {
			as := *w.Assessment
			as.Reasons, as.PickerNote, as.Bottleneck = nil, "", ""
			as.JudgeHarness, as.JudgeModel, as.RulesVersion = "", "", ""
			w.Assessment = &as
		}
		// An online session reports every few seconds, so its exact last_seen
		// moves with every report and makes the whole list look new. The TUI
		// reads it only for "idle for over an hour" on an online session, and
		// shows the age of an offline one, whose value does not move. Online
		// rows keep the minute.
		//
		// last_active is the same: a busy session moves it each time its
		// harness does something (the daemon reports it at most every 30
		// seconds). With a few busy sessions the list never hashed the same
		// twice, so every poll got all rows. The TUI draws it as minutes and
		// hours and orders by it, so the minute is enough.
		if a.Online {
			a.LastSeen -= a.LastSeen % 60
			a.LastActive -= a.LastActive % 60
		}
	}
	// The store lists the newest report first, so a report moves a row up and
	// the list looks new. With last_seen at the minute, the rows of one minute
	// are in id order: a report inside the minute changes nothing. The TUI
	// orders its sessions itself (by activity).
	sort.SliceStable(agents, func(i, j int) bool {
		if agents[i].LastSeen != agents[j].LastSeen {
			return agents[i].LastSeen > agents[j].LastSeen
		}
		return agents[i].SessionID < agents[j].SessionID
	})
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
