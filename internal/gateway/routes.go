package gateway

import (
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// The route table of decision 0152: where each automatic message, each Desk
// ask and each CTO intake alert goes. createInboxItem refuses an ask kind with
// no route, and TestRouteTableCoversEveryKind fails on a kind with no row, so
// a new kind gets a route before it ships.

// Route receivers.
const (
	routeAgent = "agent" // the session that must act
	routeChief = "chief" // the chief of staff's queue
	routeCTO   = "cto"   // the CTO intake queue
	routeOwner = "owner" // the owner directly
)

// Route classes.
const (
	routeAsk     = "ask"     // a Desk item (inbox_items)
	routeMessage = "message" // a message from agent-net to a session
	routeIntake  = "intake"  // an entry of the CTO intake queue
)

// When a CTO intake entry wakes the CTO: a P1 "CTO alert" item for the chief.
const (
	wakeNever      = "never"
	wakeAtOnce     = "at_once"     // the detector already waited (15 minutes silent, K runs, many workers)
	wakeRepeatHour = "repeat_hour" // the alert repeats for over an hour
)

// routeOAuth is the route key of an OAuth consent ask: an approval or question
// from the Connected apps flow.
const routeOAuth = "oauth"

type route struct {
	Class string
	Kind  string
	To    string
	// Auto is true when agent-net makes the item or message itself; false when
	// an agent (or the chief) asked for it.
	Auto bool
	// Wake is the wake rule of an intake kind.
	Wake string
	Note string
}

var routes = []route{
	// Agent only (unchanged).
	{Class: routeMessage, Kind: "step_task", To: routeAgent, Auto: true, Note: "step task, reminders, result, closed, merged outside, no work recorded"},
	{Class: routeMessage, Kind: "request_reminder", To: routeAgent, Auto: true, Note: "the session that owes the answer"},
	{Class: routeMessage, Kind: "idle_check", To: routeAgent, Auto: true, Note: "idle check, autonomy continue, loop tick (repo lead for a loop)"},
	{Class: routeMessage, Kind: "worker_notice", To: routeAgent, Auto: true, Note: "worker started, ended, failed, lost, idle, auto-continue"},
	{Class: routeMessage, Kind: "undelivered", To: routeAgent, Auto: true, Note: "undelivered, could not be woken, delivered to a waiting session: the sender"},
	{Class: routeMessage, Kind: "tools_disconnected", To: routeAgent, Auto: true, Note: "tools disconnected, native MCP removed: that session"},
	{Class: routeMessage, Kind: "member_blocked", To: routeAgent, Auto: true, Note: "member blocked or waiting: the team lead"},
	{Class: routeMessage, Kind: "charter_request", To: routeAgent, Auto: true, Note: "charter draft and project rules requests: the lead session"},
	{Class: routeMessage, Kind: "chief_batch", To: routeChief, Auto: true, Note: "ask batch, morning brief wake, nudge pause and resume"},

	// Desk asks: everything that wants the owner goes to the chief.
	{Class: routeAsk, Kind: protocol.AskKindApproval, To: routeChief, Note: "becomes a decision item"},
	{Class: routeAsk, Kind: protocol.AskKindStep, To: routeChief, Auto: true, Note: "step needs_owner"},
	{Class: routeAsk, Kind: protocol.AskKindQuestion, To: routeChief},
	{Class: routeAsk, Kind: protocol.AskKindIdea, To: routeChief},
	{Class: routeAsk, Kind: protocol.AskKindCheck, To: routeChief, Auto: true, Note: "raw input; never reaches the owner as is; loop paused"},
	{Class: routeAsk, Kind: protocol.AskKindWaiting, To: routeChief, Auto: true, Note: "after the debounce (0150)"},
	{Class: routeAsk, Kind: protocol.AskKindRejection, To: routeChief, Auto: true},
	{Class: routeAsk, Kind: protocol.AskKindEscalation, To: routeChief, Auto: true},
	{Class: routeAsk, Kind: protocol.AskKindStall, To: routeChief, Auto: true},
	{Class: routeAsk, Kind: protocol.AskKindHeld, To: routeChief, Auto: true, Note: "held request"},
	{Class: routeAsk, Kind: protocol.AskKindDraft, To: routeChief, Auto: true, Note: "charter draft asks"},
	{Class: routeAsk, Kind: protocol.AskKindLearned, To: routeChief, Auto: true, Note: "learned suggestions"},
	{Class: routeAsk, Kind: protocol.AskKindProposal, To: routeChief, Note: "a CTO proposal"},
	{Class: routeAsk, Kind: protocol.AskKindQuotaStuck, To: routeChief, Auto: true, Note: "work stuck on quota; links the CTO entry of the provider"},
	{Class: routeAsk, Kind: protocol.AskKindCTOAlert, To: routeChief, Auto: true, Note: "P1 wake of the CTO; the chief routes it to the lead that owns the cause"},
	{Class: routeAsk, Kind: protocol.AskKindMachine, To: routeChief, Auto: true, Note: "old rows only; a silent machine is now a CTO alert"},
	// The chief's own brief and agents' owner-only asks: not automatic.
	{Class: routeAsk, Kind: protocol.AskKindBrief, To: routeOwner, Note: "written by the chief"},
	{Class: routeAsk, Kind: protocol.AskKindPermission, To: routeOwner, Note: "an agent's proposal only the owner may approve"},
	// Owner directly: only sign-in and OAuth.
	{Class: routeAsk, Kind: protocol.AskKindSignIn, To: routeOwner, Auto: true, Note: "one-click sign-in"},
	{Class: routeAsk, Kind: routeOAuth, To: routeOwner, Auto: true, Note: "one-click OAuth consent"},

	// CTO intake queue.
	{Class: routeIntake, Kind: protocol.IntakeMachineSilent, To: routeCTO, Auto: true, Wake: wakeAtOnce, Note: "silent for 15 minutes; a machine that reported sleep never alerts (0137)"},
	{Class: routeIntake, Kind: protocol.IntakeHarnessUpdate, To: routeCTO, Auto: true, Wake: wakeRepeatHour},
	{Class: routeIntake, Kind: protocol.IntakeCompaction, To: routeCTO, Auto: true, Wake: wakeRepeatHour},
	{Class: routeIntake, Kind: protocol.IntakeMCPRepair, To: routeCTO, Auto: true, Wake: wakeRepeatHour},
	{Class: routeIntake, Kind: protocol.IntakeStruggle, To: routeCTO, Auto: true, Wake: wakeAtOnce, Note: "only across workers"},
	{Class: routeIntake, Kind: protocol.IntakeMergeQueue, To: routeCTO, Auto: true, Wake: wakeAtOnce, Note: "K = 3 failed runs in a row on the train tip"},
	{Class: routeIntake, Kind: protocol.IntakeQuota, To: routeCTO, Auto: true, Wake: wakeRepeatHour, Note: "one thread with the chief's stuck-work item"},
	{Class: routeIntake, Kind: protocol.IntakePickerOverride, To: routeCTO, Auto: true, Wake: wakeNever, Note: "the chief confirms first (0151)"},
	{Class: routeIntake, Kind: protocol.IntakeClaudeMouse, To: routeCTO, Auto: true, Wake: wakeNever, Note: "one digest line"},
	{Class: routeIntake, Kind: protocol.IntakePhonePaired, To: routeCTO, Auto: true, Wake: wakeNever, Note: "one digest line"},
}

var routeIndex = func() map[string]route {
	m := make(map[string]route, len(routes))
	for _, r := range routes {
		m[r.Class+":"+r.Kind] = r
	}
	return m
}()

// routeOf returns the route of a kind in a class.
func routeOf(class, kind string) (route, bool) {
	r, ok := routeIndex[class+":"+kind]
	return r, ok
}

// askRouteKey is the route key of a new Desk item: its kind, or oauth for a
// Connected apps consent.
func askRouteKey(item protocol.InboxItem) string {
	if item.FromKind == protocol.AskFromApps && (strings.HasPrefix(item.SourceRef, "app-consent:") || item.SourceRef == publicConsentRef) {
		return routeOAuth
	}
	return item.Kind
}

// neverOwnerAsk says whether the chief's timeout must keep an ask of this
// kind: a CTO alert and a quota stuck item are the chief's to route to a lead,
// and reach the owner only when the chief sends them.
func neverOwnerAsk(kind string) bool {
	return kind == protocol.AskKindCTOAlert || kind == protocol.AskKindQuotaStuck
}
