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
	info, ok := SignInProviders[strings.ToLower(strings.TrimSpace(provider))]
	return info, ok
}

// SignInProviderKey returns the allowlist key for a quota provider id such as
// "alibaba" or "alibaba-bl". Only a key that is in the allowlist is returned.
// A caller never gets to name an argv from this.
func SignInProviderKey(id string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(id))
	if _, ok := SignInProviders[key]; ok {
		return key, true
	}
	for known := range SignInProviders {
		if strings.HasPrefix(key, known+"-") || strings.HasPrefix(key, known+"_") {
			return known, true
		}
	}
	return "", false
}

// SignInNeeded says whether a provider error is the known "sign in" remedy.
// The markers are the ones quota-axi reports for the alibaba CLI.
func SignInNeeded(provider, errText string) bool {
	key, ok := SignInProviderKey(provider)
	if !ok {
		return false
	}
	text := strings.ToLower(errText)
	switch key {
	case "alibaba":
		return strings.Contains(text, "no console access token found") ||
			strings.Contains(text, "bl auth login --console")
	}
	return false
}

// SignInQuery asks the gateway which machine and provider an open sign-in ask
// names. Only the ask's ID comes from the caller. A prepare also names the
// owner session that clicked, so the gateway can check owner authority.
type SignInQuery struct {
	AskID string `json:"ask_id"`
	Owner string `json:"owner,omitempty"`
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
// own herdr. It names a provider key only: there is no argv field.
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
	// Session is the named herdr session of Pane ("agent-net"). An older
	// daemon leaves it empty: its pane is in the default session.
	Session string `json:"session,omitempty"`
}

// SignInRaise is the CTO operation sign_in_raise and the daemon's own raise:
// raise one sign-in ask for a known provider on a machine. Command is present
// only so an attempt to send one is refused instead of ignored.
type SignInRaise struct {
	Provider string   `json:"provider"`
	Machine  string   `json:"machine"`
	Reason   string   `json:"reason,omitempty"`
	Command  []string `json:"command,omitempty"`
}

// SignInClose closes the open sign-in ask of one provider on the machine that
// sends it, once its login remedy is gone.
type SignInClose struct {
	Provider string `json:"provider"`
	Reason   string `json:"reason,omitempty"`
}

// SignInOpenRequest is the owner action on a sign_in ask. It carries no
// command and no provider: the daemon reads both from the stored ask.
type SignInOpenRequest struct {
	AskID       string `json:"ask_id"`
	HerdrSocket string `json:"herdr_socket,omitempty"`
	HerdrPaneID string `json:"herdr_pane_id,omitempty"`
}
