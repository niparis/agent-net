package protocol

import "strings"

// Sign-in asks. An ask of kind sign_in names a machine, a CLI and why its
// interactive login is needed. The command is never supplied by an agent or a
// caller: it comes from this fixed table, keyed by provider. Only the owner's
// click opens the pane, and only on the machine the ask names.

// SignInProviderInfo is one CLI agent-net may open a login for.
type SignInProviderInfo struct {
	// Provider is the key a caller may name.
	Provider string
	// Label is the short CLI name shown in the ask and the pane title.
	Label string
	// Reason says why the login is needed, in the owner's words.
	Reason string
	// Argv is the exact command the pane runs. It is never built from input.
	Argv []string
}

// SignInProviders is the fixed allowlist. Add a provider here only with its
// exact login command; there is no other way to choose a command.
var SignInProviders = map[string]SignInProviderInfo{
	"alibaba": {
		Provider: "alibaba",
		Label:    "Alibaba Bailian CLI",
		Reason:   "the Alibaba token-plan quota needs your console login",
		Argv:     []string{"bl", "auth", "login", "--console"},
	},
}

// SignInProvider returns the allowlisted CLI for provider.
func SignInProvider(provider string) (SignInProviderInfo, bool) {
	info, ok := SignInProviders[provider]
	return info, ok
}

// SignInNeeded says whether a provider error is the known "sign in" remedy.
// The markers are the ones quota-axi reports for the alibaba CLI.
func SignInNeeded(provider, errText string) bool {
	if _, ok := SignInProvider(provider); !ok {
		return false
	}
	text := strings.ToLower(errText)
	switch provider {
	case "alibaba":
		return strings.Contains(text, "no console access token found") ||
			strings.Contains(text, "bl auth login --console")
	}
	return false
}

// SignInQuery asks the gateway which machine and provider an open sign-in ask
// names. Only the ask's ID comes from the caller.
type SignInQuery struct {
	AskID string `json:"ask_id"`
}

// SignInTarget is the gateway's answer to SignInQuery.
type SignInTarget struct {
	AskID    string `json:"ask_id"`
	Provider string `json:"provider"`
	Label    string `json:"label"`
	NodeID   string `json:"node_id"`
	Hostname string `json:"hostname,omitempty"`
}

// FrameSignInPrepare tells the daemon of the machine an ask names to open the
// login pane in its own herdr. The gateway sends it only after the owner
// clicked the ask.
const FrameSignInPrepare = "node.sign_in_prepare"

// SignInPrepare asks the target machine's daemon to open the login pane in its
// own herdr. The gateway sends it with an ask the owner already clicked.
type SignInPrepare struct {
	AskID    string `json:"ask_id"`
	Provider string `json:"provider"`
}

// SignInPrepareResult answers SignInPrepare. A false Opened carries the reason
// in Note; there is no ssh fallback.
type SignInPrepareResult struct {
	Opened bool   `json:"opened"`
	Host   string `json:"host,omitempty"`
	Pane   string `json:"pane,omitempty"`
	Note   string `json:"note,omitempty"`
}

// SignInRaise is the CTO operation sign_in_raise: raise one sign-in ask for a
// known provider on a machine. Command is present only so an attempt to send
// one is refused instead of ignored.
type SignInRaise struct {
	Provider string   `json:"provider"`
	Machine  string   `json:"machine"`
	Reason   string   `json:"reason,omitempty"`
	Command  []string `json:"command,omitempty"`
}

// SignInOpenRequest is the owner action on a sign_in ask. It carries no
// command and no provider: the daemon reads both from the stored ask.
type SignInOpenRequest struct {
	AskID       string `json:"ask_id"`
	HerdrSocket string `json:"herdr_socket,omitempty"`
	HerdrPaneID string `json:"herdr_pane_id,omitempty"`
}
