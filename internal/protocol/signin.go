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
	// ImportArgv is the fixed command that reads a console token from stdin.
	ImportArgv []string
}

// SignInProviders is the fixed allowlist. Add a provider here only with its
// exact login command; there is no other way to choose a command.
var SignInProviders = map[string]SignInProviderInfo{
	"alibaba": {
		Provider:   "alibaba",
		Label:      "Alibaba Bailian CLI",
		Reason:     "the Alibaba token-plan quota needs your console login",
		Argv:       []string{"bl", "auth", "login", "--console", "--no-browser", "--console-site", "international"},
		ImportArgv: []string{"bl", "auth", "login", "--console", "--token-stdin", "--console-region", "ap-southeast-1", "--console-site", "international"},
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
// names. Only the ask's ID comes from the caller. The owner is the session the
// gateway checks before it opens anything.
type SignInQuery struct {
	AskID string `json:"ask_id"`
	Owner string `json:"owner,omitempty"`
}

// SignInPrepareRequest is the owner action that opens a login on one named
// machine. It carries a provider key only: there is no command field.
type SignInPrepareRequest struct {
	Machine  string `json:"machine"`
	Provider string `json:"provider"`
	Owner    string `json:"owner,omitempty"`
}

// SignInTarget is the gateway's answer to SignInQuery.
type SignInTarget struct {
	AskID    string `json:"ask_id"`
	Provider string `json:"provider"`
	Label    string `json:"label"`
	NodeID   string `json:"node_id"`
	Hostname string `json:"hostname,omitempty"`
}

// FrameSignInPrepare tells the daemon of the machine a sign-in names to open
// the login pane in its own herdr. The gateway sends it only after the owner
// opened the sign-in on the machine and provider the frame names.
const FrameSignInPrepare = "node.sign_in_prepare"

// SignInPrepare asks the target machine's daemon to open the login pane in its
// own herdr. It names a provider key only: there is no argv field.
type SignInPrepare struct {
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

// SignInOpenRequest is the owner action that opens a sign-in. The owner's own
// TUI names a stored ask. The CTO names a machine, an allowlisted provider and
// the owner's quoted words. Neither form carries a command: Command is present
// only so an attempt to send one is refused instead of ignored.
type SignInOpenRequest struct {
	AskID       string `json:"ask_id,omitempty"`
	Cancel      bool   `json:"cancel,omitempty"`
	HerdrSocket string `json:"herdr_socket,omitempty"`
	HerdrPaneID string `json:"herdr_pane_id,omitempty"`
	// Machine is a node ID or hostname, for the CTO's sign_in_open.
	Machine string `json:"machine,omitempty" jsonschema:"the machine to open the login on, by node ID or hostname"`
	// Provider is one key of the fixed allowlist in protocol/signin.go.
	Provider string `json:"provider,omitempty" jsonschema:"the allowlisted CLI, such as alibaba"`
	// OwnerWords is the CTO's quote of the owner's instruction in the chat.
	OwnerWords string   `json:"owner_words,omitempty" jsonschema:"a short quote of the owner's instruction in this chat"`
	Command    []string `json:"command,omitempty"`
}

// FrameSignInOpen is the gateway frame behind the owner operation
// sign_in_open. The daemon verifies the owner's quoted words and sends the
// machine and provider; the gateway relays a prepare to that machine.
const FrameSignInOpen = "signin.owner_open"

// SignInOpenFrame is FrameSignInOpen. Owner is the owner session the gateway
// checks, and WordsVerified says the daemon found the quote in the CTO chat.
type SignInOpenFrame struct {
	Request       SignInOpenRequest `json:"request"`
	Owner         string            `json:"owner"`
	Session       string            `json:"session,omitempty"`
	By            string            `json:"by,omitempty"`
	WordsVerified bool              `json:"words_verified,omitempty"`
}

// SignInTransfer carries a console token only in the encrypted node relay.
// The machine and provider come from the ask the owner opened, never from the
// token's caller.
type SignInTransfer struct {
	Machine  string `json:"machine"`
	Provider string `json:"provider"`
	Owner    string `json:"owner"`
	Token    string `json:"token"`
}

const FrameSignInImport = "node.sign_in_import"

// SignInImport is sent by the gateway to the machine named by the ask.
type SignInImport struct {
	Provider string `json:"provider"`
	Token    string `json:"token"`
}
