package protocol

// The setup integrations. They are the owner's answers for the whole
// network: the gateway keeps them, and every machine applies them. A machine may
// keep a local answer for one item, made with a per-item setup flag.

// Integration item IDs.
const (
	IntegrationPeerMessages = "claude_peer_messages"
	IntegrationCodexTools   = "codex_tools"
	IntegrationHintHook     = "claude_hint_hook"
	IntegrationPolicyHook   = "claude_policy_hook"
	IntegrationNeedsHook    = "claude_needs_hook"
	IntegrationWorkersMod   = "claude_workers_mod"
	IntegrationQuotaAXI     = "quota_axi"
)

// IntegrationSpec is one line of the setup question and one checkbox of the
// TUI form.
type IntegrationSpec struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// IntegrationSpecs lists the integrations in the order they are asked and
// shown.
func IntegrationSpecs() []IntegrationSpec {
	return []IntegrationSpec{
		{IntegrationPeerMessages, "Claude: accept messages from your other agents"},
		{IntegrationCodexTools, "Codex: call agent-net tools without asking"},
		{IntegrationHintHook, "Claude: hint which session to ask after an error"},
		{IntegrationPolicyHook, "Claude: apply the delegation policy you set in the TUI"},
		{IntegrationNeedsHook, `Claude: show "Needs you" in the TUI`},
		{IntegrationWorkersMod, "Claude: show your workers above the prompt"},
		{IntegrationQuotaAXI, "Quota: read your subscription quotas with quota-axi"},
	}
}

// Integrations is the owner's answer for one item or for all of them. The JSON
// names are the stable keys of the gateway setting.
type Integrations struct {
	PeerMessages bool `json:"peer_messages"`
	CodexTools   bool `json:"codex_tools"`
	HintHook     bool `json:"hint_hook"`
	PolicyHook   bool `json:"policy_hook"`
	NeedsHook    bool `json:"needs_hook"`
	WorkersMod   bool `json:"workers_mod"`
	QuotaAXI     bool `json:"quota_axi"`
}

// Get returns one answer by item ID.
func (i Integrations) Get(id string) bool {
	switch id {
	case IntegrationPeerMessages:
		return i.PeerMessages
	case IntegrationCodexTools:
		return i.CodexTools
	case IntegrationHintHook:
		return i.HintHook
	case IntegrationPolicyHook:
		return i.PolicyHook
	case IntegrationNeedsHook:
		return i.NeedsHook
	case IntegrationWorkersMod:
		return i.WorkersMod
	case IntegrationQuotaAXI:
		return i.QuotaAXI
	}
	return false
}

// Set changes one answer by item ID.
func (i *Integrations) Set(id string, on bool) {
	switch id {
	case IntegrationPeerMessages:
		i.PeerMessages = on
	case IntegrationCodexTools:
		i.CodexTools = on
	case IntegrationHintHook:
		i.HintHook = on
	case IntegrationPolicyHook:
		i.PolicyHook = on
	case IntegrationNeedsHook:
		i.NeedsHook = on
	case IntegrationWorkersMod:
		i.WorkersMod = on
	case IntegrationQuotaAXI:
		i.QuotaAXI = on
	}
}

// CountOn is how many integrations are on.
func (i Integrations) CountOn() int {
	n := 0
	for _, s := range IntegrationSpecs() {
		if i.Get(s.ID) {
			n++
		}
	}
	return n
}

// IntegrationsResult answers the owner settings calls. Set is false when the
// network has no answers yet.
type IntegrationsResult struct {
	Set          bool         `json:"set"`
	Integrations Integrations `json:"integrations"`
}
