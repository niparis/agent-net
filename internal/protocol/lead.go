package protocol

// A team can send the blocked steps of its members to its lead instead of the
// owner (decision 0100). The Claude Code hint hook reads that lead for its own
// session when a tool call was denied, so the model sends the step to the lead
// with its context and carries on.

// MethodTeamLead is the local read. It is not an operator method: a session
// asks for the lead of its own team, and it changes nothing.
const MethodTeamLead = "team_lead"

// TeamLeadRequest is MethodTeamLead. A plugin or MCP client is bound to its
// session and leaves Harness and NativeID empty. The command line (the Claude
// Code hook) names the session it runs for.
type TeamLeadRequest struct {
	Harness  string `json:"harness,omitempty"`
	NativeID string `json:"native_id,omitempty"`
}

// TeamLeadResult answers MethodTeamLead. LeadSession is empty when the
// session's team does not send blocked steps to a lead, or the session is the
// lead itself.
type TeamLeadResult struct {
	Team        string `json:"team,omitempty"`
	Lead        string `json:"lead,omitempty"`
	LeadSession string `json:"lead_session,omitempty"`
}

// QueryTeamLead is the gateway read behind MethodTeamLead. It takes a gateway
// session ID, because the daemon resolves the harness's native ID first.
type QueryTeamLead struct {
	SessionID string `json:"session_id"`
}
